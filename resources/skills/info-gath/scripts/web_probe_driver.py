#!/usr/bin/env python3
"""Collect JS/API/path observations; token comparison is explicitly opt-in."""
from __future__ import annotations
import argparse
import hashlib
from html.parser import HTMLParser
import json
import os
from pathlib import Path
import re
import secrets
import shutil
import sys
import time
from urllib.parse import urljoin, urlsplit
from recon_common import atomic_json, in_scope, lines, normalize_url, origin, run_command, write_lines
from web_analysis import HttpClient, classify, signature, structured


class Scripts(HTMLParser):
    def __init__(self): super().__init__(); self.urls = []
    def handle_starttag(self, tag, attrs):
        if tag.lower() == 'script':
            value = dict(attrs).get('src')
            if value: self.urls.append(value)


def main():
    p = argparse.ArgumentParser(description=__doc__)
    p.add_argument('url'); p.add_argument('outdir', nargs='?', type=Path, default=Path('web-probe-out'))
    p.add_argument('katana_file', nargs='?', type=Path)
    a = p.parse_args(); a.url = normalize_url(a.url); out = a.outdir.resolve()
    for name in ('js', 'html', 'raw'): (out / name).mkdir(parents=True, exist_ok=True)
    mode, stealth = os.getenv('RECON_MODE', 'normal'), os.getenv('RECON_STEALTH') == '1'
    managed = os.getenv('RECON_MANAGED_PROXY') == '1'
    proxy = os.getenv('RECON_HTTP_PROXY', '')
    if mode == 'fast':
        atomic_json(out / 'web-probe-summary.json', dict(status='skipped', reason='fast')); return 0
    validate = os.getenv('RECON_VALIDATE') == '1' and not stealth
    scope = os.getenv('RECON_SCOPE_HOST', urlsplit(a.url).hostname)
    rate = min(int(os.getenv('RECON_HTTP_RATE', '10')), 2 if stealth else 10000)
    deadline = time.monotonic() + int(os.getenv('RECON_STAGE_TIMEOUT', '300'))
    client = HttpClient(out, scope, rate)
    client.deadline = deadline - 3
    failed_tools = []
    base = origin(a.url)
    def run(name, argv, *, time_limit=None, **kwargs):
        if deadline - time.monotonic() <= 1:
            failed_tools.append(name + ': budget'); return False
        remaining = deadline - time.monotonic() - 1
        timeout = min(remaining, time_limit) if time_limit is not None else remaining
        ok = run_command(argv, out, name, timeout=timeout, **kwargs)['status'] == 'success'
        if not ok: failed_tools.append(name)
        return ok
    def allowed(url):
        try: return in_scope(urlsplit(normalize_url(url)).hostname, scope)
        except (ValueError, UnicodeError): return False
    home = client.fetch(a.url)
    recovered_requests = 0
    if not home.code and not client.exhausted:
        failed_before_retry = client.failed
        home = client.fetch(a.url)
        if home.code and failed_before_retry:
            recovered_requests = 1
    if not home.code:
        summary = dict(status='partial', reason='origin_unreachable', requests=client.attempted,
            failed_requests=client.failed, budget_exhausted=client.exhausted, failed_tools=failed_tools)
        atomic_json(out / 'web-probe-summary.json', summary)
        print(json.dumps(summary, ensure_ascii=False))
        return 2
    (out / 'html/index.html').write_bytes(home.body)
    parser = Scripts(); parser.feed(home.body.decode('utf-8', 'replace'))
    js_urls = {urljoin(a.url, ref) for ref in parser.urls}
    crawled = lines(a.katana_file) if a.katana_file else []
    if not crawled and not stealth and shutil.which('katana'):
        path = out / 'katana-local.txt'
        if ':' in scope:
            pattern = r'https?://\[' + re.escape(scope) + r'\](?::\d+)?(?:/|$)'
        else:
            pattern = r'https?://(?:[a-zA-Z0-9-]+\.)*' + re.escape(scope) + r'(?::\d+)?(?:/|$)'
        crawl_budget = min(25, max(1, (deadline - time.monotonic() - 2) * .3))
        run('katana', ['katana', '-u', a.url, '-d', '3' if mode == 'deep' else '2', '-jc', '-silent', '-cs', pattern, '-rl', str(rate), '-c', '2', *(['-proxy', proxy] if proxy else []), '-o', path], time_limit=crawl_budget)
        crawled = lines(path)
    crawled = [u for u in crawled if allowed(u)]
    js_urls.update(u for u in crawled if re.search(r'\.js(?:\?|$)', u, re.I))
    for tool, argv in [('subjs', ['subjs']), ('getJS', ['getJS', '--url', a.url, '--complete'])]:
        if not managed and not stealth and shutil.which(tool):
            dest = out / ('raw/' + tool + '.txt')
            run(tool, argv, stdin=a.url + '\n', stdout=dest)
            js_urls.update(u for u in lines(dest) if re.search(r'\.js(?:\?|$)', u, re.I))
    write_lines(out / 'external-js-urls.txt', [u for u in js_urls if not allowed(u)])
    js_urls = sorted(u for u in js_urls if allowed(u))
    write_lines(out / 'js-urls.txt', js_urls)
    index, maps, endpoints = [], 0, set()
    for url in js_urls[:int(os.getenv('RECON_MAX_JS', '50'))]:
        response = client.fetch(url)
        if response.code != 200 or not response.body or 'html' in response.ctype or response.body.lstrip().lower().startswith((b'<!doctype html', b'<html')): continue
        name = hashlib.sha256(url.encode()).hexdigest() + '.js'
        path = out / 'js' / name; path.write_bytes(response.body)
        index.append(dict(url=url, file=name))
        source = response.body.decode('utf-8', 'replace')
        if shutil.which('js-beautify'):
            run('beautify-' + name[:12], ['js-beautify', '--quiet', path], stdout=out / 'js' / (name + '.pretty'))
        for ref in re.findall(r'''["'`]((?:https?://[^\s"'`]+)?/(?:api|v\d+|rest|graphql|gql|internal|auth|user|account|order|product|file|upload|search|manage|admin|sys|open)(?:[/?:][^\s"'`]{0,180})?)["'`]''', source, re.I):
            endpoint = urljoin(base + '/', ref)
            if allowed(endpoint): endpoints.add(endpoint)
        match = re.search(r'sourceMappingURL=([^\s*]+)', source[-4096:])
        if match:
            map_url = urljoin(url, match[1])
            if allowed(map_url):
                result = client.fetch(map_url)
                try:
                    data = json.loads(result.body)
                    if result.code == 200 and 'sources' in data:
                        path.with_suffix('.js.map').write_bytes(result.body); maps += 1
                except (ValueError, TypeError): pass
        if shutil.which('linkfinder.py'):
            dest = out / ('raw/linkfinder-' + name + '.txt')
            run('linkfinder-' + name[:12], [sys.executable, shutil.which('linkfinder.py'), '-i', path, '-o', 'cli'], stdout=dest)
            for ref in lines(dest):
                url_ref = urljoin(base + '/', ref)
                if allowed(url_ref): endpoints.add(url_ref)
    atomic_json(out / 'js-index.json', index)
    (out / 'js-index.txt').write_text(''.join(f"{r['url']} -> {r['file']}\n" for r in index))
    write_lines(out / 'js-endpoints.txt', endpoints)
    baseline = [client.fetch(base + '/_recon_' + secrets.token_hex(12)), client.fetch(base + '/_recon_' + secrets.token_hex(12) + '.json')]
    docs_found = []
    docs = ['/openapi.json', '/swagger.json', '/v3/api-docs', '/v2/api-docs', '/api-docs', '/api/schema.json', '/graphql', '/api/graphql']
    if mode == 'deep' and not stealth: docs += ['/openapi.yaml', '/swagger-ui/', '/api/swagger.json', '/api/v1/swagger.json', '/docs', '/api/docs', '/gql', '/query', '/__graphql']
    if stealth: docs = docs[:3]
    for path in docs:
        url = base + path
        response = client.fetch(url)
        try:
            data = json.loads(response.body)
            if response.code == 200 and isinstance(data, dict) and ('swagger' in data or 'openapi' in data) and isinstance(data.get('paths'), dict):
                docs_found.append('200 ' + url)
                for path, methods in data['paths'].items():
                    endpoint = urljoin(base + '/', path)
                    if isinstance(methods, dict) and 'get' in methods and allowed(endpoint): endpoints.add(endpoint)
            if validate and ('graphql' in path or path in ('/gql', '/query')) and isinstance(data, dict) and (data.get('errors') or data.get('data')):
                introspection = client.fetch(url, payload={'query': '{__schema{queryType{name}types{name kind}}}'})
                result = json.loads(introspection.body)
                if isinstance(result.get('data', {}).get('__schema'), dict): atomic_json(out / 'graphql-introspection.json', result)
        except (ValueError, TypeError, AttributeError): pass
    write_lines(out / 'api-docs-found.txt', docs_found)
    for url in crawled:
        if re.match(r'^/(?:api|v\d+|rest|graphql|gql|auth|admin|user)(?:/|$)', urlsplit(url).path, re.I): endpoints.add(url)
    write_lines(out / 'api-paths.txt', endpoints)
    findings, tested, attempted = [], 0, 0
    for endpoint in sorted(endpoints)[:int(os.getenv('RECON_MAX_API', '100'))]:
        if not allowed(endpoint): continue
        before = client.attempted
        noauth = client.fetch(endpoint)
        if client.attempted == before and client.exhausted: break
        attempted += 1
        fake = client.fetch(endpoint, ['Authorization: Bearer eyJhbGciOiJub25lIiwidHlwIjoiSldUIn0.e30.']) if validate else None
        random = client.fetch(endpoint, ['Authorization: Bearer ' + secrets.token_urlsafe(20)]) if validate else None
        tested += noauth.code > 0
        verdict = classify(noauth, baseline, fake, random)
        if verdict:
            findings.append(dict(verdict, url=endpoint, code_noauth=noauth.code,
                code_fakejwt=fake.code if fake else None, code_randbearer=random.code if random else None,
                content_type=noauth.ctype, resp_len=len(noauth.body), response_sha256=signature(noauth),
                evidence=noauth.evidence, token_evidence=fake.evidence if fake else None))
    unauth = dict(total_tested=tested, total_attempted=attempted, failed=attempted-tested, token_comparison=validate, high=0,
        medium=sum(r['confidence'] == 'medium' for r in findings), low=sum(r['confidence'] == 'low' for r in findings), findings=findings)
    atomic_json(out / 'unauth-check.json', unauth)
    (out / 'unauth-check.txt').write_text(''.join(f"{r['verdict']} [{r['confidence']}] {r['url']} # {r['note']}\n" for r in findings))
    # Download HTML before scanning its contents; no config excerpts containing raw secrets.
    for path in ([] if stealth else ['/login', '/index.html']):
        response = client.fetch(base + path)
        if response.code == 200: (out / 'html' / (path.strip('/') + '.html')).write_bytes(response.body)
    run('web-secrets', [sys.executable, Path(__file__).with_name('web_secrets.py'), out / 'js', out / 'secrets.json', out])
    extra = []
    paths = [] if stealth else ['.env', 'config.json', 'application.yml', '.git/HEAD', 'actuator/health', 'actuator/info', 'swagger-ui/', 'server-status', 'metrics']
    if mode == 'deep' and not stealth: paths += ['.env.production', 'application.properties', 'web.config.bak', '.vscode/settings.json', 'actuator/env', 'actuator/mappings', 'druid/index.html', 'manager/html', 'api/v1/config', 'api/v1/users']
    for path in paths:
        response = client.fetch(base + '/' + path)
        if response.code in (200, 401, 403) and not any(b.code == 200 and signature(b) == signature(response) for b in baseline):
            extra.append(f'{response.code} {base}/{path}')
    write_lines(out / 'extra-path-probe.txt', extra)
    directories = []
    if mode == 'deep' and not stealth:
        dictionary = Path(os.getenv('RECON_WEB_WORDLIST', '/usr/share/seclists/Discovery/Web-Content/common.txt'))
        if dictionary.is_file() and shutil.which('ffuf'):
            dest = out / 'dirscan-ffuf.json'
            run('ffuf', ['ffuf', '-u', base + '/FUZZ', '-w', dictionary, '-mc', '200,204,301,302,401,403', '-ac', '-t', '4', '-rate', str(rate), '-timeout', '6', '-maxtime', str(max(1, int(deadline - time.monotonic() - 2))), *(['-x', proxy] if proxy else []), '-s', '-of', 'json', '-o', dest])
            try:
                for r in json.loads(dest.read_text()).get('results', []):
                    if allowed(r['url']): directories.append(f"{r['status']} {r['url']} len={r['length']}")
            except (OSError, ValueError, KeyError): pass
        else:
            words = lines(dictionary)[:100] if dictionary.is_file() else ['admin', 'api', 'login', 'dashboard', 'config', 'backup', 'uploads', 'test', 'dev', 'debug', 'api/v1', 'actuator', 'swagger', 'health', 'status', 'metrics']
            for word in words:
                if not word or word.startswith('#'): continue
                url = base + '/' + word.lstrip('/')
                response = client.fetch(url)
                if response.code in (200, 301, 302, 401, 403) and not any(b.code == 200 and signature(b) == signature(response) for b in baseline): directories.append(f'{response.code} {url} len={len(response.body)}')
    write_lines(out / 'dirscan.txt', directories + ['CRAWLED ' + u for u in crawled])
    if validate and not managed and shutil.which('nuclei'):
        run('nuclei-api', ['nuclei', '-u', a.url, '-tags', 'api,unauth,panel', '-silent', '-rl', str(rate), '-c', '2', '-o', out / 'nuclei-api.txt'])
    summary = dict(status='partial' if client.exhausted or failed_tools or client.failed > recovered_requests else 'success', host=urlsplit(a.url).hostname,
        js_urls=len(js_urls), js_downloaded=len(index), js_omitted=max(0, len(js_urls)-len(index)), source_maps=maps,
        api_paths=len(endpoints), api_tested=tested, api_attempted=attempted, api_failed=attempted-tested, api_omitted=max(0, len(endpoints)-attempted),
        unauth_high=0, unauth_medium=unauth['medium'], token_comparison=validate,
        requests=client.attempted, failed_requests=client.failed, recovered_requests=recovered_requests, budget_exhausted=client.exhausted, failed_tools=failed_tools)
    atomic_json(out / 'web-probe-summary.json', summary)
    print(json.dumps(summary, ensure_ascii=False))
    return 2 if summary['status'] == 'partial' else 0


if __name__ == '__main__':
    raise SystemExit(main())
