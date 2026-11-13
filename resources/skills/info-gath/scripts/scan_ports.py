#!/usr/bin/env python3
"""Bounded scanner selection; preserve each (IP, protocol, port) observation."""
from __future__ import annotations
import argparse
import ipaddress
import json
import os
import shutil
import subprocess
import sys
import time
import xml.etree.ElementTree as ET
from pathlib import Path
from recon_common import atomic_json, lines, load_json, parse_nmap_xml, run_command, safe_id


def read_batch_xml(path, networks):
    """Recover completed hosts even if a timed-out Nmap XML document is truncated."""
    records, hosts, complete, invalid = [], {}, False, False
    try:
        for _, host in ET.iterparse(path, events=('end',)):
            if host.tag != 'host':
                continue
            addresses = []
            for value in host.findall('address'):
                if value.get('addrtype') not in ('ipv4', 'ipv6'):
                    continue
                try:
                    address = ipaddress.ip_address(value.get('addr', ''))
                    if not any(address.version == network.version and address in network for network in networks):
                        invalid = True
                        continue
                    addresses.append(str(address))
                except ValueError:
                    invalid = True
            state = 'timeout' if host.get('timedout', '').lower() in ('true', '1') else 'success'
            for address in addresses:
                hosts[address] = state
            if state == 'success':
                for port in host.findall('ports/port'):
                    port_state = port.find('state')
                    if port_state is None or port_state.get('state') != 'open':
                        continue
                    try:
                        number = int(port.get('portid', '0'))
                        protocol = port.get('protocol', 'tcp')
                        if not 1 <= number <= 65535 or protocol not in ('tcp', 'udp'):
                            raise ValueError('invalid port')
                        service = port.find('service')
                        attrs = service.attrib if service is not None else {}
                        for address in addresses:
                            records.append(dict(ip=address, port=number, protocol=protocol, state='open', service=attrs.get('name', ''), version=' '.join(attrs.get(k, '') for k in ('product', 'version', 'extrainfo')).strip()))
                    except ValueError:
                        invalid = True
            host.clear()
        complete = True
    except (OSError, ET.ParseError):
        pass
    return records, hosts, complete, invalid


def scan_normal_nmap(targets, outdir, rate, stage_seconds):
    """Scan the same address queue in bounded IPv4/IPv6 batches and checkpoint results."""
    # Finish writing results before the enclosing phase's deadline expires.
    deadline = time.monotonic() + max(.25, stage_seconds - 4)
    networks = {target: ipaddress.ip_network(target, strict=False) for target in targets}
    groups = [(version, [target for target in targets if networks[target].version == version]) for version in (4, 6)]
    groups = [(version, members) for version, members in groups if members]
    results, statuses = [], []
    limited = False

    def save():
        completed = sum(item['status'] == 'success' for item in statuses)
        timed_out = sum(item['status'] == 'timeout' for item in statuses)
        failed = sum(item['status'] == 'failed' for item in statuses)
        partial = limited or completed != len(targets)
        atomic_json(outdir / 'raw/ports.json', results)
        atomic_json(outdir / 'raw/port-scan-status.json', dict(
            status='partial' if partial else 'success', targets=targets, results=statuses,
            total_targets=len(targets), completed_targets=completed,
            timed_out_targets=timed_out, failed_targets=failed,
            unprocessed_targets=sum(item['status'] == 'unprocessed' for item in statuses) + max(0, len(targets) - len(statuses)),
            service_detail='disabled_for_http_proxy' if os.getenv('RECON_MANAGED_PROXY') == '1' else 'active',
        ))

    save()
    for index, (version, group) in enumerate(groups):
        remaining = deadline - time.monotonic()
        if remaining <= 1:
            limited = True
            break
        allocation = max(1, (remaining - 1) / (len(groups) - index))
        host_timeout = max(1, min(60, int(allocation - 2)))
        input_file = outdir / f'raw/port-targets-v{version}.txt'
        input_file.write_text(''.join(target + '\n' for target in group))
        xml_file = outdir / f'raw/port-discovery-v{version}.xml'
        argv = ['nmap', *(['-6'] if version == 6 else []), '-n', '-Pn', '-sT', '-T4', '--max-rate', str(rate),
            '--max-retries', '2', '--host-timeout', f'{host_timeout}s', '--min-hostgroup', str(min(16, len(group))),
            '--max-hostgroup', '64', '-F', '-iL', input_file, '-oX', xml_file]
        command = run_command(argv, outdir, f'nmap-batch-v{version}', timeout=allocation)
        found, hosts, complete, invalid = read_batch_xml(xml_file, [networks[target] for target in group])
        results.extend(found)
        limited |= command['status'] != 'success' or not complete or invalid
        for target in group:
            network = networks[target]
            matching = [state for address, state in hosts.items() if ipaddress.ip_address(address) in network]
            if matching and 'timeout' not in matching and ('/' not in target or (command['status'] == 'success' and complete)):
                state = 'success'
            elif 'timeout' in matching:
                state = 'timeout'
            elif command['status'] == 'timeout':
                state = 'unprocessed'
            else:
                state = 'failed'
            statuses.append(dict(target=target, scanner='nmap-batch-top100', status=state,
                coverage='top 100 TCP ports', open_services=sum(ipaddress.ip_address(item['ip']) in network for item in found)))
        save()
        # Preserve the existing single-host fallback if Nmap fails to start or
        # exits with an error. A timed-out batch is not repeated automatically.
        if command['status'] == 'failed':
            pending = [item for item in statuses[-len(group):] if item['status'] == 'failed' and '/' not in item['target']]
            for position, item in enumerate(pending):
                remaining = deadline - time.monotonic()
                if remaining <= 1:
                    break
                target = item['target']
                dest = outdir / f"raw/tcp-{safe_id(target)}.json"
                budget = min(15, max(.25, (remaining - 1) / (len(pending) - position)))
                args = [sys.executable, Path(__file__).with_name('port_scan.py'), '-t', target, '--profile', 'full', '--quiet', '--workers', '30', '--delay', str(1 / max(1, rate)), '-o', dest]
                attempt = run_command(args, outdir, 'tcp-' + safe_id(target), timeout=budget)
                data = load_json(dest, {})
                if attempt['status'] == 'success' and isinstance(data.get('open'), list):
                    valid = []
                    for record in data['open']:
                        try:
                            port = int(record['port'])
                            if 1 <= port <= 65535:
                                valid.append(dict(ip=target, port=port, protocol='tcp', state='open', service='tcp-connect', version=''))
                        except (ValueError, KeyError, TypeError):
                            continue
                    results.extend(valid)
                    item.update(scanner='python-profile-full', status='success', coverage='built-in common TCP profile', open_services=len(valid))
                elif attempt['status'] == 'timeout':
                    item['status'] = 'timeout'
                save()
        print(f'端口扫描：已处理 {len(statuses)}/{len(targets)} 个地址或网段。', flush=True)

    save()
    print(f"Port scan: {len(results)} observations; {'partial' if limited or any(item['status'] != 'success' for item in statuses) or len(statuses) != len(targets) else 'success'}")
    return 2 if limited or any(item['status'] != 'success' for item in statuses) or len(statuses) != len(targets) else 0


def main():
    p = argparse.ArgumentParser(description=__doc__)
    p.add_argument('input')
    p.add_argument('outdir', nargs='?', type=Path, default=Path('scan-out'))
    a = p.parse_args()
    a.outdir = a.outdir.resolve(); (a.outdir / 'raw').mkdir(parents=True, exist_ok=True)
    if os.getenv('RECON_MODE') == 'fast' or os.getenv('RECON_NO_PORTSCAN') == '1':
        atomic_json(a.outdir / 'raw/port-scan-status.json', dict(status='skipped', reason='mode / --no-port-scan'))
        return 0
    source = lines(a.input) if Path(a.input).is_file() else [a.input]
    targets = []
    for line in source:
        line = line.split('#', 1)[0].strip()
        if not line: continue
        try:
            net = ipaddress.ip_network(line, strict=False)
            targets.append(str(net) if '/' in line else str(net.network_address))
        except ValueError: p.error(f'无效的 IP/CIDR: {line}')
    targets = sorted(set(targets))
    if not targets:
        atomic_json(a.outdir / 'raw/port-scan-status.json', dict(status='skipped', reason='队列为空（可能因 CDN 排除）', targets=[]))
        return 0
    stage_seconds = int(os.getenv('RECON_STAGE_TIMEOUT', '300'))
    deadline = time.monotonic() + stage_seconds
    deep = os.getenv('RECON_MODE') == 'deep'
    stealth = os.getenv('RECON_STEALTH') == '1'
    timing = '-T2' if stealth else '-T4'
    rate = int(os.getenv('MASSCAN_RATE', '2000'))
    if not deep and not stealth and os.getenv('RECON_MANAGED_PROXY') == '1' and shutil.which('nmap'):
        return scan_normal_nmap(targets, a.outdir, rate, stage_seconds)
    results, statuses = [], []
    def command(name, argv):
        remain = deadline - time.monotonic()
        if remain <= 0: return False
        return run_command(argv, a.outdir, name, timeout=remain)['status'] == 'success'
    def read_xml(path):
        try: return parse_nmap_xml(path)
        except Exception as exc:
            statuses.append(dict(status='failed', evidence=path.name, reason=str(exc)))
            return []
    for target in targets:
        if time.monotonic() >= deadline:
            statuses.append(dict(target=target, status='timeout', reason='stage budget reached')); break
        ident = safe_id(target)
        network = ipaddress.ip_network(target, strict=False)
        family = ['-6'] if network.version == 6 else []
        discovered, scanner, ok = [], '', False
        raw_socket = os.geteuid() == 0
        if not raw_socket and shutil.which('masscan') and shutil.which('getcap'):
            cap = subprocess.run(['getcap', shutil.which('masscan')], capture_output=True, text=True, timeout=3)
            raw_socket = 'cap_net_raw' in cap.stdout
        if deep and not stealth and raw_socket and shutil.which('masscan'):
            scanner = 'masscan-full'
            dest = a.outdir / f'raw/masscan-{ident}.json'
            ok = command('masscan-' + ident, ['masscan', '-p1-65535', '--rate=' + str(rate), '--open', target, '-oJ', dest])
            data = load_json(dest)
            if ok and isinstance(data, list):
                try:
                    for host in data:
                        for port in host.get('ports', []):
                            if port.get('status', 'open') == 'open':
                                discovered.append(dict(ip=str(ipaddress.ip_address(host['ip'])), port=int(port['port']), protocol=port.get('proto', 'tcp'), state='open', service='', version=''))
                except (ValueError, KeyError, TypeError): ok = False
            else: ok = False
        if not ok and shutil.which('nmap'):
            scanner = 'nmap-full' if deep else 'nmap-top100'
            dest = a.outdir / f'raw/nmap-discovery-{ident}.xml'
            scan = ['-p-'] if deep else ['-F']
            ok = command('nmap-discovery-' + ident, ['nmap', *family, '-Pn', '-sT', timing, '--max-rate', str(rate), '--open', *scan, target, '-oX', dest])
            if ok and dest.exists(): discovered = read_xml(dest)
            elif ok: ok = False
        if not ok:
            if '/' in target:
                statuses.append(dict(target=target, status='failed', reason='CIDR 扫描需要可用的 nmap/masscan')); continue
            scanner = 'python-profile-full'
            dest = a.outdir / f'raw/tcp-{ident}.json'
            args = [sys.executable, Path(__file__).with_name('port_scan.py'), '-t', target, '--profile', 'full', '--quiet', '--workers', '2' if stealth else '30', '--delay', str(1 / min(rate, 10 if stealth else rate)), '-o', dest]
            if os.getenv('RECON_MANAGED_PROXY') != '1': args.append('--banner')
            ok = command('tcp-' + ident, args)
            data = load_json(dest, {})
            if ok:
                discovered = [dict(r, ip=target, protocol='tcp', service='tcp-connect', version=r.get('banner', '')) for r in data.get('open', [])]
        # Reject addresses outside the requested network even if tool output is malformed.
        valid = []
        for r in discovered:
            try:
                if ipaddress.ip_address(r['ip']) in network and 1 <= int(r['port']) <= 65535 and r['protocol'] in ('tcp', 'udp'):
                    valid.append(r)
            except (ValueError, KeyError, TypeError): pass
        results.extend(valid)
        statuses.append(dict(target=target, scanner=scanner, status='success' if ok else 'failed', coverage='all TCP ports' if scanner in ('masscan-full', 'nmap-full') else 'top 100 TCP ports' if scanner == 'nmap-top100' else 'built-in common TCP profile', open_services=len(valid)))
        if ok and shutil.which('nmap') and os.getenv('RECON_MANAGED_PROXY') != '1':
            by_host = {}
            for r in valid:
                if r['protocol'] == 'tcp': by_host.setdefault(r['ip'], set()).add(str(r['port']))
            for host, ports in by_host.items():
                dest = a.outdir / ('raw/nmap-' + safe_id(host))
                family = ['-6'] if ':' in host else []
                # No sudo prompts or vulnerability scripts in discovery.
                argv = ['nmap', *family, '-Pn', '-sT', timing, '--max-rate', str(rate), '-sV', '--version-intensity', '2' if stealth else '5', '--open']
                if not stealth: argv += ['--script=banner,http-headers,http-title,ssl-cert']
                argv += ['-p', ','.join(sorted(ports, key=int)), host, '-oA', dest]
                detail_ok = command('nmap-detail-' + safe_id(host), argv)
                statuses.append(dict(target=host, status='success' if detail_ok else 'failed', stage='service-detail'))
                if detail_ok and Path(str(dest) + '.xml').exists(): results.extend(read_xml(Path(str(dest) + '.xml')))
    atomic_json(a.outdir / 'raw/ports.json', results)
    partial = any(s['status'] != 'success' for s in statuses)
    atomic_json(a.outdir / 'raw/port-scan-status.json', dict(status='partial' if partial else 'success', targets=targets, results=statuses,
        service_detail='disabled_for_http_proxy' if os.getenv('RECON_MANAGED_PROXY') == '1' else 'active'))
    print(f"Port scan: {len(results)} observations; {'partial' if partial else 'success'}")
    return 2 if partial else 0


if __name__ == '__main__':
    raise SystemExit(main())
