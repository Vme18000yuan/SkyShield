#!/usr/bin/env python3
"""Exercise AI tools in an isolated Server image, without external targets.

Usage: python3 scripts/test_server_image_tools.py IMAGE
The test uses Docker's default security profile and an offline network namespace.
"""

import json
import queue
import subprocess
import sys
import tempfile
import threading
import uuid


def main(image):
    name = "skyshield-ai-tools-test-" + uuid.uuid4().hex[:12]
    root = "/tmp/skyshield-work"
    setup = (
        "set -eu; test ! -e /etc/chromium.d/apikeys; mkdir -p /tmp/skyshield-work/.tmp; "
        "cp -a /usr/local/share/lunafox/skills/info-gath /tmp/skyshield-work/skill; "
        "cp -a /usr/local/share/lunafox/skills/api-recon /tmp/skyshield-work/api-skill; "
        "export HOME=/tmp/skyshield-work TMPDIR=/tmp/skyshield-work/.tmp "
        "NORMA_TASK_OUTPUT_ROOT=/tmp/skyshield-work/.task-output "
        "PATH=/usr/local/bin:/usr/bin:/bin LANG=C.UTF-8 "
        "NODE_PATH=/usr/local/lib/skyshield-ai-node/node_modules SKYSHIELD_AI_SANDBOX=1; "
        "exec /usr/local/bin/server ai-tool-worker --root /tmp/skyshield-work"
    )
    lines = queue.Queue()
    with tempfile.TemporaryFile(mode="w+t") as errors:
        process = subprocess.Popen(
            ["docker", "run", "--rm", "-i", "--network", "none", "--name", name,
             "--entrypoint", "/bin/sh", image, "-c", setup],
            stdin=subprocess.PIPE, stdout=subprocess.PIPE, stderr=errors,
            text=True, bufsize=1,
        )

        def read_stdout():
            for line in process.stdout:
                lines.put(line)
            lines.put(None)

        threading.Thread(target=read_stdout, daemon=True).start()

        def receive():
            try:
                line = lines.get(timeout=150)
            except queue.Empty:
                raise RuntimeError("AI worker response timed out") from None
            if line is None:
                errors.seek(0)
                raise RuntimeError("AI worker exited: " + errors.read()[-4000:])
            return json.loads(line)

        def call(tool, payload):
            request = {"id": uuid.uuid4().hex, "name": tool, "input": payload,
                       "working_dir": root, "output_dir": root + "/.task-output",
                       "max_output_chars": 100000}
            process.stdin.write(json.dumps(request) + "\n")
            process.stdin.flush()
            while True:
                response = receive()
                if response.get("id") == request["id"] and "progress" not in response:
                    break
            result = response.get("result") or {}
            content = "".join(block.get("text", "") for block in result.get("Content", []))
            if response.get("error") or result.get("IsError"):
                raise RuntimeError(f"{tool} failed: {response.get('error', '')} {content}")
            return content

        try:
            if not receive().get("ready"):
                raise RuntimeError("AI worker did not become ready")

            call("Write", {"file_path": "probe.txt", "content": "skyshield-sandbox-fixture\n"})
            assert "skyshield-sandbox-fixture" in call("Read", {"file_path": "probe.txt"})
            assert "probe.txt" in call("Grep", {"pattern": "skyshield-sandbox-fixture"})

            command = """set -eu
for tool in bash python3 pip3 node npm npx chromium playwright-cli playwright-mcp curl git rg jq dig whois openssl nmap masscan subfinder dnsx httpx katana nuclei ffuf; do
    command -v "$tool" >/dev/null || { echo "missing: $tool"; exit 1; }
done
python3 -c 'import requests, dns, mmh3, tldextract, tqdm; print("python-imports-ok")'
python3 skill/scripts/waf_detect.py --help >/dev/null
python3 skill/scripts/check_tools.py --require curl dig whois openssl nmap masscan subfinder dnsx httpx katana nuclei ffuf
node --version
npm --version
npx --version
node -e 'require("puppeteer-core"); require("@playwright/mcp/package.json"); console.log("node-imports-ok")'
echo sandbox-tools-ok
"""
            output = call("Bash", {"command": command, "timeout_ms": 120000})
            if "python-imports-ok" not in output or "sandbox-tools-ok" not in output:
                raise RuntimeError("tool verification output incomplete: " + output)
            print("AI sandbox: file tools, CLI capabilities, Python imports and Node/npm/npx passed")

            call("Bash", {"command": "mkdir -p www/api"})
            call("Write", {"file_path": "www/index.html", "content": '<html>skyshield-http-fixture<script>fetch("/api/fixture")</script></html>\n'})
            call("Write", {"file_path": "www/api/fixture", "content": '{"fixture":true}\n'})
            call("Write", {"file_path": "words.txt", "content": "index.html\n"})
            call("Write", {"file_path": "browser-fixture.json", "content": json.dumps({
                "baseUrl": "http://127.0.0.1:18080/", "waitMs": 1,
                "perRouteMs": 1, "routes": ["/"], "recordWs": False,
            })})
            call("Write", {"file_path": "fixture.yaml", "content": """id: skyshield-fixture
info:
  name: Local test fixture
  author: skyshield
  severity: info
http:
  - method: GET
    path:
      - "{{BaseURL}}/"
    matchers:
      - type: word
        words:
          - "skyshield-http-fixture"
"""})
            call("Write", {"file_path": "fixture_proxy.py", "content": """import base64
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from urllib.parse import urlsplit
from urllib.request import build_opener, ProxyHandler

expected = 'Basic ' + base64.b64encode(b'fixture:fixture').decode()
opener = build_opener(ProxyHandler({}))
class Proxy(BaseHTTPRequestHandler):
    def do_GET(self):
        if self.headers.get('Proxy-Authorization') != expected:
            self.send_response(407)
            self.send_header('Proxy-Authenticate', 'Basic realm="fixture"')
            self.end_headers()
            return
        target = urlsplit(self.path)
        if target.hostname != '127.0.0.1' or target.port != 18080:
            self.send_error(403)
            return
        with opener.open(self.path, timeout=5) as response:
            body = response.read()
            self.send_response(response.status)
            self.send_header('Content-Type', response.headers.get('Content-Type', 'text/plain'))
            self.send_header('Content-Length', str(len(body)))
            self.end_headers()
            self.wfile.write(body)
        with open('proxy-requests.log', 'a') as log:
            log.write(target.path + '\\n')
ThreadingHTTPServer(('127.0.0.1', 18081), Proxy).serve_forever()
"""})
            fixture = """set -eux
python3 -m http.server 18080 --bind 127.0.0.1 --directory www >fixture-server.log 2>&1 &
pid=$!
trap 'cat fixture-server.log; kill "$pid" 2>/dev/null || true' EXIT
count=0
until curl -fsS http://127.0.0.1:18080/ >/dev/null 2>&1; do
    count=$((count + 1)); test "$count" -lt 30; sleep 0.1
done
httpx -u http://127.0.0.1:18080/ -silent -duc -json | python3 -c 'import json,sys; assert json.load(sys.stdin)["status_code"] == 200'
ffuf -w words.txt -u http://127.0.0.1:18080/FUZZ -mc 200 -s -noninteractive | rg -q '^index.html$'
nuclei -u http://127.0.0.1:18080/ -t fixture.yaml -duc -silent -no-color | rg -q '\\[skyshield-fixture\\]'
node -e 'const p=require("puppeteer-core"); (async()=>{let b=await p.launch({executablePath:"/usr/lib/chromium/chromium",args:["--no-sandbox","--single-process","--no-zygote","--disable-gpu","--disable-dev-shm-usage"]}); let page=await b.newPage(); await page.goto("http://127.0.0.1:18080/"); if(!(await page.content()).includes("skyshield-http-fixture")) throw Error("browser fixture missing"); await b.close(); console.log("browser-fixture-ok");})().catch(e=>{console.error(e);process.exit(1)})'
node api-skill/scripts/runtime_harvest.js browser-fixture.json
python3 -c 'import json; assert json.load(open("runtime_api.json"))["shell"]["url"].startswith("http://127.0.0.1:18080/")'
playwright-cli open http://127.0.0.1:18080/
playwright-cli snapshot | rg -q 'skyshield-http-fixture'
playwright-cli close
python3 fixture_proxy.py >fixture-proxy.log 2>&1 &
proxy_pid=$!
trap 'cat fixture-server.log; cat fixture-proxy.log; kill "$pid" "$proxy_pid" 2>/dev/null || true' EXIT
count=0
until curl -fsS --proxy http://fixture:fixture@127.0.0.1:18081 --noproxy '' http://127.0.0.1:18080/ >/dev/null 2>&1; do
    count=$((count + 1)); test "$count" -lt 30; sleep 0.1
done
HTTP_PROXY=http://fixture:fixture@127.0.0.1:18081 HTTPS_PROXY=http://fixture:fixture@127.0.0.1:18081 NO_PROXY='' NODE_USE_ENV_PROXY=1 node api-skill/scripts/runtime_harvest.js browser-fixture.json
python3 -c 'import json; assert any(row["u"] == "/api/fixture" for row in json.load(open("runtime_api.json"))["rec"])'
HTTP_PROXY=http://fixture:fixture@127.0.0.1:18081 HTTPS_PROXY=http://fixture:fixture@127.0.0.1:18081 NO_PROXY='' playwright-cli open http://127.0.0.1:18080/
playwright-cli snapshot | rg -q 'skyshield-http-fixture'
playwright-cli close
rg -q '^/api/fixture$' proxy-requests.log
echo local-http-tools-ok
"""
            if "local-http-tools-ok" not in call("Bash", {"command": fixture, "timeout_ms": 120000}):
                raise RuntimeError("local HTTP tool verification output incomplete")
            print("AI sandbox: httpx, ffuf, Nuclei, Chromium and authenticated proxy passed against local fixtures")
        finally:
            if process.stdin:
                process.stdin.close()
            try:
                process.wait(timeout=10)
            except subprocess.TimeoutExpired:
                subprocess.run(["docker", "rm", "-f", name], stdout=subprocess.DEVNULL,
                               stderr=subprocess.DEVNULL, check=False)
                process.wait(timeout=10)


if __name__ == "__main__":
    if len(sys.argv) != 2:
        raise SystemExit("Usage: python3 scripts/test_server_image_tools.py IMAGE")
    main(sys.argv[1])
