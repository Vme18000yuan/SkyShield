#!/usr/bin/env python3
"""Explicit mode gates, isolated output and auditable recon execution."""
from __future__ import annotations
import argparse
import fcntl
import ipaddress
import json
import os
import re
import shutil
import signal
import sys
import time
from pathlib import Path
from urllib.parse import urlsplit
from recon_common import (atomic_json, in_scope, lines, load_json, normalize_target,
    normalize_url, origin, parse_dns, run_command, service_id, tool_info, utc_now, write_lines)

SCRIPTS = Path(__file__).resolve().parent


def arguments():
    p = argparse.ArgumentParser(description=__doc__)
    p.add_argument('target')
    mode = p.add_mutually_exclusive_group()
    mode.add_argument('--fast', action='store_true', help='仅第三方被动采集')
    mode.add_argument('--deep', action='store_true')
    p.add_argument('--stealth', action='store_true', help='全流程降速并关闭高噪声探测')
    p.add_argument('--validate', action='store_true', help='额外比较无效 token 响应，仍需业务复核')
    p.add_argument('--no-port-scan', action='store_true')
    p.add_argument('--no-cloud', action='store_true', help='不主动探测云桶候选')
    p.add_argument('--force', action='store_true', help='兼容旧入口；每次执行都新建独立 run')
    p.add_argument('--rate', type=int)
    p.add_argument('--http-rate', type=int, default=10)
    p.add_argument('--max-web-hosts', type=int, default=5)
    p.add_argument('--stage-timeout', type=int, default=300)
    p.add_argument('--cloud-max-requests', type=int, default=80)
    p.add_argument('--cloud-budget-seconds', type=int, default=60)
    p.add_argument('--seed-url', action='append', default=[])
    p.add_argument('--output-dir', type=Path, default=Path('reports'))
    p.add_argument('--log-file', type=Path)
    p.add_argument('--run-id', help=argparse.SUPPRESS)
    a = p.parse_args()
    for key in ('rate', 'http_rate', 'max_web_hosts', 'stage_timeout', 'cloud_max_requests', 'cloud_budget_seconds'):
        if getattr(a, key) is not None and getattr(a, key) < 1:
            p.error('--' + key.replace('_', '-') + ' 必须为正整数')
    if a.stealth and a.validate:
        p.error('--stealth 与 --validate 不能同时使用')
    return a


class Pipeline:
    def __init__(self, args, target, out, manifest=None):
        self.a, self.t, self.out = args, target, out
        self.mode = 'fast' if args.fast else 'deep' if args.deep else 'normal'
        self.rate = min(args.http_rate, 2) if args.stealth else args.http_rate
        self.env = dict(os.environ, RECON_MODE=self.mode, RECON_STEALTH=str(int(args.stealth)),
            RECON_VALIDATE=str(int(args.validate)), RECON_NO_PORTSCAN=str(int(args.no_port_scan)),
            RECON_SCOPE_HOST=target['target'], RECON_HTTP_RATE=str(self.rate),
            RECON_STAGE_TIMEOUT=str(args.stage_timeout), RECON_MAX_JS='20' if args.stealth else '50',
            RECON_MAX_API='30' if args.stealth else '100',
            MASSCAN_RATE=str(args.rate or (100 if args.stealth else 2000)),
            NMAP_TIMING='-T2' if args.stealth else '-T4', NMAP_NO_VULNERS='1',
            RECON_CLOUD_MAX_REQUESTS=str(args.cloud_max_requests),
            RECON_CLOUD_BUDGET=str(args.cloud_budget_seconds), RECON_CLOUD_WORKERS='4')
        seeds = ([target['entry_url']] if target['entry_url'] else []) + args.seed_url
        self.m = manifest if manifest is not None else dict(schema_version='2.0', run_id=out.name, target=target['target'],
            target_id=target['target_id'], input=target['raw'], kind=target['kind'],
            mode=self.mode, stealth=args.stealth, validate=args.validate, status='running',
            started_utc=utc_now(), entry_urls=sorted(set(map(normalize_url, seeds))),
            steps={}, tools={}, coverage={})
        if manifest is None:
            self.save()

    def save(self):
        atomic_json(self.out / 'manifest.json', self.m)

    def available(self, name, check=False):
        if name not in self.m['tools']:
            self.m['tools'][name] = tool_info(name) if check else dict(path=shutil.which(name), usable=bool(shutil.which(name)))
        return self.m['tools'][name]['usable']

    def command(self, name, args, **kwargs):
        remaining = getattr(self, 'deadline', time.monotonic() + self.a.stage_timeout) - time.monotonic()
        if remaining <= 0:
            raise TimeoutError('阶段总时长预算已耗尽')
        record = run_command(args, self.out, name, timeout=remaining, env=self.env, **kwargs)
        if record['status'] == 'timeout': self.phase_timeout = True
        return record['status'] == 'success'

    def step(self, name, callback=None, reason=''):
        print(f'[{name}] {reason}', flush=True)
        state = dict(status='skipped' if callback is None else 'running', reason=reason, started_utc=utc_now())
        self.m['steps'][name] = state
        self.save()
        if callback:
            self.deadline = time.monotonic() + self.a.stage_timeout
            self.phase_timeout = False
            try:
                result = callback() or {}
                state.update(status='success')
                state.update(result)
                if self.phase_timeout: state.update(status='timeout', reason='阶段总时长预算已耗尽')
            except TimeoutError as exc:
                state.update(status='timeout', reason=str(exc))
            except Exception as exc:
                state.update(status='failed', reason=str(exc))
                print(f'  FAILED: {exc}', file=sys.stderr, flush=True)
        state['finished_utc'] = utc_now()
        self.save()

    def script(self, name, *args, stdout=None):
        ok = self.command(name, ['bash', SCRIPTS / (name + '.sh'), *args], stdout=stdout)
        return dict(status='success' if ok else 'partial', reason='' if ok else '子脚本失败/超时；详见 commands.jsonl 与 stderr')

    def passive(self):
        state = self.script('subdomain_enum', self.t['target'], self.out)
        subs = [s for s in lines(self.out / '01-subdomains.txt') if in_scope(s, self.t['target'])]
        write_lines(self.out / '01-subdomains.txt', subs + [self.t['target']])
        return state

    def organization(self):
        state = self.script('org_assets', self.t['target'], self.out)
        data = load_json(self.out / 'raw/org-assets.json', {})
        aliases = {'org-whois': 'whois', 'org-rdap': 'rdap', 'org-crt-domain': 'certificate_transparency', 'org-crt-org': 'certificate_transparency'}
        sources = []
        for item in data.get('sources', []):
            name = item.get('name')
            if name not in aliases or item.get('status') == 'success':
                continue
            entry = dict(source=aliases[name], status='timeout' if item.get('status') == 'timeout' else 'failed')
            stderr = self.out / 'raw' / (name + '.stderr')
            if item.get('returncode') == 22 and stderr.is_file():
                with stderr.open(errors='replace') as stream:
                    match = re.search(r'\berror:\s*(\d{3})\b', stream.read(1024))
                if match:
                    entry['http_status'] = int(match.group(1))
            sources.append(entry)
        self.m['coverage']['organization'] = dict(failed_sources=sources)
        return state

    def brute(self):
        wl = Path(os.getenv('RECON_DNS_WORDLIST', '/usr/share/seclists/Discovery/DNS/subdomains-top1million-110000.txt'))
        resolvers = Path(os.getenv('RECON_RESOLVERS', '/usr/share/dnsx/resolvers.txt'))
        if not self.available('shuffledns') or not wl.is_file() or not resolvers.is_file():
            return dict(status='skipped', reason='缺少 shuffledns、字典或 resolver 文件')
        dest = self.out / 'raw/sub-bruteforce.txt'
        ok = self.command('dns-brute', ['shuffledns', '-d', self.t['target'], '-w', wl, '-r', resolvers, '-silent', '-o', dest])
        write_lines(self.out / '01-subdomains.txt', lines(self.out / '01-subdomains.txt') + [s for s in lines(dest) if in_scope(s, self.t['target'])])
        return dict(status='success' if ok else 'partial')

    def dns(self):
        records, failed, ok = [], 0, False
        if self.available('dnsx', True):
            dest = self.out / 'raw/dns-ip-resolve.jsonl'
            ok = self.command('dnsx', ['dnsx', '-l', self.out / '01-subdomains.txt', '-a', '-aaaa', '-json', '-silent', '-rl', str(self.rate), '-o', dest])
            if ok and dest.exists():
                try:
                    for line in lines(dest):
                        value = json.loads(line)
                        if not isinstance(value, dict) or not isinstance(value.get('host'), str):
                            raise ValueError('invalid DNS JSONL schema')
                        for key in ('a', 'aaaa'):
                            addresses = value.get(key, [])
                            for address in [addresses] if isinstance(addresses, str) else addresses:
                                ipaddress.ip_address(address)
                    records = parse_dns(dest.read_text(errors='replace'))
                except (ValueError, TypeError):
                    ok = False
                    self.m['tools']['dnsx']['output_error'] = 'invalid DNS JSONL; using dig fallback'
            elif ok:
                ok = False
        if not ok:
            if not self.available('dig'):
                raise RuntimeError('dnsx 不可用且 dig 缺失，DNS 解析未执行')
            for n, sub in enumerate(lines(self.out / '01-subdomains.txt')):
                for typ in ('A', 'AAAA'):
                    dest = self.out / f'raw/dns-{n}-{typ}.txt'
                    if not self.command(f'dns-{n}-{typ}', ['dig', '+short', '+time=3', '+tries=1', typ, sub], stdout=dest):
                        failed += 1
                        continue
                    for val in lines(dest):
                        try:
                            ip = ipaddress.ip_address(val)
                            if ip.version == (4 if typ == 'A' else 6):
                                records.append(dict(host=sub, type=typ, ip=str(ip)))
                        except ValueError:
                            pass
                time.sleep(1 / self.rate)
        records = [r for r in records if in_scope(r['host'], self.t['target'])]
        atomic_json(self.out / 'dns-records.json', records)
        write_lines(self.out / '04-ips.txt', [r['ip'] for r in records])
        if self.available('dig'):
            with (self.out / '02-dns-baseline.txt').open('w') as baseline:
                for typ in ('NS', 'MX', 'TXT', 'SOA', 'A', 'AAAA', 'CAA'):
                    dest = self.out / f'raw/dns-baseline-{typ}.txt'
                    if not self.command('dns-baseline-' + typ, ['dig', '+short', '+time=3', '+tries=1', typ, self.t['target']], stdout=dest):
                        failed += 1
                    baseline.write(f'=== {typ} ===\n' + dest.read_text(errors='replace'))
        return dict(status='partial' if failed else 'success', failed_queries=failed, records=len(records))

    def axfr(self):
        ok = self.command('axfr', [sys.executable, SCRIPTS / 'dns_zone_transfer.py', self.t['target'], '--output', self.out / 'axfr.json'])
        data = load_json(self.out / 'axfr.json', {})
        outcomes = [item.get('status') for item in data.get('results', []) if isinstance(item, dict)]
        self.m['coverage']['axfr'] = {name: outcomes.count(name) for name in ('success', 'refused', 'failed', 'timeout')}
        return dict(status='success' if ok else 'partial', reason='' if ok else 'AXFR 检查失败，不能判为全部拒绝')

    def cdn(self):
        self.available('whois')
        self.available('dnsx')
        return self.script('find_real_ip', self.t['target'], self.out / '01-subdomains.txt', self.out, stdout=self.out / '03-cdn-realip.md')

    def ports(self):
        self.available('nmap')
        state = self.script('port_scan', self.out / '04-ips.txt', self.out)
        data = load_json(self.out / 'raw/port-scan-status.json', {})
        targets = data.get('targets', []) if isinstance(data.get('targets'), list) else []
        items = [item for item in data.get('results', []) if isinstance(item, dict) and item.get('stage') != 'service-detail'] if isinstance(data.get('results'), list) else []
        total = len(targets)
        self.m['coverage']['ports'] = dict(total=total,
            completed=sum(item.get('status') == 'success' for item in items),
            timed_out=sum(item.get('status') == 'timeout' for item in items),
            failed=sum(item.get('status') == 'failed' for item in items),
            unprocessed=sum(item.get('status') == 'unprocessed' for item in items) + max(0, total - len(items)))
        return state

    def http(self):
        seeds = self.m['entry_urls'][:]
        if self.t['kind'] == 'HOST':
            seeds += lines(self.out / '01-subdomains.txt')
        elif not seeds:
            host = self.t['target']
            host = f'[{host}]' if ':' in host else host
            seeds = [f'https://{host}/', f'http://{host}/']
        alive, records, ok = [], [], False
        if self.available('httpx', True):
            dest = self.out / 'raw/httpx-alive.jsonl'
            proxy = ['-proxy', os.environ['RECON_HTTP_PROXY']] if os.getenv('RECON_HTTP_PROXY') else []
            ok = self.command('httpx-alive', ['httpx', '-silent', '-json', '-status-code', '-title', '-tech-detect', '-ip', '-cdn', '-tls-grab', '-web-server', '-follow-host-redirects', '-rl', str(self.rate), '-threads', '2' if self.a.stealth else '10', *proxy, '-o', dest], stdin='\n'.join(seeds) + '\n')
            if ok and dest.exists():
                for line in lines(dest):
                    try:
                        item = json.loads(line)
                        item['url'] = normalize_url(item['url'])
                        if in_scope(urlsplit(item['url']).hostname, self.t['target']):
                            records.append(item)
                    except (ValueError, KeyError, TypeError):
                        ok = False
                        break
            elif ok:
                ok = False
        errors = 0
        if not ok:
            records = []
            if not self.available('curl'):
                raise RuntimeError('httpx 不可用/执行失败且 curl 缺失，HTTP 探测未执行')
            for n, seed in enumerate(dict.fromkeys(seeds)):
                candidates = [seed] if '://' in seed else ['https://' + seed + '/', 'http://' + seed + '/']
                for url in candidates:
                    meta = self.out / f'raw/http-{n}-meta.txt'
                    success = self.command(f'http-{n}', ['curl', '-ksI', '--connect-timeout', '3', '--max-time', '8', '--max-filesize', '65536', '-o', self.out / f'raw/http-{n}-headers.txt', '-w', '%{http_code}', url], stdout=meta)
                    code = meta.read_text().strip()
                    if success and code.isdigit() and 100 <= int(code) <= 599:
                        records.append(dict(url=normalize_url(url), status_code=int(code), source='curl'))
                        break
                    errors += 1
                time.sleep(1 / self.rate)
        for r in records:
            alive.append(f"{r['url']} [{r.get('status_code', '')}] [{r.get('host', '')}] [{r.get('title', '')}]")
        atomic_json(self.out / 'http-records.json', records)
        write_lines(self.out / '04-alive.txt', alive)
        dns = load_json(self.out / 'dns-records.json', [])
        cdn_hosts = {urlsplit(r['url']).hostname for r in records if r.get('cdn') or r.get('cdn_name')}
        cdn_ips = {r['ip'] for r in dns if r['host'] in cdn_hosts}
        excluded = [r for r in dns if r['ip'] in cdn_ips]
        if dns:
            write_lines(self.out / '04-ips.txt', [r['ip'] for r in dns if r['ip'] not in cdn_ips])
        atomic_json(self.out / 'cdn-excluded.json', excluded)
        self.m['coverage']['http'] = dict(seeds=len(set(seeds)), alive=len(records), fallback=not ok, cdn_ips_excluded=len(cdn_ips))
        return dict(status='partial' if errors and not records else 'success', reason='curl fallback' if not ok else 'httpx')

    def web(self):
        by_origin = {}
        records = load_json(self.out / 'http-records.json', [])
        alive_origins = {origin(r['url']) for r in records}
        for url in self.m['entry_urls']:
            if origin(url) in alive_origins: by_origin.setdefault(origin(url), url)
        for r in sorted(records, key=lambda r: (urlsplit(r['url']).hostname != self.t['target'], 'admin' not in r['url'] and 'api' not in r['url'], r['url'])):
            by_origin.setdefault(origin(r['url']), r['url'])
        urls = list(by_origin.values())[:self.a.max_web_hosts]
        self.m['coverage']['web'] = dict(discovered_origins=len(by_origin), selected=len(urls), omitted=max(0, len(by_origin) - len(urls)), limit=self.a.max_web_hosts)
        if not urls:
            return dict(status='skipped', reason='无 HTTP 存活入口')
        self.available('katana')
        entry_file = self.out / 'raw/web-entrypoints.json'
        atomic_json(entry_file, urls)
        self.save()
        ok = self.command('web-batch', [sys.executable, SCRIPTS / 'web_batch.py', entry_file, self.out])
        batch = load_json(self.out / 'raw/web-batch-status.json', {})
        for name in ('attempted', 'completed', 'partial', 'failed', 'timed_out', 'pending', 'workers', 'per_worker_rate'):
            value = batch.get(name)
            if isinstance(value, int) and not isinstance(value, bool) and value >= 0:
                self.m['coverage']['web'][name] = value
        failures = sum(self.m['coverage']['web'].get(name, 0) for name in ('partial', 'failed', 'timed_out', 'pending'))
        return dict(status='success' if ok and batch.get('status') == 'success' else 'partial', failed_entrypoints=failures)

    def execute(self):
        t, a = self.t, self.a
        (self.out / '00-scope.md').write_text(f"# Recon Scope - {t['target']}\n\n- Mode: {self.mode}\n- Start: {self.m['started_utc']}\n- Input: {t['raw']}\n- Normalized target: {t['target']}\n- Type: {t['kind']}\n- Output key: {t['target_id']}\n- Run: {self.out.name}\n- Stealth: {a.stealth}\n")
        if t['kind'] == 'HOST':
            self.step('passive', self.passive)
            self.step('organization', self.organization)
        else:
            self.step('passive', reason='IP/CIDR 无子域枚举')
            self.step('organization', reason='IP/CIDR 无域名组织反查')
            write_lines(self.out / '04-ips.txt', [t['target']])
        if self.mode == 'fast':
            for name in ('dns_brute', 'dns', 'axfr', 'cdn', 'http', 'ports', 'web', 'cloud'):
                self.step(name, reason='fast 模式仅第三方被动采集')
        else:
            if t['kind'] == 'HOST':
                self.step('dns_brute', self.brute if a.deep and not a.stealth else None, '仅 deep 且非 stealth 时主动枚举')
                self.step('dns', self.dns)
                self.step('axfr', self.axfr if not a.stealth else None, 'stealth 跳过 AXFR' if a.stealth else '')
                self.step('cdn', self.cdn)
            self.step('http', self.http if t['kind'] != 'CIDR' else None, 'CIDR 不构造单一 Web URL' if t['kind'] == 'CIDR' else '')
            self.step('ports', None if a.no_port_scan else self.ports, '--no-port-scan' if a.no_port_scan else '')
            self.step('web', self.web if t['kind'] != 'CIDR' else None)
            self.step('cloud', (lambda: self.script('cloud_buckets', t['target'], self.out)) if t['kind'] == 'HOST' and not a.stealth and not a.no_cloud else None, '--no-cloud 或 stealth/IP/CIDR 跳过云桶候选探测' if a.no_cloud or a.stealth or t['kind'] != 'HOST' else '')
        self.m.update(status='partial' if any(s['status'] in ('failed', 'partial', 'timeout') for s in self.m['steps'].values()) else 'completed', finished_utc=utc_now())
        self.save()


def publish(base, run):
    legacy = base / 'runs' / ('legacy-' + str(time.time_ns()))
    known = {'raw', '00-scope.md', '01-subdomains.txt', '02-dns-baseline.txt', '02-axfr.txt',
        '03-cdn-realip.md', '03-dns-cross.txt', '04-alive.txt', '04-ips.txt',
        '06-fingerprints.md', '07-architecture.md', '09-org-assets.md',
        'manifest.json', 'commands.jsonl', 'dns-records.json', 'http-records.json',
        'axfr.json', 'cdn-excluded.json', 'recon.json', '08-recon-summary.md',
        '信息收集报告_' + run.parent.parent.name + '.md'}
    known.update(p.name for p in run.iterdir() if p.is_file())
    for name in sorted(known):
        dest = base / name
        if dest.exists() and not dest.is_symlink():
            legacy.mkdir(parents=True, exist_ok=True)
            dest.rename(legacy / name)
        if not dest.is_symlink():
            dest.symlink_to(Path('latest') / name, target_is_directory=name == 'raw')
    pointer = base / ('.latest-' + str(os.getpid()))
    pointer.symlink_to(Path('runs') / run.name, target_is_directory=True)
    pointer.replace(base / 'latest')


def main():
    a = arguments()
    try:
        t = normalize_target(a.target)
        for url in a.seed_url:
            if not in_scope(urlsplit(normalize_url(url)).hostname, t['target']):
                raise ValueError('--seed-url 超出目标范围')
    except (ValueError, UnicodeError) as exc:
        print(f'Invalid target: {exc}', file=sys.stderr)
        return 2
    base = a.output_dir.resolve() / t['target_id']
    base.mkdir(parents=True, exist_ok=True)
    with (base / '.lock').open('a') as lock:
        try:
            fcntl.flock(lock, fcntl.LOCK_EX | fcntl.LOCK_NB)
        except BlockingIOError:
            print('同一目标正在执行: ' + t['target'], file=sys.stderr)
            return 3
        run_id = a.run_id or time.strftime('%Y%m%dT%H%M%S') + f'-{time.time_ns() % 1000000000:09d}'
        if not re.fullmatch(r'[a-zA-Z0-9_-]+', run_id):
            print('Invalid run id', file=sys.stderr)
            return 2
        out = base / 'runs' / run_id
        out.mkdir(parents=True, exist_ok=False)
        (out / 'raw').mkdir()
        pipeline = Pipeline(a, t, out)
        def interrupt(signum, frame):
            raise KeyboardInterrupt
        signal.signal(signal.SIGTERM, interrupt)
        try:
            pipeline.execute()
        except KeyboardInterrupt:
            for state in pipeline.m['steps'].values():
                if state['status'] == 'running':
                    state.update(status='interrupted', reason='执行被中断')
            pipeline.m.update(status='interrupted', finished_utc=utc_now())
            pipeline.save()
        try:
            from recon_report import build_report
            result = build_report(out)
            pipeline.m['status'] = result['status']
            publish(base, out)
        except Exception as exc:
            pipeline.m.update(status='failed', report_error=str(exc))
            pipeline.save()
            print(f'报告生成失败: {exc}', file=sys.stderr)
            return 1
        print(f"[{pipeline.m['status']}] {base / ('信息收集报告_' + t['target_id'] + '.md')}", flush=True)
        return 0 if pipeline.m['status'] == 'completed' else 130 if pipeline.m['status'] == 'interrupted' else 2


if __name__ == '__main__':
    if '--log-file' in sys.argv:
        index = sys.argv.index('--log-file')
        if index + 1 < len(sys.argv):
            path = Path(sys.argv[index + 1]); path.parent.mkdir(parents=True, exist_ok=True)
            stream = path.open('a', buffering=1)
            class Tee:
                def __init__(self, original): self.original = original
                def write(self, text): self.original.write(text); stream.write(text)
                def flush(self): self.original.flush(); stream.flush()
            sys.stdout, sys.stderr = Tee(sys.stdout), Tee(sys.stderr)
    sys.exit(main())
