"""Bounded retries for read-only third-party collection sources."""
from __future__ import annotations

from pathlib import Path
import re
import time

from recon_common import run_command


def fetch_source(url, outdir, name, *, stdout, deadline, max_bytes, redirects=False, request_timeout=30):
    # Keep the inherited proxy policy and leave time for the rest of the phase.
    deadline = min(deadline, time.monotonic() + 60)
    record = dict(name=name, status='timeout', returncode=124, attempts=0)
    for attempt in range(1, 4):
        remaining = deadline - time.monotonic()
        if remaining <= 0:
            break
        request_seconds = min(request_timeout, remaining)
        argv = ['curl', '-fsS', '--connect-timeout', '5', '--max-time', f'{request_seconds:.3f}',
            '--max-filesize', str(max_bytes)]
        if redirects:
            argv.append('-L')
        record = run_command([*argv, url], outdir, name,
            timeout=min(remaining, request_seconds + 1), stdout=stdout)
        record['attempts'] = attempt
        if record.get('returncode') == 22:
            stderr = Path(outdir) / 'raw' / (name + '.stderr')
            if stderr.is_file():
                with stderr.open(errors='replace') as stream:
                    codes = re.findall(r'\berror:\s*(\d{3})\b', stream.read(4096))
                if codes:
                    record['http_status'] = int(codes[-1])
        transient = record.get('returncode') in (5, 6, 7, 18, 28, 52, 56, 124) or record.get('http_status') in (408, 429, 500, 502, 503, 504)
        if record['status'] == 'success' or not transient or attempt == 3:
            break
        delay = 2 ** (attempt - 1)
        if time.monotonic() + delay >= deadline:
            break
        time.sleep(delay)
    return record
