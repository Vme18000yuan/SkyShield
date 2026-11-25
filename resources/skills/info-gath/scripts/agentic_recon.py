#!/usr/bin/env python3
"""Run one info-gath phase at a time for the product's AI agent.

The caller owns the target and run ID. Each invocation checkpoints the existing
Skill's evidence model, so the model can inspect a result before choosing the
next phase; no model-supplied paths or commands are executed here.
"""
from __future__ import annotations

import argparse
import fcntl
import hashlib
import json
import re
import signal
import sys
from pathlib import Path
from types import SimpleNamespace

from recon_common import load_json, normalize_target, utc_now, write_lines
from recon_pipeline import Pipeline, publish
from recon_report import build_report

STAGES = ("passive", "organization", "dns_brute", "dns", "axfr", "cdn", "http", "ports", "web")


def applicable(target, mode):
    if mode == "fast":
        return {"passive", "organization"} if target["kind"] == "HOST" else set()
    phases = {"ports"}
    if target["kind"] != "CIDR":
        phases.update(("http", "web"))
    if target["kind"] == "HOST":
        phases.update(("passive", "organization", "dns", "axfr", "cdn"))
        if mode == "deep":
            phases.add("dns_brute")
    return phases


def arguments():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("target")
    parser.add_argument("--mode", choices=("fast", "normal", "deep"), required=True)
    parser.add_argument("--stage", choices=(*STAGES, "finish"), required=True)
    parser.add_argument("--run-id", required=True)
    parser.add_argument("--output-dir", type=Path, required=True)
    parser.add_argument("--max-web-hosts", type=int, default=5)
    parser.add_argument("--stage-timeout", type=int, default=300)
    parser.add_argument("--resume", action="store_true", help=argparse.SUPPRESS)
    args = parser.parse_args()
    if not re.fullmatch(r"[a-zA-Z0-9_-]+", args.run_id):
        parser.error("invalid run ID")
    if not 1 <= args.max_web_hosts <= 50 or not 10 <= args.stage_timeout <= 300:
        parser.error("phase budget out of range")
    return args


def settings(args):
    return SimpleNamespace(fast=args.mode == "fast", deep=args.mode == "deep", stealth=False,
        validate=False, no_port_scan=False, no_cloud=True, force=False, rate=None,
        http_rate=10, max_web_hosts=args.max_web_hosts, stage_timeout=args.stage_timeout,
        cloud_max_requests=80, cloud_budget_seconds=60, seed_url=[])


def new_pipeline(args, target, out):
    out.mkdir(parents=True, exist_ok=False)
    (out / "raw").mkdir()
    pipeline = Pipeline(settings(args), target, out)
    (out / "00-scope.md").write_text(
        f"# Recon Scope - {target['target']}\n\n- Mode: {args.mode}\n"
        f"- Start: {pipeline.m['started_utc']}\n- Input: {target['raw']}\n"
        f"- Normalized target: {target['target']}\n- Type: {target['kind']}\n"
        f"- Output key: {target['target_id']}\n- Run: {args.run_id}\n- Stealth: False\n")
    if target['kind'] == 'HOST':
        write_lines(out / '01-subdomains.txt', [target['target']])
    else:
        write_lines(out / '04-ips.txt', [target['target']])
    return pipeline


def resume_pipeline(args, target, out):
    manifest = load_json(out / 'manifest.json', {})
    if not isinstance(manifest, dict) or any((
        manifest.get('run_id') != args.run_id,
        manifest.get('target') != target['target'],
        manifest.get('kind') != target['kind'],
        manifest.get('input') != target['raw'],
        manifest.get('mode') != args.mode,
        manifest.get('status') not in (('running', 'partial', 'interrupted') if getattr(args, 'resume', False) else ('running',)),
        not isinstance(manifest.get('steps'), dict),
    )):
        raise ValueError('采集状态与原始目标不匹配，无法继续')
    pipeline = Pipeline(settings(args), target, out, manifest=manifest)
    if manifest.get('status') != 'running':
        pipeline.m['status'] = 'running'
        pipeline.m.pop('finished_utc', None)
        pipeline.m.get('coverage', {}).pop('agentic', None)
        pipeline.save()
    return pipeline


def checkpoint(pipeline, final=False):
    """Produce the normal import envelope after each completed phase."""
    if not final:
        pipeline.m['status'] = 'partial'
    pipeline.save()
    data = build_report(pipeline.out)
    pipeline.m = load_json(pipeline.out / 'manifest.json', pipeline.m)
    if not final:
        pipeline.m['status'] = 'running'
        pipeline.save()
    return data


def execute(args):
    target = normalize_target(args.target)
    base = args.output_dir.resolve() / target['target_id']
    base.mkdir(parents=True, exist_ok=True)
    with (base / '.lock').open('a') as lock:
        try:
            fcntl.flock(lock, fcntl.LOCK_EX | fcntl.LOCK_NB)
        except BlockingIOError:
            raise ValueError('该目标的采集阶段仍在运行')
        out = base / 'runs' / args.run_id
        pipeline = resume_pipeline(args, target, out) if out.exists() else new_pipeline(args, target, out)
        allowed = applicable(target, args.mode)
        if args.stage == 'finish':
            missing = []
            for name in (*STAGES, 'cloud'):
                if name in pipeline.m['steps']:
                    if name in allowed and pipeline.m['steps'][name].get('status') == 'skipped':
                        missing.append(name)
                    continue
                required = name in allowed
                if required:
                    missing.append(name)
                pipeline.m['steps'][name] = dict(status='skipped', reason='Agent 未执行该阶段' if required else '当前目标或模式不执行')
            pipeline.m['coverage']['agentic'] = dict(unexecuted=missing)
            incomplete = bool(missing) or not allowed or any(
                state.get('status') in ('failed', 'partial', 'timeout', 'interrupted')
                or (name in allowed and state.get('status') == 'skipped')
                for name, state in pipeline.m['steps'].items())
            pipeline.m.update(status='partial' if incomplete else 'completed', finished_utc=utc_now())
            data = checkpoint(pipeline, final=True)
            publish(base, out)
            print(json.dumps({'stage': 'finish', 'status': data['status'], 'missing': missing}, ensure_ascii=False))
            return 0
        if args.stage not in allowed:
            raise ValueError('该阶段不适用于当前目标或采集模式')
        attempts = pipeline.m.setdefault('agentic_attempts', {})
        count = int(attempts.get(args.stage, 0))
        port_input = None
        if args.stage == 'ports':
            path = pipeline.out / '04-ips.txt'
            port_input = hashlib.sha256(args.mode.encode() + b'\0' + (path.read_bytes() if path.exists() else b'')).hexdigest()
            if (pipeline.m['steps'].get('ports', {}).get('status') == 'success'
                and pipeline.m.get('agentic_port_input') == port_input
                and (pipeline.out / 'raw/ports.json').is_file()
                and (pipeline.out / 'raw/port-scan-status.json').is_file()):
                data = checkpoint(pipeline)
                print(json.dumps({'stage': 'ports', 'status': 'success', 'attempt': count,
                    'reused': True, 'stats': data.get('stats', {})}, ensure_ascii=False))
                return 0
        if count >= 2:
            raise ValueError('该阶段已达到重试上限')
        attempts[args.stage] = count + 1
        if port_input is not None:
            pipeline.m['agentic_port_input'] = port_input
        pipeline.save()
        callbacks = {'passive': pipeline.passive, 'organization': pipeline.organization,
            'dns_brute': pipeline.brute, 'dns': pipeline.dns, 'axfr': pipeline.axfr,
            'cdn': pipeline.cdn, 'http': pipeline.http, 'ports': pipeline.ports,
            'web': pipeline.web}
        pipeline.step(args.stage, callbacks[args.stage])
        data = checkpoint(pipeline)
        print(json.dumps({'stage': args.stage, 'status': pipeline.m['steps'][args.stage]['status'],
            'attempt': count + 1, 'stats': data.get('stats', {})}, ensure_ascii=False))
        return 0


def main():
    signal.signal(signal.SIGTERM, lambda *_: (_ for _ in ()).throw(KeyboardInterrupt()))
    try:
        return execute(arguments())
    except KeyboardInterrupt:
        print('采集阶段已中断；此前完成的阶段仍被保留。', file=sys.stderr)
        return 130
    except (OSError, ValueError, TypeError, KeyError) as exc:
        print(f'采集阶段无法执行: {exc}', file=sys.stderr)
        return 2


if __name__ == '__main__':
    sys.exit(main())
