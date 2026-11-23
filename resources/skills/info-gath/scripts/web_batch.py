#!/usr/bin/env python3
"""Analyze selected Web origins with bounded, shared-rate parallelism."""
from __future__ import annotations

import argparse
from concurrent.futures import ThreadPoolExecutor, as_completed
import hashlib
import json
import math
import os
from pathlib import Path
import threading
import time

from recon_common import atomic_json, load_json, normalize_url, run_command, service_id

SCRIPTS = Path(__file__).resolve().parent


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('entries', type=Path)
    parser.add_argument('outdir', type=Path)
    args = parser.parse_args()
    out = args.outdir.resolve()
    (out / 'raw').mkdir(parents=True, exist_ok=True)
    entries = json.loads(args.entries.read_text())
    if not isinstance(entries, list) or any(not isinstance(url, str) for url in entries):
        parser.error('Web 入口列表无效')
    entries = list(dict.fromkeys(normalize_url(url) for url in entries))
    rate = max(1, int(os.getenv('RECON_HTTP_RATE', '10')))
    stealth = os.getenv('RECON_STEALTH') == '1'
    workers = min(1 if stealth else 3, rate, max(1, len(entries)))
    worker_rate = max(1, rate // workers)
    stage_seconds = max(1, int(os.getenv('RECON_STAGE_TIMEOUT', '300')))
    deadline = time.monotonic() + max(.5, stage_seconds - 4)
    waves = max(1, math.ceil(len(entries) / workers))
    entry_budget = max(.5, (stage_seconds - 6) / waves)
    items = [dict(id=service_id(url) + '-' + hashlib.sha256(url.encode()).hexdigest()[:10],
        status='pending', fingerprint='pending', web='pending') for url in entries]
    lock = threading.Lock()
    status_path = out / 'raw/web-batch-status.json'

    def checkpoint():
        pending = sum(item['status'] in ('pending', 'running') for item in items)
        completed = sum(item['status'] == 'success' for item in items)
        summary = dict(status='running' if pending else 'success' if completed == len(items) else 'partial',
            selected=len(items), attempted=sum(item['status'] != 'pending' for item in items),
            completed=completed, partial=sum(item['status'] == 'partial' for item in items),
            failed=sum(item['status'] == 'failed' for item in items),
            timed_out=sum(item['status'] == 'timeout' for item in items),
            pending=pending, workers=workers, per_worker_rate=worker_rate, results=items)
        for key in ('crawler_timed_out', 'crawler_failed', 'failed_requests'):
            summary[key] = sum(item.get(key, 0) for item in items)
        atomic_json(status_path, summary)
        return summary

    def update(index, **values):
        with lock:
            items[index].update(values)
            return checkpoint()

    checkpoint()

    def execute(index, url):
        ident = items[index]['id']
        dest = out / 'raw' / ('fp-' + ident)
        update(index, status='running')
        entry_deadline = min(deadline, time.monotonic() + entry_budget)

        def run(name, budget):
            if budget <= .25:
                return 'timeout'
            env = dict(os.environ, RECON_HTTP_RATE=str(worker_rate),
                RECON_STAGE_TIMEOUT=str(max(1, int(budget - 1))))
            result = run_command(['bash', SCRIPTS / (name + '.sh'), url, dest], out,
                name + '-' + ident, timeout=budget, env=env)
            if result['status'] == 'success': return 'success'
            if result['status'] == 'timeout': return 'timeout'
            return 'partial' if result.get('returncode') == 2 else 'failed'

        try:
            remaining = entry_deadline - time.monotonic() - 1
            fingerprint_budget = min(60, max(.25, entry_budget * .45), remaining)
            fingerprint = run('fingerprint', fingerprint_budget)
            update(index, fingerprint=fingerprint)
            web = run('web_probe', entry_deadline - time.monotonic() - 1)
            details = load_json(dest / 'web-probe-summary.json', {})
            if not isinstance(details, dict): details = {}
            crawl_timeout = 'katana' in details.get('timed_out_tools', [])
            failed_requests = details.get('failed_requests', 0)
            if type(failed_requests) is not int or failed_requests < 0: failed_requests = 0
            statuses = (fingerprint, web)
            result = 'success' if all(value == 'success' for value in statuses) else 'timeout' if 'timeout' in statuses else 'failed' if 'failed' in statuses else 'partial'
            summary = update(index, web=web, status=result, crawler_timed_out=int(crawl_timeout),
                crawler_failed=int('katana' in details.get('failed_tools', []) and not crawl_timeout), failed_requests=failed_requests)
            print(f"Web 分析：已处理 {summary['selected'] - summary['pending']}/{summary['selected']} 个入口。", flush=True)
        except Exception as exc:
            update(index, status='failed', error=type(exc).__name__)

    with ThreadPoolExecutor(max_workers=workers) as pool:
        futures = [pool.submit(execute, index, url) for index, url in enumerate(entries)]
        for future in as_completed(futures):
            future.result()
    with lock:
        summary = checkpoint()
    return 0 if summary['status'] == 'success' else 2


if __name__ == '__main__':
    raise SystemExit(main())
