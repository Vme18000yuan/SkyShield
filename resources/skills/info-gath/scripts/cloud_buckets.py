#!/usr/bin/env python3
"""Seed-first, rate-limited storage observations with global request/time budgets."""
from __future__ import annotations
import argparse
from concurrent.futures import ThreadPoolExecutor, as_completed
import hashlib
import json
import os
from pathlib import Path
import re
import threading
import time
from urllib.parse import urlsplit
import xml.etree.ElementTree as ET
from recon_common import atomic_json, lines, normalize_target, run_command


def classify(code, body):
    try:
        tree = ET.fromstring(body)
        tag = tree.tag.rsplit('}', 1)[-1]
        if code == 200 and tag in ('ListBucketResult', 'EnumerationResults'):
            return 'PUBLIC_LISTING'
        if code == 403 and any(x.tag.rsplit('}', 1)[-1] == 'Code' and x.text in ('AccessDenied', 'AuthorizationFailure', 'AuthenticationFailed') for x in tree.iter()):
            return 'EXISTS_DENIED'
    except ET.ParseError: pass
    if code == 200: return 'EXISTS_200'
    if code in (301, 302, 307, 308): return 'REDIRECT'
    return None


def candidates(root, out, org=''):
    requests, seen = [], set()
    def add(url, provider, bucket_id, source):
        key = (provider, bucket_id)
        if key not in seen:
            seen.add(key); requests.append(dict(url=url, provider=provider, bucket_id=bucket_id, source=source, ownership='unverified'))
    # Read actual downloaded assets, not masked secret values; no JS from prior runs.
    seeds = set()
    for pattern in ('raw/fp-*/js/*.js', 'raw/fp-*/html/*.html'):
        for path in out.glob(pattern):
            content = path.read_text(errors='replace')[:2097152]
            seeds.update(re.findall(r'https?://[a-zA-Z0-9._-]+\.(?:s3(?:[.-][a-z0-9-]+)?\.amazonaws\.com|oss-[a-z0-9-]+\.aliyuncs\.com|cos\.[a-z0-9-]+\.myqcloud\.com|blob\.core\.windows\.net)', content))
            seeds.update(re.findall(r'https://storage\.googleapis\.com/[a-zA-Z0-9._-]+', content))
    for url in sorted(seeds):
        host = urlsplit(url).hostname
        if 'amazonaws.com' in host: provider, bid, query = 's3', 's3:' + host.split('.s3')[0], '?max-keys=1'
        elif '.aliyuncs.com' in host: provider, bid, query = 'oss', 'oss:' + host.split('.oss-')[0], '?max-keys=1'
        elif '.myqcloud.com' in host: provider, bid, query = 'cos', 'cos:' + host.split('.cos.')[0], '?max-keys=1'
        elif host == 'storage.googleapis.com': provider, bid, query = 'gcp', 'gcp:' + urlsplit(url).path.strip('/'), '?max-keys=1'
        else: provider, bid, query = 'azure', 'azure:' + host.split('.')[0], '?comp=list&maxresults=1'
        add(url.rstrip('/') + '/' + query, provider, bid, 'observed_js_html')
    # Provider-specific valid names; choose vhost S3 only to avoid double counting.
    base = root.split('.')[0]
    names = [base]
    names += [w.lower() for w in re.findall(r'[a-zA-Z0-9]{4,}', org) if w.lower() not in ('limited', 'corporation', 'company', 'privacy', 'redacted')][:2]
    suffixes = ['', '-assets', '-static', '-uploads', '-backup', '-prod', '-dev', '-data']
    for stem in dict.fromkeys(names):
        for suffix in suffixes:
            name = stem + suffix
            if not re.fullmatch(r'[a-z0-9][a-z0-9-]{1,61}[a-z0-9]', name): continue
            add(f'https://{name}.s3.amazonaws.com/?max-keys=1', 's3', 's3:' + name, 'name_candidate')
            add(f'https://storage.googleapis.com/{name}/?max-keys=1', 'gcp', 'gcp:' + name, 'name_candidate')
            for region in ('cn-hangzhou', 'cn-shanghai', 'cn-beijing', 'cn-shenzhen', 'cn-hongkong'):
                add(f'https://{name}.oss-{region}.aliyuncs.com/?max-keys=1', 'oss-' + region, 'oss:' + name, 'name_candidate')
            if os.getenv('COS_APP_ID') and re.fullmatch(r'\d+', os.environ['COS_APP_ID']):
                cos = name + '-' + os.environ['COS_APP_ID']
                for region in ('ap-guangzhou', 'ap-shanghai', 'ap-beijing'):
                    add(f'https://{cos}.cos.{region}.myqcloud.com/?max-keys=1', 'cos-' + region, 'cos:' + cos, 'name_candidate')
            azure = name.replace('-', '')
            if 3 <= len(azure) <= 24:
                add(f'https://{azure}.blob.core.windows.net/?comp=list&maxresults=1', 'azure', 'azure:' + azure, 'name_candidate')
    return requests


def main():
    p = argparse.ArgumentParser(description=__doc__)
    p.add_argument('root'); p.add_argument('outdir', nargs='?', type=Path); p.add_argument('org', nargs='?', default='')
    a = p.parse_args(); t = normalize_target(a.root)
    out = (a.outdir or Path('reports') / t['target_id']).resolve(); (out / 'raw/cloud').mkdir(parents=True, exist_ok=True)
    if os.getenv('RECON_MODE') == 'fast' or os.getenv('RECON_STEALTH') == '1' or t['kind'] != 'HOST':
        atomic_json(out / 'raw/cloud-buckets.json', []); atomic_json(out / 'raw/cloud-coverage.json', dict(status='skipped', reason='mode / target kind')); return 0
    plan = candidates(t['target'], out, a.org)
    max_requests, budget = int(os.getenv('RECON_CLOUD_MAX_REQUESTS', '80')), int(os.getenv('RECON_CLOUD_BUDGET', '60'))
    workers, rate = max(1, min(8, int(os.getenv('RECON_CLOUD_WORKERS', '4')))), max(1, int(os.getenv('RECON_HTTP_RATE', '10')))
    deadline, next_start, attempted, failed = time.monotonic() + budget, 0, 0, 0
    mutex = threading.Lock()
    def probe(item):
        nonlocal next_start, attempted, failed
        with mutex:
            now = time.monotonic()
            start = max(now, next_start)
            if start >= deadline or attempted >= max_requests: return None
            next_start = start + 1 / rate; attempted += 1
        time.sleep(max(0, start - time.monotonic()))
        remain = deadline - time.monotonic()
        if remain <= 0: return None
        ident = hashlib.sha256(item['url'].encode()).hexdigest()[:20]
        body, meta = out / f'raw/cloud/{ident}.body', out / f'raw/cloud/{ident}.meta'
        command = ['curl', '-ks', '--connect-timeout', '3', '--max-time', str(max(1, min(8, int(remain)))), '--max-filesize', '65536', '-o', body, '-w', '%{http_code}', item['url']]
        result = run_command(command, out, 'cloud-' + ident, timeout=remain, stdout=meta)
        text = meta.read_text().strip() if meta.exists() else ''
        code = int(text) if text.isdigit() else 0
        if result['status'] != 'success':
            with mutex: failed += 1
            return None
        verdict = classify(code, body.read_bytes() if body.exists() else b'')
        if not verdict: return None
        return dict(item, verdict=verdict, status_code=code, confidence='medium' if verdict == 'PUBLIC_LISTING' else 'low', state='needs_review', evidence=str(body.relative_to(out)))
    records = []
    with ThreadPoolExecutor(max_workers=workers) as pool:
        # Complete observed seeds before scheduling guesses, even with concurrent workers.
        for source in ('observed_js_html', 'name_candidate'):
            group = [item for item in plan if item['source'] == source][:max(0, max_requests-attempted)]
            futures = [pool.submit(probe, item) for item in group]
            for future in as_completed(futures):
                try:
                    record = future.result()
                    if record: records.append(record)
                except Exception:
                    failed += 1
    # Prefer a listing over a redirect/denial when several regions refer to one bucket.
    unique = {}
    for record in sorted(records, key=lambda r: r['verdict'] != 'PUBLIC_LISTING'):
        unique.setdefault(record['bucket_id'], record)
    records = sorted(unique.values(), key=lambda r: (r['source'] != 'observed_js_html', r['provider'], r['url']))
    atomic_json(out / 'raw/cloud-buckets.json', records)
    (out / 'raw/cloud-buckets.txt').write_text(''.join(f"{r['verdict']} {r['status_code']} {r['provider']} {r['url']}\n" for r in records))
    status = 'partial' if failed else 'success'
    coverage = dict(status=status, candidates=len(plan), attempted=attempted, omitted=max(0, len(plan)-attempted), failed_requests=failed, request_limit=max_requests, budget_seconds=budget, budget_exhausted=time.monotonic() >= deadline, seed_requests=sum(r['source'] == 'observed_js_html' for r in plan))
    atomic_json(out / 'raw/cloud-coverage.json', coverage)
    print(json.dumps(coverage, ensure_ascii=False))
    return 2 if status == 'partial' else 0


if __name__ == '__main__':
    raise SystemExit(main())
