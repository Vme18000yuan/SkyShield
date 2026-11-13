#!/usr/bin/env python3
"""One evidence model for JSON, Chinese/English and batch reports."""
from __future__ import annotations
import argparse
import ipaddress
import json
import re
from pathlib import Path
from collections import Counter
from recon_common import atomic_json, lines, load_json, normalize_target, parse_grepable, parse_nmap_xml, utc_now


def cell(value):
    return str(value).replace('|', '\\|').replace('\n', ' ').replace('\r', '')


def report_directory(path):
    path = Path(path)
    if (path / 'latest').is_symlink():
        if not (path / 'latest').is_dir():
            raise ValueError('latest 指针损坏；请指定一个明确的 run 目录')
        return (path / 'latest').resolve()
    return path.resolve()


def collect_ports(out, errors):
    found = {}
    def add(record, evidence):
        try:
            address = str(ipaddress.ip_address(record['ip']))
            port = int(record['port'])
            protocol = record.get('protocol', 'tcp')
            if not 1 <= port <= 65535 or protocol not in ('tcp', 'udp') or record.get('state', 'open') != 'open':
                return
            key = address, protocol, port
            entry = dict(ip=address, port=port, protocol=protocol, state='open', service=record.get('service', ''), version=record.get('version', record.get('banner', '')), evidence=str(evidence.relative_to(out)))
            if key not in found or len(entry['version']) + len(entry['service']) > len(found[key]['version']) + len(found[key]['service']):
                found[key] = entry
        except (ValueError, KeyError, TypeError) as exc:
            errors.append(f'{evidence.name}: invalid port record: {exc}')
    for path in out.glob('raw/*.xml'):
        if not path.name.startswith(('nmap-', 'rustscan-')):
            continue
        try:
            for record in parse_nmap_xml(path): add(record, path)
        except Exception as exc: errors.append(f'{path.name}: {exc}')
    for path in out.glob('raw/*.nmap'):
        if path.with_suffix('.xml').exists(): continue
        host = None
        for line in lines(path):
            if line.startswith('Nmap scan report for '):
                candidate = line.rsplit(' ', 1)[-1].strip('()')
                try: host = str(ipaddress.ip_address(candidate))
                except ValueError: host = None
            match = re.match(r'^(\d+)/(tcp|udp)\s+open\s+(\S+)(?:\s+(.*))?$', line)
            if host and match:
                add(dict(ip=host, port=match[1], protocol=match[2], service=match[3], version=match[4] or ''), path)
    for path in list(out.glob('raw/*.gnmap')) + list(out.glob('raw/masscan-*.txt')) + list(out.glob('raw/nmap-fast-*.txt')):
        try:
            for record in parse_grepable(path.read_text(errors='replace')): add(record, path)
        except (ValueError, OSError) as exc: errors.append(f'{path.name}: {exc}')
    for path in list(out.glob('raw/tcp-*.json')) + list(out.glob('raw/masscan-*.json')) + list(out.glob('raw/ports.json')):
        try:
            data = json.loads(path.read_text())
            if path.name.startswith('tcp-'):
                for r in data.get('open', []): add(dict(r, ip=data['target'], service='tcp-connect'), path)
            elif path.name.startswith('masscan-'):
                for host in data:
                    for port in host.get('ports', []): add(dict(port, ip=host['ip'], protocol=port.get('proto', 'tcp')), path)
            else:
                for record in data: add(record, path)
        except (ValueError, KeyError, TypeError, OSError) as exc: errors.append(f'{path.name}: {exc}')
    return sorted(found.values(), key=lambda r: (ipaddress.ip_address(r['ip']).version, int(ipaddress.ip_address(r['ip'])), r['protocol'], r['port']))


def collect_axfr(out):
    result = load_json(out / 'axfr.json')
    if isinstance(result, dict): return result
    path = out / '02-axfr.txt'
    if not path.exists(): return dict(status='skipped', successful_ns=[], results=[])
    results, current, content = [], 'unknown', []
    def save():
        if not content: return
        text = '\n'.join(content)
        if re.search(r'\bIN\s+SOA\b', text) and re.search(r'\bIN\s+(?:A|AAAA|NS|MX|SOA)\b', text): status = 'success'
        elif re.search(r'REFUSED|Transfer failed', text, re.I): status = 'refused'
        else: status = 'failed'
        results.append(dict(ns=current, status=status))
    for line in lines(path):
        if line.startswith('=== AXFR '):
            save(); current = line.removeprefix('=== AXFR ').strip('= '); content = []
        else: content.append(line)
    save()
    successful = [r['ns'] for r in results if r['status'] == 'success']
    return dict(status='success' if successful else 'refused' if results and all(r['status'] == 'refused' for r in results) else 'unknown', successful_ns=successful, results=results)


def collect(out):
    out = report_directory(out)
    manifest = load_json(out / 'manifest.json', {})
    scope = '\n'.join(lines(out / '00-scope.md'))
    target_match = re.search(r'^- Normalized target:\s*(.+)$', scope, re.M)
    target = manifest.get('target', target_match[1] if target_match else out.name)
    target_id = manifest.get('target_id', normalize_target(target)['target_id'])
    errors = []
    # An existing corrupt artifact is a failed read, not an empty successful check.
    patterns = ('manifest.json', 'dns-records.json', 'cdn-excluded.json', 'http-records.json', 'axfr.json',
        'raw/org-assets.json', 'raw/cloud-buckets.json', 'raw/cloud-coverage.json',
        'raw/port-scan-status.json', 'raw/fp-*/unauth-check.json',
        'raw/fp-*/secrets.json', 'raw/fp-*/web-probe-summary.json')
    for pattern in patterns:
        for path in out.glob(pattern):
            try:
                obj = json.loads(path.read_text())
                expected = list if path.name in ('dns-records.json', 'cdn-excluded.json', 'http-records.json', 'cloud-buckets.json') else dict
                if not isinstance(obj, expected): raise ValueError('invalid top-level type')
            except (ValueError, OSError) as exc:
                errors.append(f'{path.relative_to(out)}: {exc}')
    ports = collect_ports(out, errors)
    ips = set()
    for value in lines(out / '04-ips.txt') + [r['ip'] for r in ports] + [r.get('ip', '') for r in load_json(out / 'dns-records.json', [])]:
        try: ips.add(str(ipaddress.ip_address(value)))
        except ValueError: pass
    data = dict(schema_version='2.0', target=target, target_id=target_id,
        generated_utc=utc_now(), mode=manifest.get('mode', 'unknown'),
        status=manifest.get('status', 'unknown'), run_id=manifest.get('run_id'),
        stealth=manifest.get('stealth', False), validate=manifest.get('validate', False),
        entry_urls=manifest.get('entry_urls', []), steps=manifest.get('steps', {}),
        coverage=manifest.get('coverage', {}), tools=manifest.get('tools', {}),
        subdomains=sorted(set(lines(out / '01-subdomains.txt'))), alive=lines(out / '04-alive.txt'),
        http=load_json(out / 'http-records.json', []), ips=sorted(ips),
        dns_records=load_json(out / 'dns-records.json', []), cdn_excluded=load_json(out / 'cdn-excluded.json', []),
        networks=[v for v in lines(out / '04-ips.txt') if '/' in v], ports={}, services=ports,
        axfr=collect_axfr(out), org_assets={}, asn_prefixes=lines(out / 'raw/asn-prefixes.txt'),
        cloud_buckets=[], unauth_findings=[], wayback_params=lines(out / 'raw/wayback-params.txt'), discovered_urls=[],
        dirscan=[], secrets=dict(total=0, summary={}), sensitive_paths=[], evidence_errors=errors)
    for record in ports:
        data['ports'].setdefault(record['ip'], []).append(dict(port=str(record['port']), protocol=record['protocol'], service=record['service'], version=record['version']))
    org = load_json(out / 'raw/org-assets.json', {})
    data['org_assets'] = org or dict(related_roots=lines(out / 'raw/org-related-domains.txt'))
    buckets = load_json(out / 'raw/cloud-buckets.json', [])
    if not buckets:
        for line in lines(out / 'raw/cloud-buckets.txt'):
            parts = line.split()
            if len(parts) >= 4 and parts[0] in ('PUBLIC_LISTING', 'EXISTS_200', 'EXISTS_DENIED', 'REDIRECT'):
                buckets.append(dict(verdict=parts[0], provider=parts[2], url=parts[3]))
    unique_buckets = {}
    for bucket in buckets:
        unique_buckets.setdefault(bucket.get('bucket_id', bucket.get('url')), bucket)
    data['cloud_buckets'] = list(unique_buckets.values())
    data['coverage']['cloud'] = load_json(out / 'raw/cloud-coverage.json', {})
    data['coverage']['ports'] = load_json(out / 'raw/port-scan-status.json', {})
    tech, waf, paths, favicon, secrets = [], [], [], [], Counter()
    for fp in sorted(out.glob('raw/fp-*')):
        tech += lines(fp / 'raw/nuclei-tech.txt')
        waf += lines(fp / 'raw/wafw00f.txt')
        favicon += lines(fp / 'raw/favicon-hash.txt')
        paths += lines(fp / 'raw/path-probe.txt') + lines(fp / 'extra-path-probe.txt')
        unauth = load_json(fp / 'unauth-check.json', {})
        for finding in unauth.get('findings', []):
            if finding.get('confidence') not in ('high', 'medium'): continue
            finding = dict(finding)
            for key in ('evidence', 'token_evidence'):
                value = finding.get(key)
                if isinstance(value, str) and value:
                    candidate = (fp / value).resolve()
                    finding[key] = str(candidate.relative_to(out.resolve())) if candidate.is_relative_to(fp.resolve()) else ''
            data['unauth_findings'].append(finding)
        secret = load_json(fp / 'secrets.json', {})
        secrets.update(secret.get('summary', {}))
        data['secrets']['total'] += secret.get('total', 0)
        data['dirscan'] += lines(fp / 'dirscan.txt')
        for filename in ('api-paths.txt', 'js-endpoints.txt', 'js-urls.txt', 'katana-local.txt'):
            path = fp / filename
            data['discovered_urls'] += [dict(url=url, evidence=str(path.relative_to(out))) for url in lines(path)]
        for filename in ('api-docs-found.txt', 'dirscan.txt'):
            path = fp / filename
            for line in lines(path):
                match = re.match(r'^(?:\d{3}|CRAWLED)\s+(https?://\S+)', line)
                if match: data['discovered_urls'].append(dict(url=match[1], evidence=str(path.relative_to(out))))
    data['discovered_urls'] += [dict(url=url, evidence='raw/wayback-params.txt') for url in data['wayback_params']]
    data['secrets']['summary'] = dict(secrets)
    data['sensitive_paths'] = sorted(set(lines(out / 'raw/nuclei-exposure.txt') + [p for p in paths if p.startswith('200 ')]))
    data['fingerprints'] = dict(nuclei_tech=sorted(set(tech)), waf=waf, favicon=favicon)
    data['stats'] = dict(subdomains=len(data['subdomains']), alive=len(data['alive']), ips=len(ips),
        open_ports_total=len({(p['protocol'], p['port']) for p in ports}),
        open_services_total=len(ports), related_roots=len(data['org_assets'].get('related_roots', [])),
        cloud_buckets_public=sum(b.get('verdict') == 'PUBLIC_LISTING' for b in data['cloud_buckets']),
        unauth_high=sum(f.get('confidence') == 'high' for f in data['unauth_findings']), nuclei_tech=len(set(tech)))
    if errors and data['status'] == 'completed': data['status'] = 'partial'
    return data


def render(data, chinese=True):
    cn = chinese
    title = '信息收集报告' if cn else 'Recon report'
    text = [f"# {title} — {data['target']}", '', f"**{'执行状态' if cn else 'Status'}**: {data['status']}  ",
        f"**Run**: {data.get('run_id') or 'legacy / unknown'}  ", f"**{'模式' if cn else 'Mode'}**: {data['mode']} (stealth={data['stealth']}, validate={data['validate']})  ",
        f"**{'生成时间' if cn else 'Generated'}**: {data['generated_utc']}", '',
        '| ' + ('指标 | 数量' if cn else 'Metric | Count') + ' |', '|---|---:|']
    names = [('subdomains', '子域', 'Subdomains'), ('alive', 'HTTP 存活', 'HTTP alive'), ('ips', '主机 IP', 'Host IPs'), ('open_ports_total', '唯一协议/端口', 'Unique protocol/ports'), ('open_services_total', '开放服务（IP/协议/端口）', 'Open services (IP/protocol/port)'), ('nuclei_tech', '技术指纹命中', 'Tech findings')]
    text += [f"| {zh if cn else en} | {data['stats'][key]} |" for key, zh, en in names]
    text += ['', '## ' + ('执行情况' if cn else 'Execution'), '', '| Phase | Status | Reason |', '|---|---|---|']
    for name, step in data['steps'].items(): text.append(f"| {cell(name)} | {cell(step.get('status'))} | {cell(step.get('reason', ''))} |")
    if not data['steps']: text.append('| legacy | unknown | ' + ('旧产物缺少执行清单，不能据此判定已完成检查' if cn else 'No manifest; coverage is unknown') + ' |')
    text += ['', '## ' + ('覆盖范围' if cn else 'Coverage'), '', '```json', json.dumps(data['coverage'], ensure_ascii=False, indent=2), '```', '',
        '## ' + ('端口与服务' if cn else 'Ports and services'), '', '| IP | Protocol | Port | Service | Version | Evidence |', '|---|---|---:|---|---|---|']
    for r in data['services']: text.append('| ' + ' | '.join(cell(r[k]) for k in ('ip', 'protocol', 'port', 'service', 'version', 'evidence')) + ' |')
    if not data['services']: text.append('| — | — | — | ' + ('本次记录中无确认开放服务；需结合端口阶段状态及覆盖范围判断' if cn else 'No confirmed open services in these artifacts; consult phase status and coverage') + ' | | |')
    highrisk = [r for r in data['services'] if r['port'] in (21, 23, 445, 1433, 1521, 3306, 5432, 6379, 9200, 27017, 11211, 2181, 8161, 7001, 7002)]
    text += ['', '## ' + ('需复核的暴露面' if cn else 'Exposure observations to review'), '']
    text += [f"- `{r['ip']}:{r['port']}/{r['protocol']}` — {cell(r['service'])}" for r in highrisk]
    if not highrisk: text.append('本次证据未记录典型敏感服务端口；跳过、失败或受限的检查不构成安全结论。' if cn else 'No typical sensitive service ports in the evidence. Skipped, failed or limited checks are inconclusive.')
    axfr = data['axfr']
    if axfr.get('successful_ns'):
        text += ['', '**AXFR ' + ('成功' if cn else 'succeeded') + '**: ' + ', '.join(axfr['successful_ns'])]
    else:
        text += ['', '**AXFR**: ' + axfr.get('status', 'unknown')]
    text += ['', '```json', json.dumps(axfr.get('results', []), ensure_ascii=False, indent=2), '```', '',
        '### ' + ('敏感路径候选（200 或模板命中，需内容复核）' if cn else 'Sensitive path candidates (content review required)'), '']
    text += ['- ' + cell(p) for p in data['sensitive_paths'][:50]] or ['—']
    text += ['', '### ' + ('匿名接口与 token 响应差异（权限语义待复核）' if cn else 'Anonymous endpoints / token differences (authorization review required)'), '']
    text += ['- ' + cell(f.get('url')) + ' — ' + cell(f.get('verdict')) + ' [' + cell(f.get('confidence')) + ']' for f in data['unauth_findings'][:50]] or ['—']
    text += ['', '### ' + ('云存储候选（公开列举不自动等同于数据泄露）' if cn else 'Cloud storage observations (listing alone does not prove a data leak)'), '']
    text += ['- ' + cell(b.get('url')) + ' — ' + cell(b.get('verdict')) for b in data['cloud_buckets'][:40]] or ['—']
    text += ['', '### ' + ('技术指纹' if cn else 'Technology fingerprints'), '', '```', '\n'.join(data['fingerprints']['nuclei_tech'][:50]), '```', '',
        '### WAF / CDN', '', '```', '\n'.join(data['fingerprints']['waf'][:40]), '```', '',
        '### ' + ('组织/ASN 关联线索（不自动加入扫描范围）' if cn else 'Organization/ASN candidates (not automatically in scope)'), '',
        '```json', json.dumps(dict(org_assets=data['org_assets'], asn_prefixes=data['asn_prefixes']), ensure_ascii=False, indent=2), '```', '',
        '### ' + ('前端敏感信息候选统计' if cn else 'Frontend secret candidate counts'), '', '```json', json.dumps(data['secrets'], ensure_ascii=False, indent=2), '```', '',
        '## ' + ('证据与附件' if cn else 'Evidence and artifacts'), '',
        '- `manifest.json` — ' + ('阶段执行状态' if cn else 'Phase status'),
        '- `commands.jsonl` — ' + ('命令、退出码、耗时、stderr 路径' if cn else 'Commands, return codes, duration, stderr'),
        '- `recon.json` — ' + ('完整机器数据；正文仅展示摘要' if cn else 'Full machine-readable data; report previews are limited'),
        '- `raw/` — ' + ('原始工具产物' if cn else 'Raw tool evidence')]
    if data['evidence_errors']: text += ['', '**Evidence errors**', *['- ' + cell(e) for e in data['evidence_errors']]]
    return '\n'.join(text) + '\n'


def build_report(out):
    out = report_directory(out)
    data = collect(out)
    atomic_json(out / 'recon.json', data)
    from skyshield_output import skyshield_output
    atomic_json(out / 'skyshield-import.json', skyshield_output(data))
    (out / '08-recon-summary.md').write_text(render(data, False))
    (out / f"信息收集报告_{data['target_id']}.md").write_text(render(data, True))
    manifest = load_json(out / 'manifest.json')
    if manifest and manifest.get('status') != data['status']:
        manifest['status'] = data['status']; atomic_json(out / 'manifest.json', manifest)
    return data


def build_batch(targets, output_root, timestamp, elapsed=0, statuses=None):
    root = Path(output_root)
    root.mkdir(parents=True, exist_ok=True)
    text = [f'# 批量信息收集汇总 — {timestamp}', '', f'目标数: {len(targets)}；总耗时: {elapsed}s', '',
        '| 目标 | 状态 | 子域 | HTTP 存活 | IP | 开放服务 | 报告 |', '|---|---|---:|---:|---:|---:|---|']
    for target in targets:
        item = normalize_target(target)
        state = (statuses or {}).get(item['target_id'], {})
        directory = root / item['target_id']
        if state.get('run_id'): directory = directory / 'runs' / state['run_id']
        data = load_json(directory / 'recon.json', {})
        status = state.get('status', data.get('status', 'not_run'))
        stats = data.get('stats', {})
        report = directory / f"信息收集报告_{item['target_id']}.md"
        link = f"[查看]({report.relative_to(root)})" if report.exists() else '—'
        text.append(f"| {cell(item['target'])} | {cell(status)} | {stats.get('subdomains', '—')} | {stats.get('alive', '—')} | {stats.get('ips', '—')} | {stats.get('open_services_total', '—')} | {link} |")
    text += ['', '执行失败、跳过或受限的检查需结合各目标 manifest 和覆盖范围判断。']
    path = root / f'批量侦察汇总_{timestamp}.md'
    path.write_text('\n'.join(text) + '\n')
    return path


def main():
    p = argparse.ArgumentParser(description=__doc__)
    p.add_argument('target', nargs='?')
    p.add_argument('--outdir', type=Path)
    p.add_argument('--targets', nargs='+')
    p.add_argument('--output-dir', type=Path, default=Path('reports'))
    p.add_argument('--timestamp', default=utc_now().replace(':', '').replace('.', '-'))
    p.add_argument('--elapsed', type=int, default=0)
    a = p.parse_args()
    if a.targets:
        print(build_batch(a.targets, a.output_dir, a.timestamp, a.elapsed)); return 0
    out = a.outdir or a.output_dir / (a.target or '')
    if not out.is_dir(): p.error(f'目录不存在: {out}')
    print(json.dumps(build_report(out)['stats'], ensure_ascii=False))
    return 0


if __name__ == '__main__':
    raise SystemExit(main())
