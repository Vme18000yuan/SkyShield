#!/usr/bin/env python3
"""
TCP connect port scanner for reconnaissance.
Profiles align with references/03-port-scanning.md

Features:
- Multi-profile port sets (quick/web/middleware/iot_ics/host/full)
- Concurrent scanning with adaptive workers
- Banner grabbing for open ports
- Resume support via JSON state file
- Progress bar (tqdm if available)
- Rate limiting and timeout handling
"""

from __future__ import annotations

import argparse
import json
import socket
import sys
import time
from concurrent.futures import ThreadPoolExecutor, as_completed
from datetime import datetime, timezone
from pathlib import Path
from typing import Any, Dict, List, Set
from recon_common import atomic_json, normalize_target

try:
    from tqdm import tqdm

    HAS_TQDM = True
except ImportError:
    HAS_TQDM = False

PROFILES: Dict[str, List[int]] = {
    "quick": [
        21,
        22,
        23,
        25,
        53,
        80,
        110,
        135,
        139,
        143,
        443,
        445,
        993,
        995,
        1433,
        1521,
        3306,
        3389,
        5432,
        5900,
        6379,
        8080,
        8443,
        27017,
    ],
    "web": [
        80,
        443,
        3000,
        5000,
        7001,
        7002,
        8000,
        8001,
        8080,
        8081,
        8082,
        8083,
        8084,
        8085,
        8088,
        8089,
        8443,
        8888,
        9000,
        9080,
        9090,
        9200,
        9443,
        10000,
        10443,
    ],
    "middleware": [
        443,
        1099,
        2181,
        2888,
        3888,
        4848,
        5432,
        5601,
        5672,
        61616,
        6379,
        7001,
        7002,
        8005,
        8009,
        8080,
        8161,
        8443,
        9080,
        9200,
        9300,
        10911,
        10912,
        11211,
        1521,
        1433,
        20880,
        27017,
        3306,
        15672,
        9092,
    ],
    "iot_ics": [
        21,
        22,
        23,
        80,
        443,
        502,
        503,
        102,
        1089,
        1090,
        1883,
        1911,
        1962,
        2404,
        44818,
        47808,
        5683,
        5684,
        8883,
        20000,
        4840,
    ],
    "host": [
        20,
        21,
        22,
        23,
        25,
        53,
        110,
        135,
        139,
        143,
        445,
        993,
        995,
        1433,
        1521,
        3306,
        3389,
        5432,
        5900,
        5985,
        5986,
        6379,
        9200,
        27017,
    ],
    "db": [1433, 1521, 3306, 5432, 6379, 11211, 27017, 9200, 9300, 5601, 9092],
}


def ports_for_profile(name: str) -> List[int]:
    if name == "full":
        merged: Set[int] = set()
        for key in ("quick", "web", "middleware", "iot_ics", "host"):
            merged.update(PROFILES[key])
        return sorted(merged)
    if name not in PROFILES:
        raise ValueError(f"Unknown profile: {name}. Choose from: {list(PROFILES)} + full")
    return PROFILES[name]


def parse_ports(spec: str) -> List[int]:
    """Parse comma-separated ports or ranges like 8000-8010."""
    out: Set[int] = set()
    for part in spec.split(","):
        part = part.strip()
        if not part:
            continue
        if "-" in part:
            a, b = part.split("-", 1)
            lo, hi = int(a), int(b)
            if lo > hi:
                lo, hi = hi, lo
            if not 1 <= lo <= hi <= 65535:
                raise ValueError("Port range must be within 1-65535")
            for p in range(lo, hi + 1):
                if 1 <= p <= 65535:
                    out.add(p)
        else:
            p = int(part)
            if not 1 <= p <= 65535:
                raise ValueError("Port must be within 1-65535")
            out.add(p)
    return sorted(out)


def probe(host: str, port: int, timeout: float) -> tuple[int, bool, str | None]:
    try:
        with socket.create_connection((host, port), timeout=timeout):
            return port, True, None
    except socket.timeout:
        return port, False, "timeout"
    except OSError as e:
        return port, False, str(e)


def grab_banner(host: str, port: int, timeout: float) -> str | None:
    """Attempt to grab a banner from an open port."""
    try:
        with socket.create_connection((host, port), timeout=timeout) as s:
            s.settimeout(timeout)
            # HTTP-like ports: send HEAD request
            if port in (80, 8080, 8000, 8888, 3000, 5000, 7001, 9080, 9090, 9200):
                s.sendall(f"HEAD / HTTP/1.0\r\nHost: {host}\r\n\r\n".encode())
            # Generic probe
            else:
                s.sendall(b"\r\n")
            data = s.recv(1024)
            if data:
                text = data.decode("utf-8", errors="replace").strip()
                # Clean up: take first line or first 400 chars
                first_line = text.split("\n")[0].strip()
                return first_line[:400] if first_line else text[:400]
    except OSError:
        return None
    return None


def load_state(state_file: Path) -> Dict[str, Any]:
    if state_file.exists():
        try:
            return json.loads(state_file.read_text(encoding="utf-8"))
        except Exception:
            pass
    return {"scanned": [], "open": []}


def save_state(state_file: Path, state: Dict[str, Any]) -> None:
    state_file.parent.mkdir(parents=True, exist_ok=True)
    atomic_json(state_file, state)


def main() -> int:
    parser = argparse.ArgumentParser(
        description="TCP port scanner for reconnaissance",
        formatter_class=argparse.RawDescriptionHelpFormatter,
        epilog="""
Profiles:
  quick      ~24 common ports (web + db + remote)
  web        25 web-related ports (incl. dev, proxy, app servers)
  middleware 31 middleware / Java / MQ / cache ports
  iot_ics    21 IoT / ICS / SCADA ports
  host       24 host / Windows / Linux remote ports
  db         11 database / cache / search ports
  full       union of quick+web+middleware+iot_ics+host (~90 ports)

Examples:
  python port_scan.py -t 203.0.113.10 -p web --banner -o result.json
  python port_scan.py -t example.com --ports 8000-8100,8443 --workers 40
        """,
    )
    parser.add_argument("--target", "-t", required=True, help="IP or hostname to scan")
    parser.add_argument(
        "--profile",
        "-p",
        default="quick",
        choices=list(PROFILES.keys()) + ["full"],
        help="Port set profile (default: quick)",
    )
    parser.add_argument("--ports", help="Custom ports: 80,443 or 8000-8010,8443")
    parser.add_argument("--timeout", type=float, default=2.5, help="Connect timeout seconds (default: 2.5)")
    parser.add_argument("--workers", type=int, default=100, help="Concurrent workers (default: 100)")
    parser.add_argument("--banner", action="store_true", help="Grab banner on open ports")
    parser.add_argument("--output", "-o", help="Write JSON results to file")
    parser.add_argument("--resume", metavar="STATE.json", help="Resume from previous state file")
    parser.add_argument("--delay", type=float, default=0.0, help="Delay between probes (seconds)")
    parser.add_argument("--quiet", "-q", action="store_true", help="Suppress per-port output, only show summary")
    args = parser.parse_args()
    if args.workers < 1 or args.timeout <= 0 or args.delay < 0:
        parser.error("workers/timeout must be positive; delay must be nonnegative")

    # Resolve ports
    if args.ports:
        try:
            ports = parse_ports(args.ports)
        except ValueError as exc:
            parser.error(str(exc))
        profile_name = "custom"
    else:
        ports = ports_for_profile(args.profile)
        profile_name = args.profile

    try:
        normalized = normalize_target(args.target)
        if normalized['kind'] == 'CIDR': parser.error("TCP connect fallback accepts one host, not a CIDR")
        target = normalized['target']
    except (ValueError, UnicodeError) as exc:
        parser.error(str(exc))

    # Resume state
    state_file = Path(args.resume) if args.resume else None
    state = load_state(state_file) if state_file else {"scanned": [], "open": []}
    if state_file and state_file.exists() and state.get('target') != target:
        parser.error("Resume state belongs to another/unknown target; use a new state file")
    state['target'] = target
    already_scanned: Set[int] = set(state.get("scanned", []))
    known_open: List[Dict[str, Any]] = [r for r in state.get("open", []) if r.get('port') in ports]

    remaining = [p for p in ports if p not in already_scanned]
    if not remaining and not args.quiet:
        print(f"[*] All {len(ports)} ports already scanned (resume).", file=sys.stderr)

    open_ports: List[Dict[str, Any]] = list(known_open)
    scanned_count = len(already_scanned)

    # Progress bar setup
    if HAS_TQDM and not args.quiet:
        pbar = tqdm(total=len(remaining), desc=f"Scanning {target}", unit="port")
    else:
        pbar = None

    start_time = time.time()

    with ThreadPoolExecutor(max_workers=args.workers) as ex:
        future_to_port = {}
        next_probe = time.monotonic()
        for port in remaining:
            if args.delay:
                time.sleep(max(0.0, next_probe - time.monotonic()))
                next_probe = time.monotonic() + args.delay
            future_to_port[ex.submit(probe, target, port, args.timeout)] = port

        for fut in as_completed(future_to_port):
            port = future_to_port[fut]
            try:
                port, is_open, err = fut.result()
            except Exception as e:
                port, is_open, err = port, False, str(e)

            scanned_count += 1
            if state_file:
                state["scanned"].append(port)

            if is_open:
                entry: Dict[str, Any] = {"port": port, "state": "open"}
                if args.banner:
                    banner = grab_banner(target, port, args.timeout)
                    if banner:
                        entry["banner"] = banner
                open_ports.append(entry)
                if state_file:
                    state["open"].append(entry)
                if not args.quiet:
                    banner_str = f" | {entry.get('banner', '')[:60]}" if args.banner and entry.get("banner") else ""
                    print(f"[+] {target}:{port} OPEN{banner_str}")

            if pbar:
                pbar.update(1)
            elif not args.quiet:
                # Simple progress every 10 ports
                if scanned_count % 10 == 0:
                    print(f"[*] Progress: {scanned_count}/{len(ports)}", file=sys.stderr)

            if state_file and scanned_count % 25 == 0:
                save_state(state_file, state)

    if pbar:
        pbar.close()

    # Deduplicate and sort
    seen_ports: Set[int] = set()
    deduped: List[Dict[str, Any]] = []
    for e in sorted(open_ports, key=lambda x: x["port"]):
        if e["port"] not in seen_ports:
            seen_ports.add(e["port"])
            deduped.append(e)

    elapsed = time.time() - start_time

    result: Dict[str, Any] = {
        "target": target,
        "profile": profile_name,
        "scanned_count": len(ports),
        "open_count": len(deduped),
        "open": deduped,
        "elapsed_sec": round(elapsed, 2),
        "timestamp_utc": datetime.now(timezone.utc).isoformat(),
        "workers": args.workers,
        "timeout": args.timeout,
    }

    # Save final state
    if state_file:
        save_state(state_file, state)

    # Output
    text = json.dumps(result, indent=2, ensure_ascii=False)
    if not args.quiet:
        print(text)
    if args.output:
        Path(args.output).parent.mkdir(parents=True, exist_ok=True)
        Path(args.output).write_text(text, encoding="utf-8")
        if args.quiet:
            print(f"[*] Wrote {len(deduped)} open ports to {args.output}", file=sys.stderr)

    return 0


if __name__ == "__main__":
    sys.exit(main())
