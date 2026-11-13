#!/usr/bin/env python3
"""Web fingerprints with bounded requests and mode-aware NSE selection."""
from __future__ import annotations
import argparse
import base64
import json
import os
import shutil
import sys
import time
from pathlib import Path
from urllib.parse import urljoin, urlsplit
from recon_common import atomic_json, lines, normalize_url, origin, run_command, tool_info


def main():
    p = argparse.ArgumentParser(description=__doc__)
    p.add_argument('url'); p.add_argument('outdir', nargs='?', type=Path, default=Path('fp-out'))
    a = p.parse_args(); a.url = normalize_url(a.url); a.outdir = a.outdir.resolve()
    (a.outdir / 'raw').mkdir(parents=True, exist_ok=True)
    if os.getenv('RECON_MODE') == 'fast':
        atomic_json(a.outdir / 'fingerprint-status.json', dict(status='skipped', reason='fast')); return 0
    deadline = time.monotonic() + int(os.getenv('RECON_STAGE_TIMEOUT', '300'))
    stealth = os.getenv('RECON_STEALTH') == '1'
    rate = min(int(os.getenv('RECON_HTTP_RATE', '10')), 2 if stealth else 10000)
    statuses, failed = [], False
    def run(name, argv, *, stdout=None, stdin=None):
        remaining = deadline - time.monotonic()
        if remaining <= 0:
            statuses.append(dict(name=name, status='timeout')); return False
        r = run_command(argv, a.outdir, name, timeout=remaining, stdout=stdout, stdin=stdin)
        statuses.append(r); return r['status'] == 'success'
    def curl(name, url, dest, headers=False):
        time.sleep(1 / rate)
        argv = ['curl', '-ksI' if headers else '-ks', '--connect-timeout', '3', '--max-time', '8', '--max-filesize', '2097152', '-o', dest, '-w', '%{http_code}', url]
        meta = a.outdir / ('raw/' + name + '-meta.txt')
        ok = run(name, argv, stdout=meta)
        return ok, meta.read_text().strip() if meta.exists() else '000'
    ok, _ = curl('headers', a.url, a.outdir / 'raw/curl-headers.txt', True)
    if not ok:
        ok, _ = curl('headers-retry', a.url, a.outdir / 'raw/curl-headers.txt', True)
        if ok:
            statuses[-2]['recovered'] = True
    if not ok:
        atomic_json(a.outdir / 'fingerprint-status.json', dict(status='partial', reason='origin_unreachable', commands=statuses))
        return 2
    managed = os.getenv('RECON_MANAGED_PROXY') == '1'
    if not managed and shutil.which('whatweb'):
        failed |= not run('whatweb', ['whatweb', '-a', '1' if stealth else '3', a.url, '--log-brief=' + str(a.outdir / 'raw/whatweb.txt'), '--log-json=' + str(a.outdir / 'raw/whatweb.json')])
    if not managed and not stealth and shutil.which('wafw00f'):
        failed |= not run('wafw00f', ['wafw00f', a.url, '-o', a.outdir / 'raw/wafw00f.txt'])
    else:
        # Existing response headers are observations, without extra WAF payloads.
        (a.outdir / 'raw/wafw00f.txt').write_text((a.outdir / 'raw/curl-headers.txt').read_text(errors='replace') if (a.outdir / 'raw/curl-headers.txt').exists() else 'unavailable')
    if not managed and shutil.which('nuclei'):
        dest = a.outdir / 'raw/nuclei-tech.jsonl'
        ok = run('nuclei-tech', ['nuclei', '-u', a.url, '-tags', 'tech,fingerprint', '-silent', '-jsonl', '-rl', str(rate), '-c', '1' if stealth else '5', '-o', dest])
        failed |= not ok
        findings = []
        for line in lines(dest):
            try:
                r = json.loads(line)
                findings.append(f"[{r.get('template-id', '')}] [{r.get('info', {}).get('severity', '')}] {r.get('matched-at', a.url)}")
            except ValueError: failed = True
        (a.outdir / 'raw/nuclei-tech.txt').write_text('\n'.join(findings) + ('\n' if findings else ''))
    parsed = urlsplit(a.url)
    port = parsed.port or (443 if parsed.scheme == 'https' else 80)
    if not managed and not stealth and os.getenv('RECON_NO_PORTSCAN') != '1' and shutil.which('nmap'):
        failed |= not run('nmap-web', ['nmap', *(['-6'] if ':' in parsed.hostname else []), '-Pn', '-sT', '-T3', '--max-rate', os.getenv('MASSCAN_RATE', '2000'), '--script=http-headers,http-title,http-server-header,ssl-cert', '-p', str(port), parsed.hostname, '-oN', a.outdir / 'raw/nmap-web.txt'])
    if parsed.scheme == 'https' and shutil.which('openssl'):
        host = '[' + parsed.hostname + ']' if ':' in parsed.hostname else parsed.hostname
        cert = a.outdir / 'raw/tls-handshake.txt'
        ok = run('tls', ['openssl', 's_client', '-connect', f'{host}:{port}', '-servername', parsed.hostname], stdin=b'', stdout=cert)
        if ok and cert.exists():
            run('tls-cert', ['openssl', 'x509', '-noout', '-text'], stdin=cert.read_bytes(), stdout=a.outdir / 'raw/tls-cert.txt')
    paths = ['robots.txt', 'sitemap.xml', '.well-known/security.txt', '.well-known/openid-configuration', 'manifest.json']
    if not stealth: paths += ['.git/HEAD', '.svn/entries', 'package.json', 'composer.json']
    observed = []
    for path in paths:
        url = urljoin(origin(a.url) + '/', path)
        dest = a.outdir / ('raw/path-' + path.replace('/', '_') + '.body')
        ok, code = curl('path-' + path.replace('/', '_'), url, dest)
        if not ok: failed = True
        observed.append(f'{code} {url}')
        if code != '200' and dest.exists(): dest.unlink()
    (a.outdir / 'raw/path-probe.txt').write_text('\n'.join(observed) + '\n')
    try:
        import mmh3
    except ImportError:
        statuses.append(dict(name='favicon-hash', status='skipped', reason='optional mmh3 unavailable'))
    else:
        dest = a.outdir / 'raw/favicon.ico'
        ok, code = curl('favicon', origin(a.url) + '/favicon.ico', dest)
        if ok and code == '200' and dest.exists():
            value = mmh3.hash(base64.encodebytes(dest.read_bytes()))
            (a.outdir / 'raw/favicon-hash.txt').write_text(f'favicon_hash={value}\n')
    failed |= any(r['status'] not in ('success', 'skipped') and not r.get('recovered') for r in statuses)
    atomic_json(a.outdir / 'fingerprint-status.json', dict(status='partial' if failed else 'success', commands=statuses))
    return 2 if failed else 0


if __name__ == '__main__':
    raise SystemExit(main())
