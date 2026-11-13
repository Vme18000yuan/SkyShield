#!/usr/bin/env python3
"""Check executable capabilities, not just PATH membership."""
import argparse
import json
import os
from pathlib import Path
from recon_common import tool_info


def main():
    p = argparse.ArgumentParser(description=__doc__)
    p.add_argument('--json', action='store_true'); p.add_argument('--require', nargs='+')
    a = p.parse_args()
    names = a.require or ['curl', 'dig', 'whois', 'openssl', 'httpx', 'dnsx', 'subfinder', 'amass', 'assetfinder', 'nmap', 'masscan', 'whatweb', 'wafw00f', 'nuclei', 'katana', 'ffuf']
    result = {name: tool_info(name) for name in names}
    if a.json: print(json.dumps(result, ensure_ascii=False, indent=2))
    else:
        for name, r in result.items():
            print(f"{name:15} {'OK' if r['usable'] else 'UNAVAILABLE':12} {r['path'] or '-'} {r.get('reason') or r.get('version')}")
        print('SecLists:', 'found' if Path('/usr/share/seclists').is_dir() else 'not installed (optional)')
        for key in ('PDCP_API_KEY', 'GITHUB_TOKEN', 'COS_APP_ID'):
            print(key + ': ' + ('set' if os.getenv(key) else 'not set'))
    return 1 if a.require and any(not r['usable'] for r in result.values()) else 0


if __name__ == '__main__':
    raise SystemExit(main())
