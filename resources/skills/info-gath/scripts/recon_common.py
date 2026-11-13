#!/usr/bin/env python3
"""Shared target, process and evidence handling. Standard library only."""
from __future__ import annotations

import hashlib
import ipaddress
import json
import os
import re
import shutil
import signal
import subprocess
import sys
import time
import xml.etree.ElementTree as ET
from datetime import datetime, timezone
from pathlib import Path
from urllib.parse import urlsplit, urlunsplit


def utc_now():
    return datetime.now(timezone.utc).isoformat()


def atomic_json(path, data):
    path = Path(path)
    path.parent.mkdir(parents=True, exist_ok=True)
    temp = path.with_name(path.name + f".{os.getpid()}.tmp")
    temp.write_text(json.dumps(data, ensure_ascii=False, indent=2) + "\n")
    temp.replace(path)


def load_json(path, default=None):
    try:
        value = json.loads(Path(path).read_text())
        if isinstance(default, (dict, list)) and not isinstance(value, type(default)):
            return default
        return value
    except (OSError, ValueError):
        return default


def lines(path):
    try:
        return [s.strip() for s in Path(path).read_text(errors="replace").splitlines() if s.strip()]
    except OSError:
        return []


def write_lines(path, values):
    Path(path).parent.mkdir(parents=True, exist_ok=True)
    Path(path).write_text("".join(str(x) + "\n" for x in sorted(set(values))))


def safe_id(value):
    result = re.sub(r"[^a-zA-Z0-9._-]", "_", value)
    if len(result) > 170:
        result = result[:150] + "-" + hashlib.sha256(value.encode()).hexdigest()[:16]
    return result


def normalize_host(host):
    host = host.rstrip(".").lower()
    try:
        return str(ipaddress.ip_address(host))
    except ValueError:
        if re.fullmatch(r"[0-9.]+", host):
            raise ValueError("无效的 IPv4 地址")
        host = host.encode("idna").decode("ascii")
        if len(host) > 253 or not all(re.fullmatch(r"[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?", p) for p in host.split(".")):
            raise ValueError("无效的域名或 IP")
        return host


def normalize_url(value):
    p = urlsplit(value)
    if p.scheme.lower() not in ("http", "https") or not p.hostname or p.username or p.password:
        raise ValueError("URL 必须为不含用户凭据的 HTTP(S) 地址")
    host = normalize_host(p.hostname)
    port = p.port
    if port is not None and not 1 <= port <= 65535:
        raise ValueError("无效端口")
    authority = f"[{host}]" if ":" in host else host
    if port and port != (443 if p.scheme.lower() == "https" else 80):
        authority += f":{port}"
    return urlunsplit((p.scheme.lower(), authority, p.path or "/", p.query, ""))


def normalize_target(raw):
    s = raw.strip()
    if not s or re.search(r"[\s\x00-\x1f]", s) or s.startswith("-"):
        raise ValueError("目标为空、包含空白/控制字符或以 '-' 开头")
    entry_url = None
    try:
        network = ipaddress.ip_network(s, strict=False)
        target = str(network) if "/" in s else str(network.network_address)
        kind = "CIDR" if "/" in s else "IP"
    except ValueError:
        if "/" in s and "://" not in s:
            raise ValueError("CIDR 无效；URL 请保留 http:// 或 https://")
        if "://" in s:
            entry_url = normalize_url(s)
            target = normalize_host(urlsplit(entry_url).hostname)
        elif ":" in s:
            entry_url = normalize_url("https://" + s)
            target = normalize_host(urlsplit(entry_url).hostname)
        else:
            target = normalize_host(s)
        try:
            ipaddress.ip_address(target)
            kind = "IP"
        except ValueError:
            kind = "HOST"
    return {"raw": raw, "target": target, "target_id": safe_id(target), "kind": kind, "entry_url": entry_url}


def in_scope(host, root):
    try:
        host, root = normalize_host(host), normalize_host(root)
    except (ValueError, UnicodeError):
        return False
    try:
        root_ip = ipaddress.ip_address(root)
    except ValueError:
        return host == root or host.endswith("." + root)
    try:
        return ipaddress.ip_address(host) == root_ip
    except ValueError:
        return False


def origin(url):
    p = urlsplit(normalize_url(url))
    return f"{p.scheme}://{p.netloc}"


def service_id(url):
    p = urlsplit(normalize_url(url))
    return safe_id(f"{p.hostname}--{p.scheme}-{p.port or (443 if p.scheme == 'https' else 80)}")


def tool_info(name):
    path = shutil.which(name)
    result = {"path": path, "usable": False, "version": "", "reason": "not installed"}
    if not path:
        return result
    flag = "-h" if name in ("httpx", "dnsx") else "--version"
    if name == "dig": flag = "-v"
    if name == "openssl": flag = "version"
    if name in ("subfinder", "nuclei", "katana", "shuffledns", "asnmap"):
        flag = "-version"
    try:
        p = subprocess.run([path, flag], capture_output=True, text=True, errors="replace", timeout=5)
        text = p.stdout + p.stderr
        identity = True
        if name == "httpx":
            identity = "-tech-detect" in text and "-status-code" in text and "-json" in text
        elif name == "dnsx":
            identity = "-aaaa" in text and "-json" in text
        result.update(usable=p.returncode == 0 and identity, version=text.strip().splitlines()[0][:180] if text.strip() else "", reason="" if p.returncode == 0 and identity else f"capability check failed (exit {p.returncode})")
        if result['usable'] and name in ('httpx', 'dnsx'):
            version = subprocess.run([path, '-version'], capture_output=True, text=True, timeout=5)
            value = (version.stdout + version.stderr).strip()
            result['version'] = value.splitlines()[0][:180] if version.returncode == 0 and value else 'unknown'
    except (OSError, subprocess.TimeoutExpired) as exc:
        result["reason"] = str(exc)
    return result


def terminate_tree(process):
    """Terminate the supervised group, plus descendants visible through /proc."""
    parents = {}
    try:
        entries = list(Path('/proc').iterdir())
    except OSError:
        # The managed Landlock sandbox deliberately does not grant /proc. Its
        # child supervisors cascade process-group termination without it.
        entries = []
    for entry in entries:
        if not entry.name.isdigit(): continue
        try:
            stat = (entry / 'stat').read_text().rsplit(')', 1)[1].split()
            parents[int(entry.name)] = int(stat[1])
        except (OSError, ValueError, IndexError): pass
    descendants, frontier = [], [process.pid]
    while frontier:
        next_level = [pid for pid, parent in parents.items() if parent in frontier]
        descendants.extend(next_level); frontier = next_level
    for pid in [process.pid] + descendants[::-1]:
        try:
            if os.getpgid(pid) == pid: os.killpg(pid, signal.SIGKILL)
            else: os.kill(pid, signal.SIGKILL)
        except ProcessLookupError: pass


def supervised_command(args):
    """Start a process-group supervisor before the requested command."""
    return [sys.executable, str(Path(__file__).with_name('recon_supervisor.py')), str(os.getpid()), *map(str, args)]


def run_command(args, outdir, name, *, timeout=180, stdout=None, stdin=None, env=None):
    """Log exit status and kill the complete child process group on timeout."""
    outdir = Path(outdir)
    raw = outdir / "raw"
    raw.mkdir(parents=True, exist_ok=True)
    stdout_path = Path(stdout) if stdout else raw / f"{name}.stdout"
    stderr_path = raw / f"{name}.stderr"
    started = time.monotonic()
    logged_args = list(map(str, args))
    for i, arg in enumerate(logged_args[:-1]):
        if arg in ('-t', '--token', '--api-key') and args[0] == 'github-subdomains':
            logged_args[i + 1] = '<redacted>'
        if arg == '-H' and logged_args[i + 1].lower().startswith(('authorization:', 'cookie:')):
            logged_args[i + 1] = logged_args[i + 1].split(':', 1)[0] + ': <redacted>'
    record = {"name": name, "command": logged_args, "started_utc": utc_now(), "stdout": str(stdout_path.relative_to(outdir)) if stdout_path.is_relative_to(outdir) else str(stdout_path), "stderr": str(stderr_path.relative_to(outdir))}
    process = None
    try:
        with stdout_path.open("wb") as output, stderr_path.open("wb") as error:
            process = subprocess.Popen(supervised_command(args), stdin=subprocess.PIPE if stdin is not None else subprocess.DEVNULL, stdout=output, stderr=error, env=env, start_new_session=True)
            try:
                process.communicate(input=stdin.encode() if isinstance(stdin, str) else stdin, timeout=max(0.1, timeout))
                record.update(returncode=process.returncode, status="success" if process.returncode == 0 else "failed")
            except subprocess.TimeoutExpired:
                terminate_tree(process)
                process.communicate()
                record.update(returncode=124, status="timeout")
    except OSError as exc:
        if process and process.poll() is None:
            terminate_tree(process)
            process.wait()
        record.update(returncode=127, status="failed", error=str(exc))
    except BaseException:
        if process and process.poll() is None:
            terminate_tree(process)
            process.wait()
        record.update(returncode=130, status='interrupted', duration_seconds=round(time.monotonic() - started, 3))
        with (outdir / 'commands.jsonl').open('a') as log:
            log.write(json.dumps(record, ensure_ascii=False) + '\n')
        raise
    record["duration_seconds"] = round(time.monotonic() - started, 3)
    with (outdir / "commands.jsonl").open("a") as log:
        log.write(json.dumps(record, ensure_ascii=False) + "\n")
    return record


def parse_dns(text):
    """Only typed answer fields are addresses; never mine the hostname."""
    records = []
    for line in text.splitlines():
        try:
            item = json.loads(line)
            host = item.get("host", "")
            for typ, key in (("A", "a"), ("AAAA", "aaaa")):
                values = item.get(key, [])
                for val in [values] if isinstance(values, str) else values:
                    records.append({"host": host, "type": typ, "ip": str(ipaddress.ip_address(val))})
            continue
        except (ValueError, TypeError, AttributeError):
            pass
        match = re.fullmatch(r"(\S+)\s+\[(A|AAAA)\]\s+\[?([^\[\]]+)\]?", line.strip())
        if match:
            for val in re.split(r"[,\s]+", match[3]):
                try:
                    records.append({"host": match[1], "type": match[2], "ip": str(ipaddress.ip_address(val))})
                except ValueError:
                    pass
    return records


def parse_grepable(text):
    records = []
    for line in text.splitlines():
        host = re.search(r"\bHost:\s*([0-9a-fA-F:.]+)", line)
        ports = re.search(r"\bPorts:\s*(.*?)(?:\t|$)", line)
        if not host or not ports:
            continue
        for m in re.finditer(r"(?:^|,\s*)(\d+)/(open)/(tcp|udp)/[^,]*", ports[1]):
            if 1 <= int(m[1]) <= 65535:
                records.append({"ip": str(ipaddress.ip_address(host[1])), "port": int(m[1]), "protocol": m[3], "state": "open", "service": "", "version": ""})
    return records


def parse_nmap_xml(path):
    records = []
    tree = ET.parse(path)
    for host in tree.findall("host"):
        addresses = [a.get("addr") for a in host.findall("address") if a.get("addrtype") in ("ipv4", "ipv6")]
        for port in host.findall("ports/port"):
            state = port.find("state")
            if state is None or state.get("state") != "open":
                continue
            service = port.find("service")
            attrs = service.attrib if service is not None else {}
            for address in addresses:
                records.append({"ip": str(ipaddress.ip_address(address)), "port": int(port.get("portid")), "protocol": port.get("protocol", "tcp"), "state": "open", "service": attrs.get("name", ""), "version": " ".join(attrs.get(k, "") for k in ("product", "version", "extrainfo")).strip()})
    return records
