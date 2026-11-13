#!/usr/bin/env python3
"""AXFR checks with per-nameserver success/refusal/error evidence."""
from __future__ import annotations
import argparse
import json
import re
import shutil
import socket
import subprocess
import sys
from pathlib import Path
from recon_common import atomic_json, normalize_host, utc_now


def dig_query(args, timeout):
    p = subprocess.run(['dig', *args], capture_output=True, text=True, timeout=timeout)
    return p.returncode, p.stdout + p.stderr


def main():
    p = argparse.ArgumentParser(description=__doc__)
    p.add_argument('domain')
    p.add_argument('--output', '-o', type=Path)
    p.add_argument('--timeout', type=float, default=10)
    a = p.parse_args()
    domain = normalize_host(a.domain)
    results, records, ns_list, errors = [], [], [], []
    if shutil.which('dig'):
        try:
            code, text = dig_query(['+short', '+time=3', '+tries=1', 'NS', domain], a.timeout)
            ns_list = [x.rstrip('.') for x in text.splitlines() if re.fullmatch(r'[a-zA-Z0-9.-]+\.', x)]
            if code or not ns_list: errors.append('NS query failed or returned no nameservers')
        except (OSError, subprocess.TimeoutExpired) as exc: errors.append(str(exc))
        for ns in ns_list:
            try:
                code, text = dig_query(['AXFR', domain, '@' + ns, '+time=' + str(max(1, int(a.timeout))), '+tries=1', '+noall', '+answer', '+comments'], a.timeout)
                parsed = []
                for line in text.splitlines():
                    m = re.match(r'^(\S+)\s+(\d+)\s+IN\s+(\S+)\s+(.+)$', line)
                    if m: parsed.append(dict(name=m[1].rstrip('.'), ttl=int(m[2]), type=m[3], data=m[4]))
                soa = [r for r in parsed if r['type'] == 'SOA']
                status = 'success' if code == 0 and len(soa) >= 2 else 'refused' if 'REFUSED' in text else 'failed'
                results.append(dict(ns=ns, status=status, record_count=len(parsed), evidence=text[:65536]))
                if status == 'success': records.extend(parsed)
            except (OSError, subprocess.TimeoutExpired) as exc:
                results.append(dict(ns=ns, status='timeout' if isinstance(exc, subprocess.TimeoutExpired) else 'failed', error=str(exc)))
    else:
        try:
            import dns.query
            import dns.resolver
            import dns.zone
            import dns.rdatatype
            ns_list = [str(x.target).rstrip('.') for x in dns.resolver.resolve(domain, 'NS', lifetime=a.timeout)]
            for ns in ns_list:
                try:
                    addresses = socket.getaddrinfo(ns, 53, type=socket.SOCK_STREAM)
                    address = addresses[0][4][0]
                    zone = dns.zone.from_xfr(dns.query.xfr(address, domain, lifetime=a.timeout))
                    observed = []
                    for name, node in zone.nodes.items():
                        fqdn = domain if str(name) == '@' else str(name) + '.' + domain
                        for rdataset in node.rdatasets:
                            for rdata in rdataset:
                                observed.append(dict(name=fqdn, ttl=rdataset.ttl, type=dns.rdatatype.to_text(rdataset.rdtype), data=str(rdata)))
                    records.extend(observed)
                    results.append(dict(ns=ns, address=address, status='success', record_count=len(observed)))
                except Exception as exc:
                    results.append(dict(ns=ns, status='refused' if 'REFUSED' in str(exc) else 'failed', error=str(exc)))
        except Exception as exc: errors.append('dig unavailable / dnspython fallback failed: ' + str(exc))
    unique = {(r['name'], r['type'], r['data']): r for r in records}
    successes = [r['ns'] for r in results if r['status'] == 'success']
    incomplete = bool(errors) or not results or any(r['status'] in ('failed', 'timeout') for r in results)
    result = dict(domain=domain, status='partial' if incomplete else 'success' if successes else 'refused',
        successful_ns=successes, nameservers=ns_list, results=results, records=list(unique.values()), record_count=len(unique), errors=errors, timestamp_utc=utc_now())
    if a.output: atomic_json(a.output, result)
    print(json.dumps(result, ensure_ascii=False, indent=2))
    return 2 if incomplete else 0


if __name__ == '__main__':
    raise SystemExit(main())
