#!/usr/bin/env python3
"""Verify DNS/hosts/HTTP inheritance and Docker child creates using local fixtures."""
import argparse
import http.server
import importlib.util
import json
import os
import pathlib
import secrets
import subprocess
import tempfile
import threading
import time
import uuid


def call(args, check=True):
    result = subprocess.run([str(value) for value in args], text=True, capture_output=True)
    if check and result.returncode:
        raise RuntimeError('Container network verification failed: ' + result.stderr[-1800:])
    return result.stdout.strip() + (('\n' + result.stderr.strip()) if not check and result.stderr else '')


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument('--helper-image', required=True)
    parser.add_argument('--probe-image', default='solder110/skyshield-server:latest')
    parser.add_argument('--helper-binary', type=pathlib.Path)
    parser.add_argument('--mode', choices=['host', 'bridge'], default='host')
    args = parser.parse_args()
    for image in (args.helper_image, args.probe_image):
        call(['docker', 'image', 'inspect', image])
    spec = importlib.util.spec_from_file_location('deployment_network', pathlib.Path(__file__).with_name('deployment-network.py'))
    network_module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(network_module)
    subnet = network_module.choose_subnet(network_module.host_routes())
    prefix = 'skyshield-netcheck-' + uuid.uuid4().hex[:12]
    network = prefix + '_default'
    volume, helper, probe, client, store = [prefix + '-' + suffix for suffix in ('sock', 'helper', 'probe', 'client', 'store')]

    class Fixture(http.server.BaseHTTPRequestHandler):
        def do_GET(self):
            if self.headers.get('Proxy-Authorization'):
                self.send_error(500)
                return
            data = b'private-host-network-fixture'
            self.send_response(200)
            self.send_header('Content-Length', str(len(data)))
            self.end_headers()
            self.wfile.write(data)

        def log_message(self, *values):
            pass

    server = http.server.ThreadingHTTPServer(('127.0.0.1', 0), Fixture)
    thread = threading.Thread(target=server.serve_forever, daemon=True)
    thread.start()
    with tempfile.TemporaryDirectory(prefix='skyshield-host-network-fixture-') as temporary:
        root = pathlib.Path(temporary)
        try:
            call(['docker', 'network', 'create', '--label', 'org.skyshield.network-test=true', '--subnet', subnet, network])
            info = json.loads(call(['docker', 'network', 'inspect', network]))[0]
            gateway = info['IPAM']['Config'][0]['Gateway']
            default_bridge = json.loads(call(['docker', 'network', 'inspect', 'bridge']))[0]
            allowed = [subnet] + [item['Subnet'] for item in default_bridge['IPAM']['Config'] if item.get('Subnet')]
            allowed += [value for value in network_module.host_addresses() if value not in allowed]
            call(['docker', 'run', '-d', '--rm', '--name', store, '--network', network,
                  '--label', 'org.skyshield.network-test=true', '--label', 'com.docker.compose.project=' + prefix,
                  '--label', 'com.docker.compose.service=postgres', '--entrypoint', 'python3', args.probe_image,
                  '-m', 'http.server', '15432', '--bind', '0.0.0.0'])
            store_info = json.loads(call(['docker', 'inspect', store]))[0]
            store_ip = store_info['NetworkSettings']['Networks'][network]['IPAddress']
            hosts, resolv = root / 'hosts', root / 'resolv.conf'
            hosts.write_text('127.0.0.1 host.network.fixture\n10.31.47.9 private.network.fixture\n')
            resolv.write_text('nameserver 127.0.0.1\n')
            config = dict(bindIP=gateway, dnsPort=53, proxyPort=31280, allowedCIDRs=allowed,
                          resolvConf='/network/resolv.conf', hostsFile='/network/hosts',
                          dockerSocket='/run/skyshield-network/docker.sock', dockerUpstream='/var/run/docker-host.sock',
                          proxyToken=secrets.token_hex(32), proxyEnvironment={}, searchDomains=[], engineNetworkMode=args.mode, projectName=prefix)
            config_path = root / 'config.json'
            config_path.write_text(json.dumps(config)); config_path.chmod(0o600)
            call(['docker', 'volume', 'create', '--label', 'org.skyshield.network-test=true', volume])
            binary_mount = ['-v', str(args.helper_binary.resolve()) + ':/usr/local/bin/skyshield-network-helper:ro'] if args.helper_binary else []
            call(['docker', 'run', '-d', '--rm', '--name', helper, '--label', 'org.skyshield.network-test=true', '--network', 'host', '--read-only', *binary_mount,
                  '-v', str(root) + ':/network:ro', '-v', '/var/run/docker.sock:/var/run/docker-host.sock',
                  '-v', volume + ':/run/skyshield-network', '--entrypoint', '/usr/local/bin/skyshield-network-helper', args.helper_image,
                  '--config', '/network/config.json'])
            for attempt in range(60):
                result = subprocess.run(['docker', 'exec', helper, '/usr/local/bin/skyshield-network-helper', '--health-socket', '/run/skyshield-network/docker.sock'], capture_output=True)
                if result.returncode == 0:
                    break
                time.sleep(.25)
            else:
                raise RuntimeError('Network helper did not start: ' + call(['docker', 'logs', helper], check=False)[-1000:])
            for name in ('context.pb', 'token'):
                (root / name).write_text('local-fixture-only')
            (root / 'socket').mkdir()
            program = """import socket, urllib.request
assert socket.gethostbyname('private.network.fixture') == '10.31.47.9'
assert socket.gethostbyname('postgres') == 'STORE_IP'
with socket.create_connection(('postgres', 15432), timeout=5):
 pass
with urllib.request.urlopen('http://host.network.fixture:PORT/', timeout=5) as response:
 assert response.read() == b'private-host-network-fixture'
print('dynamic-engine-host-network-ok')
""".replace('PORT', str(server.server_port)).replace('STORE_IP', store_ip)

            def docker_client(*values):
                return call(['docker', 'run', '--rm', '--name', client, '--label', 'org.skyshield.network-test=true',
                             '-e', 'DOCKER_HOST=unix:///run/skyshield-network/docker.sock',
                             '-v', volume + ':/run/skyshield-network:ro', '--entrypoint', 'docker', args.helper_image, *values])

            mounts = []
            for source, target in [('context.pb', '/run/lunafox/context/execution.pb'), ('token', '/run/lunafox/credential/token'), ('socket', '/run/lunafox/socket')]:
                mounts += ['--mount', 'type=bind,source=' + str(root / source) + ',target=' + target + ',readonly']
            docker_client('create', '--name', probe, '--label', 'org.skyshield.network-test=true', '--dns-option', 'timeout:1', '--dns-option', 'attempts:1', *mounts,
                          '--entrypoint', 'python3', args.probe_image, '-c', program)
            created = json.loads(call(['docker', 'inspect', probe]))[0]
            if created['HostConfig']['Dns'] != [gateway]:
                raise RuntimeError('Dynamic child container did not receive inherited DNS')
            if args.mode == 'host' and created['HostConfig']['NetworkMode'] != 'host':
                raise RuntimeError('Host execution network mode was not inherited')
            if args.mode == 'bridge' and created['HostConfig']['NetworkMode'] not in ('default', 'bridge'):
                raise RuntimeError('Explicit bridge network mode changed')
            output = docker_client('start', '--attach', probe)
            if 'dynamic-engine-host-network-ok' not in output:
                raise RuntimeError('Child DNS/host HTTP verification did not finish')
            state = json.loads(call(['docker', 'inspect', probe]))[0]['State']
            if state['ExitCode'] != 0:
                raise RuntimeError('Child network fixture failed')
            entry_address = '127.0.0.1' if args.mode == 'host' else gateway
            bridge_server = http.server.ThreadingHTTPServer((entry_address, 0), Fixture)
            bridge_thread = threading.Thread(target=bridge_server.serve_forever, daemon=True)
            bridge_thread.start()
            try:
                program = "import urllib.request; o=urllib.request.build_opener(urllib.request.ProxyHandler({})); r=o.open('http://IP:PORT/',timeout=5); assert r.read()==b'private-host-network-fixture'; print('bridge-entry-host-tcp-ok')"
                program = program.replace('IP:PORT', entry_address + ':' + str(bridge_server.server_port))
                entry_network = 'host' if args.mode == 'host' else network
                output = call(['docker', 'run', '--rm', '--name', client, '--network', entry_network, '--label', 'org.skyshield.network-test=true',
                               '--entrypoint', 'python3', args.probe_image, '-c', program])
                if 'bridge-entry-host-tcp-ok' not in output:raise RuntimeError('Bridge entry could not reach host execution service')
            finally:
                bridge_server.shutdown(); bridge_server.server_close(); bridge_thread.join(timeout=3)
            print('Container verification passed: host DNS/hosts, host-loopback HTTP, private service DNS/TCP, entry service TCP, dynamic Engine create and Docker attach stream')
        except Exception:
            print('Network helper fixture log:', call(['docker', 'logs', helper], check=False)[-1000:], flush=True)
            hold = min(int(os.environ.get('SKYSHIELD_NETWORK_TEST_HOLD', '0')), 60)
            if hold:
                print('Isolated network fixture retained temporarily:', prefix, gateway, flush=True)
                time.sleep(hold)
            if 'gateway' in locals():
                diagnostic = subprocess.run(['docker', 'exec', helper, 'nslookup', 'private.network.fixture', gateway], text=True, capture_output=True, timeout=15)
                print('Host namespace DNS fixture diagnostic:', diagnostic.stdout[-1200:] + diagnostic.stderr[-500:])
                diagnostic = subprocess.run(['docker', 'run', '--rm', '--label', 'org.skyshield.network-test=true', '--dns', gateway,
                                             '--entrypoint', 'sh', args.probe_image, '-c',
                                             'cat /etc/resolv.conf; dig +time=2 +tries=1 @' + gateway + ' private.network.fixture; dig +tcp +time=2 +tries=1 @' + gateway + ' private.network.fixture'],
                                            text=True, capture_output=True, timeout=15)
                print('Bridge DNS fixture diagnostic:', diagnostic.stdout[-2500:] + diagnostic.stderr[-500:])
            raise
        finally:
            for name in (client, probe, helper, store):
                call(['docker', 'rm', '-f', name], check=False)
            call(['docker', 'volume', 'rm', volume], check=False)
            call(['docker', 'network', 'rm', network], check=False)
            server.shutdown(); server.server_close(); thread.join(timeout=3)


if __name__ == '__main__':
    main()
