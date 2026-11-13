#!/usr/bin/env python3
"""Convert recon evidence into a conservative, portable SkyShield import envelope.

Scope values are hints, not authorization. A consumer must check the requested
target and every record against its own target registry before any write.
"""
from __future__ import annotations

import hashlib
import ipaddress
import json
import re
from pathlib import PurePosixPath
from urllib.parse import urlsplit, urlunsplit

from recon_common import normalize_host, normalize_target, normalize_url


def clean_host(value):
    try:
        if not isinstance(value, str) or len(value) > 253:
            return None
        return normalize_host(value)
    except (ValueError, UnicodeError):
        return None


def clean_ip(value):
    try:
        return str(ipaddress.ip_address(value))
    except (ValueError, TypeError):
        return None


def clean_url(value):
    """Do not copy query strings, fragments, userinfo, or control characters."""
    try:
        if not isinstance(value, str) or len(value) > 4096 or re.search(r'[\x00-\x20\x7f]', value):
            return None
        item = urlsplit(normalize_url(value))
        return urlunsplit((item.scheme, item.netloc, item.path, '', ''))
    except (ValueError, UnicodeError):
        return None


def evidence_path(value):
    if not isinstance(value, str) or not value or '\\' in value or len(value) > 512:
        return None
    path = PurePosixPath(value)
    if path.is_absolute() or any(part in ('.', '..') for part in path.parts):
        return None
    return str(path)


def clean_title(value):
    if not isinstance(value, str):
        return ''
    text = re.sub(r'[\x00-\x1f\x7f]+', ' ', value).strip()[:512]
    return re.sub(r'(?i)\b(token|password|secret|api[_-]?key)\s*[:=]\s*[^\s,;]+', r'\1=[REDACTED]', text)


def clean_technologies(value):
    if isinstance(value, str):
        value = [value]
    if not isinstance(value, list):
        return []
    return sorted({s.strip() for s in value if isinstance(s, str) and re.fullmatch(r'[A-Za-z0-9][A-Za-z0-9 +._:/-]{0,119}', s.strip())})[:100]


def scope_for(host, target):
    try:
        network = ipaddress.ip_network(target, strict=False)
        return 'in_scope' if ipaddress.ip_address(host) in network else 'out_of_scope'
    except ValueError:
        host = clean_host(host)
        if not host:
            return 'unverified'
        return 'in_scope' if host == target or host.endswith('.' + target) else 'out_of_scope'


def stable_id(kind, *parts):
    raw = json.dumps([kind, *parts], ensure_ascii=True, separators=(',', ':'))
    return hashlib.sha256(raw.encode()).hexdigest()


def skyshield_output(data):
    target = normalize_target(str(data['target']))
    root, kind = target['target'], target['kind']
    assets, observations, vulnerabilities = {}, {}, {}
    dns_by_ip = {}
    cdn_ips = set()
    for record in data.get('cdn_excluded', []):
        if isinstance(record, dict):
            address = clean_ip(record.get('ip'))
            if address:
                cdn_ips.add(address)
    for record in data.get('dns_records', []):
        if not isinstance(record, dict):
            continue
        address, host = clean_ip(record.get('ip')), clean_host(record.get('host'))
        typ = record.get('type')
        if address and host and typ in ('A', 'AAAA') and ipaddress.ip_address(address).version == (4 if typ == 'A' else 6):
            dns_by_ip.setdefault(address, set()).add(host)

    def add_asset(asset_type, value, scope, evidence, **fields):
        ident = stable_id(asset_type, value, fields.get('protocol', ''), fields.get('port', 0))
        path = evidence_path(evidence)
        entry = dict(id=ident, type=asset_type, value=value, scope=scope,
                     evidence=[path] if path else [], **fields)
        if ident in assets:
            previous = assets[ident]
            previous['evidence'] = sorted(set(previous['evidence'] + entry['evidence']))
            if 'bound_domains' in entry:
                previous['bound_domains'] = sorted(set(previous.get('bound_domains', []) + entry['bound_domains']))
        else:
            assets[ident] = entry

    def add_observation(category, value, scope, evidence):
        ident = stable_id(category, value)
        path = evidence_path(evidence)
        observations[ident] = dict(id=ident, category=category, value=value, scope=scope,
                                   status='needs_review', evidence=[path] if path else [])

    for raw in data.get('subdomains', []):
        host = clean_host(raw)
        if host and not clean_ip(host):
            add_asset('domain', host, scope_for(host, root), '01-subdomains.txt')
    org = data.get('org_assets') if isinstance(data.get('org_assets'), dict) else {}
    for raw in org.get('related_roots', []):
        host = clean_host(raw)
        if host and not clean_ip(host):
            add_asset('domain', host, scope_for(host, root), 'raw/org-assets.json')

    for address in sorted({ip for item in data.get('ips', []) if (ip := clean_ip(item))} | set(dns_by_ip)):
        domains = sorted(dns_by_ip.get(address, []))
        scope = scope_for(address, root)
        if kind == 'HOST' and scope != 'in_scope':
            scope = 'associated' if any(scope_for(domain, root) == 'in_scope' for domain in domains) else 'unverified'
        if address in cdn_ips and kind == 'HOST':
            scope = 'unverified'
        add_asset('ip', address, scope, 'dns-records.json' if domains else '04-ips.txt',
                  bound_domains=domains, cdn=address in cdn_ips)

    for record in data.get('http', []):
        if not isinstance(record, dict):
            continue
        url = clean_url(record.get('url'))
        if not url:
            continue
        host = urlsplit(url).hostname
        code = record.get('status_code')
        fields = {'status_code': code} if isinstance(code, int) and not isinstance(code, bool) and 100 <= code <= 599 else {}
        title = clean_title(record.get('title'))
        technologies = clean_technologies(record.get('tech', record.get('technologies', [])))
        if title: fields['page_title'] = title
        if technologies: fields['technologies'] = technologies
        add_asset('service', url, scope_for(host, root), 'http-records.json', **fields)
        if urlsplit(url).path not in ('', '/'):
            add_asset('endpoint', url, scope_for(host, root), 'http-records.json')

    for record in data.get('discovered_urls', []):
        if not isinstance(record, dict):
            continue
        url = clean_url(record.get('url'))
        if url:
            add_asset('endpoint', url, scope_for(urlsplit(url).hostname, root), record.get('evidence', ''), confidence='candidate')

    for record in data.get('services', []):
        if not isinstance(record, dict):
            continue
        address, port, protocol = clean_ip(record.get('ip')), record.get('port'), record.get('protocol')
        if not address or not isinstance(port, int) or isinstance(port, bool) or not 1 <= port <= 65535 or protocol not in ('tcp', 'udp'):
            continue
        service = record.get('service', '')
        if not isinstance(service, str) or not re.fullmatch(r'[A-Za-z0-9_.+/-]{0,64}', service):
            service = ''
        domains = sorted(dns_by_ip.get(address, []))
        scope = scope_for(address, root)
        if kind == 'HOST':
            scope = 'associated' if any(scope_for(domain, root) == 'in_scope' for domain in domains) else 'unverified'
            if address in cdn_ips:
                scope = 'unverified'
        add_asset('service', address, scope, record.get('evidence', ''), protocol=protocol,
                  port=port, service_name=service, bound_domains=domains, cdn=address in cdn_ips)

    for finding in data.get('unauth_findings', []):
        if not isinstance(finding, dict) or finding.get('verdict') not in ('ANONYMOUS_DATA', 'REVIEW_TOKEN_DIFFERENCE'):
            continue
        url = clean_url(finding.get('url'))
        if url:
            scope = scope_for(urlsplit(url).hostname, root)
            category = finding['verdict']
            add_observation(category, url, scope, 'recon.json')
            titles = {'ANONYMOUS_DATA': '匿名数据响应', 'REVIEW_TOKEN_DIFFERENCE': '令牌响应差异'}
            descriptions = {'ANONYMOUS_DATA': '匿名请求返回结构化数据，须核实接口是否应受保护。',
                            'REVIEW_TOKEN_DIFFERENCE': '无效令牌返回的响应存在差异，缺少授权基线，须人工复核。'}
            ident = stable_id(category, url)
            vulnerabilities[ident] = dict(id=ident, type=category, url=url, title=titles[category],
                severity='info', confidence='candidate', status='needs_review', scope=scope,
                description=descriptions[category], evidence=list(dict.fromkeys(['recon.json'] + [p for key in ('evidence', 'token_evidence') if (p := evidence_path(finding.get(key)))])))
    for bucket in data.get('cloud_buckets', []):
        if not isinstance(bucket, dict) or bucket.get('verdict') != 'PUBLIC_LISTING':
            continue
        url = clean_url(bucket.get('url'))
        if url:
            add_observation('PUBLIC_LISTING', url, 'unverified', 'raw/cloud-buckets.json')
    axfr = data.get('axfr')
    if kind == 'HOST' and isinstance(axfr, dict) and axfr.get('status') == 'success':
        add_observation('DNS_ZONE_TRANSFER', root, 'in_scope', 'axfr.json')

    status = data.get('status') if data.get('status') in ('completed', 'partial', 'failed', 'interrupted', 'unknown') else 'unknown'
    return dict(schema_version='1.0', run=dict(id=str(data.get('run_id') or ''), target=root,
                kind=kind, status=status, mode=data.get('mode', 'unknown')),
                assets=sorted(assets.values(), key=lambda item: item['id']),
                vulnerabilities=sorted(vulnerabilities.values(), key=lambda item: item['id']),
                observations=sorted(observations.values(), key=lambda item: item['id']))
