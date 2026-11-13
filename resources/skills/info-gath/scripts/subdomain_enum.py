#!/usr/bin/env python3
"""Parallel passive source collection with per-source status and scope parsing."""
from __future__ import annotations
import argparse
from concurrent.futures import ThreadPoolExecutor, as_completed
import ipaddress
import json
import os
from pathlib import Path
import secrets
import shutil
import time
from urllib.parse import urlsplit
import xml.etree.ElementTree as ET
from recon_common import atomic_json, in_scope, lines, normalize_target, run_command, write_lines


def main():
    p = argparse.ArgumentParser(description=__doc__)
    p.add_argument('root'); p.add_argument('outdir', nargs='?', type=Path)
    a = p.parse_args(); t = normalize_target(a.root)
    if t['kind'] != 'HOST': p.error('子域枚举需要域名')
    root = t['target']; out = (a.outdir or Path('reports') / t['target_id']).resolve()
    raw = out / 'raw'; raw.mkdir(parents=True, exist_ok=True)
    deadline = time.monotonic() + int(os.getenv('RECON_STAGE_TIMEOUT', '300'))
    mode = os.getenv('RECON_MODE', 'normal')
    specs = []
    def add(name, argv, filename, stdin=None):
        if shutil.which(argv[0]): specs.append((name, argv, raw / filename, stdin))
    proxy = os.getenv('RECON_HTTP_PROXY', '')
    managed = os.getenv('RECON_MANAGED_PROXY') == '1'
    add('subfinder', ['subfinder', '-d', root, '-all', '-silent', '-rl', os.getenv('RECON_HTTP_RATE', '10'), *(['-proxy', proxy] if proxy else [])], 'sub-subfinder.txt')
    if not managed:
        add('amass', ['amass', 'enum', '-passive', '-d', root], 'sub-amass.txt')
        add('assetfinder', ['assetfinder', '--subs-only', root], 'sub-assetfinder.txt')
        if os.getenv('PDCP_API_KEY'): add('chaos', ['chaos', '-d', root, '-silent'], 'sub-chaos.txt')
        if os.getenv('GITHUB_TOKEN'): add('github', ['github-subdomains', '-d', root, '-t', os.environ['GITHUB_TOKEN']], 'sub-github.txt')
        if shutil.which('gau'):
            add('gau', ['gau', '--subs', '--blacklist', 'png,jpg,gif,css,woff'], 'gau-all-urls.txt', root + '\n')
        else: add('wayback', ['waybackurls'], 'gau-all-urls.txt', root + '\n')
    add('crtsh', ['curl', '-fsS', '--connect-timeout', '5', '--max-time', '30', '--max-filesize', '8388608', f'https://crt.sh/?q=%25.{root}&output=json'], 'crtsh.json')
    if not managed: add('theharvester', ['theHarvester', '-d', root, '-b', 'crtsh,duckduckgo,bing,hackertarget,otx', '-f', str(raw / 'theharvester')], 'theharvester.stdout')
    statuses, domains, parameter_urls = [], set(), set()
    def collect(spec):
        name, argv, dest, stdin = spec
        record = run_command(argv, out, 'source-' + name, timeout=max(0.1, min(180, deadline-time.monotonic())), stdout=dest, stdin=stdin)
        found, params = set(), set()
        if record['status'] == 'success':
            try:
                if name == 'crtsh':
                    data = json.loads(dest.read_text())
                    values = [d['name_value'] for d in data]
                    values = [v for value in values for v in value.splitlines()]
                elif name in ('gau', 'wayback'):
                    values = []
                    for url in lines(dest):
                        parsed = urlsplit(url)
                        if parsed.hostname and in_scope(parsed.hostname, root):
                            values.append(parsed.hostname)
                            if parsed.query: params.add(url)
                elif name == 'theharvester':
                    file = raw / 'theharvester.xml'
                    values = [node.text or '' for node in ET.parse(file).iter('host')] if file.exists() else []
                else: values = lines(dest)
                for value in values:
                    value = value.strip().lower().removeprefix('*.').rstrip('.')
                    if in_scope(value, root): found.add(value)
                write_lines(raw / ('sub-' + name + '.txt'), found)
            except (ValueError, KeyError, TypeError, OSError, ET.ParseError) as exc:
                record.update(status='failed', error=str(exc))
        record['domains'] = len(found)
        return record, found, params
    with ThreadPoolExecutor(max_workers=2 if os.getenv('RECON_STEALTH') == '1' else 4) as pool:
        for future in as_completed([pool.submit(collect, spec) for spec in specs]):
            record, observed, params = future.result()
            statuses.append(record); domains.update(observed); parameter_urls.update(params)
    write_lines(out / '01-subdomains.txt', domains)
    write_lines(raw / 'wayback-params.txt', parameter_urls)
    if mode != 'fast' and shutil.which('dig'):
        wildcard = []
        for n in range(2):
            dest = raw / f'wildcard-query-{n}.txt'
            run_command(['dig', '+short', '+time=3', '+tries=1', 'A', secrets.token_hex(8) + '.' + root], out, 'wildcard-' + str(n), timeout=5, stdout=dest)
            for value in lines(dest):
                try: wildcard.append(str(ipaddress.ip_address(value)))
                except ValueError: pass
        atomic_json(raw / 'wildcard.json', dict(detected=bool(wildcard), ips=sorted(set(wildcard))))
    failed = not statuses or any(r['status'] != 'success' for r in statuses)
    atomic_json(raw / 'subdomain-status.json', dict(status='partial' if failed else 'success', sources=statuses, domains=len(domains)))
    print(f'Passive domains: {len(domains)}; sources: {len(statuses)}')
    return 2 if failed else 0


if __name__ == '__main__':
    raise SystemExit(main())
