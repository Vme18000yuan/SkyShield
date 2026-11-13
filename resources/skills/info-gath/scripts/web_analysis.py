#!/usr/bin/env python3
"""Pure response classification and bounded HTTP evidence collection."""
from __future__ import annotations
from dataclasses import dataclass
import hashlib
import json
import re
import time
from pathlib import Path
from urllib.parse import urlsplit
from recon_common import in_scope, normalize_url, run_command


@dataclass
class Response:
    code: int = 0
    ctype: str = ''
    body: bytes = b''
    evidence: str = ''


def signature(response):
    try:
        value = json.loads(response.body)
        normalized = json.dumps(value, sort_keys=True, ensure_ascii=False).encode()
    except (ValueError, UnicodeError):
        normalized = re.sub(rb'\s+', b' ', response.body).strip()
    return hashlib.sha256(normalized).hexdigest()


def structured(response):
    if not response.body.strip(): return False
    try:
        json.loads(response.body); return True
    except (ValueError, UnicodeError):
        return 'xml' in response.ctype and response.body.lstrip().startswith(b'<')


def classify(noauth, baselines, fake=None, random=None):
    """A status or a JSON body alone never proves missing authorization."""
    baseline = any(b.code == 200 and b.body and signature(b) == signature(noauth) and structured(b) == structured(noauth) for b in baselines)
    if noauth.code == 200:
        if baseline:
            return dict(verdict='SPA_FALLBACK', confidence='low', severity='info', state='observation', note='与随机路径响应内容一致')
        if structured(noauth):
            return dict(verdict='ANONYMOUS_DATA', confidence='medium', severity='info', state='needs_review', note='匿名可访问结构化数据；须确认该接口是否应受保护')
        return dict(verdict='REVIEW_200', confidence='low', severity='info', state='observation', note='HTTP 200，不能据此确认资源或权限问题')
    if noauth.code in (401, 403) and fake and fake.code == 200:
        fake_baseline = any(b.code == 200 and b.body and signature(b) == signature(fake) for b in baselines)
        if structured(fake) and not fake_baseline:
            return dict(verdict='REVIEW_TOKEN_DIFFERENCE', confidence='medium', severity='info', state='needs_review', note='无效 token 的结构化响应有差异；缺少授权正向基线，不能确认绕过')
    return None


class HttpClient:
    def __init__(self, outdir, scope, rate=10, seconds=180):
        self.out = Path(outdir).resolve()
        (self.out / 'raw/http').mkdir(parents=True, exist_ok=True)
        self.scope, self.interval, self.next_request = scope, 1 / max(1, rate), 0
        self.deadline = time.monotonic() + seconds
        self.attempted = self.failed = 0
        self.exhausted = False

    def fetch(self, url, headers=None, payload=None):
        try:
            url = normalize_url(url)
            if not in_scope(urlsplit(url).hostname, self.scope): return Response()
        except (ValueError, UnicodeError): return Response()
        delay = max(0, self.next_request - time.monotonic())
        if time.monotonic() + delay >= self.deadline:
            self.exhausted = True; return Response()
        time.sleep(delay)
        self.next_request = time.monotonic() + self.interval
        ident = hashlib.sha256((url + repr(headers) + repr(payload)).encode()).hexdigest()[:24]
        body, meta = self.out / f'raw/http/{ident}.body', self.out / f'raw/http/{ident}.meta'
        response_headers = body.with_suffix('.headers')
        for path in (body, meta, response_headers, body.with_suffix('.request')):
            path.unlink(missing_ok=True)
        argv = ['curl', '-ksv', '--dump-header', response_headers, '--connect-timeout', '3', '--max-time', '8', '--max-filesize', '2097152', '-o', body, '-w', '%{http_code}|%{content_type}', '-A', 'Mozilla/5.0']
        for header in headers or []: argv += ['-H', header]
        sent_body = json.dumps(payload) if payload is not None else ''
        if payload is not None: argv += ['-H', 'Content-Type: application/json', '--data', sent_body]
        argv.append(url)
        self.attempted += 1
        r = run_command(argv, self.out, 'http-' + ident, timeout=min(10, self.deadline - time.monotonic()), stdout=meta)
        # curl's outgoing header trace records the real request. Exclude proxy
        # CONNECT exchanges and never synthesize headers when no request was sent.
        trace = self.out / f'raw/http-{ident}.stderr'
        blocks, current = [], []
        for line in trace.read_text(errors='replace').splitlines() if trace.exists() else []:
            if line in ('>', '> '):
                if current: blocks.append(current); current = []
            elif line.startswith('> '): current.append(line[2:])
        if current: blocks.append(current)
        sent = [block for block in blocks if block and not block[0].startswith('CONNECT ')]
        if sent:
            body.with_suffix('.request').write_text('\r\n'.join(sent[-1]) + '\r\n\r\n' + sent_body)
        if r['status'] != 'success':
            self.failed += 1; return Response(evidence=str(body.relative_to(self.out)))
        status, _, ctype = meta.read_text(errors='replace').partition('|')
        return Response(int(status) if status.isdigit() else 0, ctype.strip(), body.read_bytes()[:2097152] if body.exists() else b'', str(body.relative_to(self.out)))
