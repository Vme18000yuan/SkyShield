#!/usr/bin/env python3
"""Registration and certificate candidates; related roots are never automatic scope."""
from __future__ import annotations
import argparse
import json
import os
from pathlib import Path
import re
import shutil
import time
from urllib.parse import quote
from recon_common import atomic_json, lines, load_json, normalize_target, run_command, write_lines
from source_http import fetch_source


def registrable(host):
    try:
        import tldextract
        x = tldextract.TLDExtract(suffix_list_urls=())(host)
        return '.'.join(p for p in (x.domain, x.suffix) if p)
    except ImportError:
        multi = {'co.uk','org.uk','ac.uk','com.cn','net.cn','org.cn','com.au','net.au','com.br','com.hk','com.sg','co.jp','co.kr','co.nz','com.tw'}
        parts = host.rstrip('.').lower().split('.')
        return '.'.join(parts[-3:] if '.'.join(parts[-2:]) in multi else parts[-2:])


def main():
    p = argparse.ArgumentParser(description=__doc__)
    p.add_argument('root'); p.add_argument('outdir', nargs='?', type=Path)
    a = p.parse_args(); t = normalize_target(a.root)
    out = (a.outdir or Path('reports') / t['target_id']).resolve(); (out / 'raw').mkdir(parents=True, exist_ok=True)
    deadline = time.monotonic() + min(120, int(os.getenv('RECON_STAGE_TIMEOUT', '300')))
    statuses, org, email, domains = [], '', '', set()
    def command(name, argv, dest):
        if argv[0] == 'curl':
            record = fetch_source(argv[-1], out, name, stdout=dest, deadline=deadline,
                max_bytes=int(argv[argv.index('--max-filesize') + 1]), redirects='-fsSL' in argv,
                request_timeout=int(argv[argv.index('--max-time') + 1]))
        else:
            record = run_command(argv, out, name, timeout=max(0.1, min(30, deadline-time.monotonic())), stdout=dest)
        statuses.append(record); return record['status'] == 'success'
    if shutil.which('whois'):
        path = out / 'raw/org-whois.txt'
        if command('org-whois', ['whois', t['target']], path):
            text = path.read_text(errors='replace')
            m = re.search(r'^(?:Registrant Organization|org-name|Organization|registrant_org):\s*(.+)', text, re.M | re.I)
            if m: org = m[1].strip()
            m = re.search(r'^Registrant Email:\s*(\S+@\S+)', text, re.M | re.I)
            if m: email = m[1]
    rdap = out / 'raw/org-rdap.json'
    if shutil.which('curl') and command('org-rdap', ['curl', '-fsSL', '--max-time', '20', '--max-filesize', '1048576', 'https://rdap.org/domain/' + t['target']], rdap):
        data = load_json(rdap)
        if not isinstance(data, dict): statuses[-1].update(status='failed', error='invalid RDAP JSON')
        else:
            for entity in data.get('entities', []):
                if 'registrant' not in entity.get('roles', []): continue
                for row in entity.get('vcardArray', [None, []])[1]:
                    if len(row) >= 4 and isinstance(row[3], str):
                        if row[0] in ('org', 'fn') and not org: org = row[3]
                        if row[0] == 'email' and not email: email = row[3]
    if re.search(r'privacy|redacted|proxy|隐私', org, re.I): org = ''
    queries = [('domain', 'https://crt.sh/?q=%25.' + t['target'] + '&output=json')]
    if org: queries.append(('org', 'https://crt.sh/?O=' + quote(org) + '&output=json'))
    if shutil.which('curl'):
        for name, url in queries:
            path = out / ('raw/org-crt-' + name + '.json')
            if command('org-crt-' + name, ['curl', '-fsS', '--max-time', '30', '--max-filesize', '8388608', url], path):
                data = load_json(path)
                if not isinstance(data, list): statuses[-1].update(status='failed', error='invalid CT JSON'); continue
                for item in data:
                    for domain in item.get('name_value', '').splitlines():
                        domain = domain.lower().removeprefix('*.').rstrip('.')
                        if re.fullmatch(r'[a-z0-9.-]+\.[a-z]{2,}', domain): domains.add(domain)
    root = registrable(t['target'])
    related = sorted({registrable(d) for d in domains} - {root})
    write_lines(out / 'raw/org-related-domains.txt', related)
    status = 'partial' if not statuses or any(r['status'] != 'success' for r in statuses) else 'success'
    data = dict(status=status, org=org, registrant_email=email, related_roots=related, related_root_confidence='candidate', scope_expansion=False, sources=statuses)
    atomic_json(out / 'raw/org-assets.json', data)
    (out / '09-org-assets.md').write_text('# 组织资产候选 — ' + t['target'] + '\n\n组织 (Org): ' + (org or '<未公开/隐私保护>') + '\n\n注册邮箱: ' + (email or '<未公开>') + '\n\n关联根域为证书/注册信息线索，不代表已确认归属或已授权范围。\n\n' + '\n'.join('- ' + d for d in related) + '\n')
    return 2 if status == 'partial' else 0


if __name__ == '__main__':
    raise SystemExit(main())
