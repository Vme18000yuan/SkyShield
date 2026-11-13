#!/usr/bin/env python3
"""Normalize and merge targets before scheduling isolated recon processes."""
from __future__ import annotations
import argparse
from concurrent.futures import ThreadPoolExecutor, as_completed
import json
import os
from pathlib import Path
import subprocess
import signal
import sys
import threading
import time
from recon_common import atomic_json, load_json, normalize_target, supervised_command, terminate_tree, utc_now
from recon_report import build_batch


def main():
    p = argparse.ArgumentParser(description=__doc__)
    p.add_argument('targets', nargs='*'); p.add_argument('--targets', dest='targets_file', type=Path)
    p.add_argument('--agents', type=int, choices=range(1, 6), default=3)
    mode = p.add_mutually_exclusive_group()
    mode.add_argument('--fast', action='store_true'); mode.add_argument('--deep', action='store_true')
    p.add_argument('--mode', choices=('fast', 'normal', 'deep'))
    for flag in ('stealth', 'validate', 'no-port-scan', 'force'): p.add_argument('--' + flag, action='store_true')
    for flag in ('rate', 'http-rate', 'stage-timeout', 'max-web-hosts', 'cloud-max-requests', 'cloud-budget-seconds'):
        p.add_argument('--' + flag, type=int)
    p.add_argument('--dry-run', action='store_true')
    p.add_argument('--output-dir', type=Path, default=Path('reports'))
    a = p.parse_args()
    if a.stealth and a.validate: p.error('--stealth 与 --validate 不能同时使用')
    if a.mode and (a.fast or a.deep): p.error('--mode 与 --fast/--deep 不可同时设置')
    raw_targets = a.targets[:]
    if a.targets_file:
        try: raw_targets += [l.split('#', 1)[0].strip() for l in a.targets_file.read_text().splitlines() if l.split('#', 1)[0].strip()]
        except OSError as exc: p.error(str(exc))
    jobs = {}
    for raw in raw_targets:
        try: target = normalize_target(raw)
        except (ValueError, UnicodeError) as exc: p.error(f'{raw}: {exc}')
        job = jobs.setdefault(target['target_id'], dict(target=target['target'], target_id=target['target_id'], aliases=[], seeds=[]))
        job['aliases'].append(raw)
        if target['entry_url']: job['seeds'].append(target['entry_url'])
    if not jobs: p.error('至少需要一个目标')
    common = []
    selected_mode = a.mode or ('fast' if a.fast else 'deep' if a.deep else 'normal')
    if selected_mode != 'normal': common.append('--' + selected_mode)
    for flag in ('stealth', 'validate', 'no_port_scan', 'force'):
        if getattr(a, flag): common.append('--' + flag.replace('_', '-'))
    for flag in ('rate', 'http_rate', 'stage_timeout', 'max_web_hosts', 'cloud_max_requests', 'cloud_budget_seconds'):
        value = getattr(a, flag)
        if value is not None:
            if value < 1: p.error('--' + flag.replace('_', '-') + ' 必须为正整数')
            common += ['--' + flag.replace('_', '-'), str(value)]
    print(f'输入 {len(raw_targets)} 个，规范化后 {len(jobs)} 个目标，最大并发 {a.agents}', flush=True)
    if a.dry_run:
        print(json.dumps(list(jobs.values()), ensure_ascii=False, indent=2)); return 0
    started = time.monotonic(); timestamp = time.strftime('%Y%m%d_%H%M%S') + f'_{time.time_ns() % 1000000000:09d}'
    root = a.output_dir.resolve(); root.mkdir(parents=True, exist_ok=True)
    processes, mutex, statuses = {}, threading.Lock(), {}
    stopping = threading.Event()
    interrupted = False
    def request_stop(signum, frame):
        raise KeyboardInterrupt
    signal.signal(signal.SIGTERM, request_stop)
    for index, job in enumerate(jobs.values(), 1): job['run_id'] = timestamp + '_' + str(index)
    def execute(index, job):
        run_id = job['run_id']
        logfile = root / job['target_id'] / 'logs' / (run_id + '.log')
        logfile.parent.mkdir(parents=True, exist_ok=True)
        argv = [sys.executable, Path(__file__).with_name('recon_pipeline.py'), job['target'], '--output-dir', root, '--run-id', run_id, *common]
        for seed in sorted(set(job['seeds'])): argv += ['--seed-url', seed]
        print('[START] ' + job['target'], flush=True)
        with logfile.open('w') as stream:
            with mutex:
                if stopping.is_set():
                    return job['target_id'], dict(status='cancelled', run_id=run_id, aliases=job['aliases'])
                process = subprocess.Popen(supervised_command(argv), stdout=stream, stderr=subprocess.STDOUT, start_new_session=True)
                processes[process.pid] = process
            code = process.wait()
            with mutex: processes.pop(process.pid, None)
        manifest = load_json(root / job['target_id'] / 'runs' / run_id / 'manifest.json', {})
        status = manifest.get('status', 'locked' if code == 3 else 'failed')
        if stopping.is_set() and status in ('running', 'failed'):
            status = 'interrupted'
        if code != 0 and status == 'completed': status = 'failed'
        state = dict(status=status, returncode=code, run_id=run_id, log=str(logfile.relative_to(root)), aliases=job['aliases'])
        print('[' + status.upper() + '] ' + job['target'], flush=True)
        return job['target_id'], state
    pool = ThreadPoolExecutor(max_workers=a.agents)
    futures = []
    try:
        futures = [pool.submit(execute, i, job) for i, job in enumerate(jobs.values(), 1)]
        for future in as_completed(futures):
            key, state = future.result(); statuses[key] = state
            atomic_json(root / ('batch_' + timestamp + '.json'), dict(targets=statuses, elapsed_seconds=round(time.monotonic()-started, 2)))
    except KeyboardInterrupt:
        interrupted = True
        stopping.set()
        for future in futures: future.cancel()
        with mutex:
            active = list(processes.values())
        # Give recon a chance to persist an interrupted manifest and report.
        for process in active:
            try: process.send_signal(signal.SIGINT)
            except ProcessLookupError: pass
        for process in active:
            try: process.wait(timeout=4)
            except subprocess.TimeoutExpired:
                terminate_tree(process); process.wait()
    finally: pool.shutdown(wait=True, cancel_futures=True)
    for future in futures:
        if future.cancelled(): continue
        try:
            key, state = future.result(); statuses[key] = state
        except Exception:
            pass
    for job in jobs.values():
        state = statuses.setdefault(job['target_id'], dict(status='cancelled' if interrupted else 'failed', run_id=job['run_id'], aliases=job['aliases']))
        manifest_path = root / job['target_id'] / 'runs' / job['run_id'] / 'manifest.json'
        manifest = load_json(manifest_path)
        if interrupted and manifest and manifest.get('status') == 'running':
            manifest.update(status='interrupted', finished_utc=utc_now())
            for step in manifest.get('steps', {}).values():
                if step['status'] == 'running': step.update(status='interrupted', reason='批量执行被中断')
            atomic_json(manifest_path, manifest)
            state['status'] = 'interrupted'
    atomic_json(root / ('batch_' + timestamp + '.json'), dict(status='interrupted' if interrupted else 'completed' if all(s['status'] == 'completed' for s in statuses.values()) else 'partial', targets=statuses, elapsed_seconds=round(time.monotonic()-started, 2)))
    report = build_batch([j['target'] for j in jobs.values()], root, timestamp, round(time.monotonic()-started), statuses)
    print(str(report))
    return 130 if interrupted else 0 if all(s['status'] == 'completed' for s in statuses.values()) else 2


if __name__ == '__main__':
    raise SystemExit(main())
