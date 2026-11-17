#!/usr/bin/env python3
"""Project-scoped host network adapter; never edits daemon.json or host DNS."""
import argparse
import hashlib
import ipaddress
import json
import os
from pathlib import Path
import secrets
import shlex
import shutil
import socket
import stat
import subprocess
import sys
import tempfile
import urllib.parse

PROXY_KEYS = ('HTTP_PROXY', 'HTTPS_PROXY', 'ALL_PROXY', 'NO_PROXY', 'http_proxy', 'https_proxy', 'all_proxy', 'no_proxy')
INTERNAL_NO_PROXY = 'localhost,127.0.0.1,::1,server,frontend,postgres,redis,loki,nginx,network-helper'


def docker(*args):
    result = subprocess.run(['docker', *args], text=True, capture_output=True)
    if result.returncode:
        raise RuntimeError('Docker 网络操作失败：' + ' '.join(args[:2]) + '；请检查 Docker 服务及权限。')
    return result.stdout.strip()


def settings(root):
    result = {}
    path = root / '.env'
    if path.exists():
        for line in path.read_text().splitlines():
            line = line.strip()
            if not line or line.startswith('#'):
                continue
            if line.startswith('export '):
                line = line[7:]
            key, separator, raw = line.partition('=')
            key = key.strip()
            if separator and (key in PROXY_KEYS or key in ('PUBLIC_HOST', 'PUBLIC_PORT') or key.startswith('SKYSHIELD_')):
                values = shlex.split(raw, comments=True)
                result[key] = ' '.join(values)
    result.update(os.environ)
    return result


def private_write(path, value):
    if path.is_symlink() or (path.exists() and not path.is_file()):
        raise RuntimeError('网络配置路径不是普通文件：' + str(path))
    fd, name = tempfile.mkstemp(prefix='.' + path.name + '-', dir=path.parent)
    temporary = Path(name)
    try:
        with os.fdopen(fd, 'w') as output:
            output.write(value)
            output.flush()
            os.fsync(output.fileno())
        temporary.replace(path)
    finally:
        temporary.unlink(missing_ok=True)


def write_overlay(path, overlay):
    private_write(path.with_suffix('.json'), json.dumps(overlay, ensure_ascii=False) + '\n')
    lines = ['services:']
    for name, service in overlay['services'].items():
        lines.append('  ' + json.dumps(name) + ':')
        for key, value in service.items():
            rendered = '!reset []' if key == 'ports' and service.get('network_mode') == 'host' else json.dumps(value, ensure_ascii=False)
            lines.append('    ' + json.dumps(key) + ': ' + rendered)
    for key in ('networks', 'volumes'):
        lines.append(key + ': ' + json.dumps(overlay[key], ensure_ascii=False))
    private_write(path, '\n'.join(lines) + '\n')


def resolver_search(path):
    servers, search = [], []
    for line in path.read_text().splitlines():
        fields = line.split('#', 1)[0].split()
        if not fields:
            continue
        if fields[0] == 'nameserver' and len(fields) > 1:
            ip = ipaddress.ip_address(fields[1].split('%', 1)[0])
            if ip.is_unspecified or ip.is_multicast:
                raise RuntimeError('宿主机 DNS 地址无效。')
            if fields[1] not in servers:
                servers.append(fields[1])
        if fields[0] in ('search', 'domain'):
            for domain in fields[1:]:
                domain = domain.lstrip('~').rstrip('.')
                if domain and domain not in search:
                    search.append(domain)
    if not servers:
        raise RuntimeError('宿主机没有配置 DNS；请先让运行 Docker 的虚拟机能够解析内网域名。')
    return servers, search


def host_resolv_conf(env, stub=Path('/run/systemd/resolve/stub-resolv.conf'), fallback=Path('/etc/resolv.conf')):
    if env.get('SKYSHIELD_HOST_RESOLV_CONF'):
        path = Path(env['SKYSHIELD_HOST_RESOLV_CONF']).expanduser()
        if not path.is_absolute():
            raise RuntimeError('SKYSHIELD_HOST_RESOLV_CONF 必须是绝对路径。')
        return path.resolve()
    # systemd-resolved's upstream file loses per-link VPN split DNS. Its local
    # stub preserves routing domains, so prefer it when the resolver is active.
    if stub.is_file():
        try:
            with socket.create_connection(('127.0.0.53', 53), timeout=.3):
                return stub.resolve()
        except OSError:
            pass
    return fallback.resolve()


def network_info(name):
    result = subprocess.run(['docker', 'network', 'inspect', name], text=True, capture_output=True)
    if result.returncode:
        return None
    entries = json.loads(result.stdout)
    if len(entries) != 1:
        raise RuntimeError('无法确认 Docker 网络身份。')
    return entries[0]


def docker_socket(env):
    endpoint = env.get('DOCKER_HOST', '') if not env.get('DOCKER_CONTEXT') else ''
    if not endpoint:
        endpoint = docker('context', 'inspect', '--format', '{{.Endpoints.docker.Host}}')
    parsed = urllib.parse.urlsplit(endpoint)
    if parsed.scheme != 'unix' or parsed.netloc or not parsed.path.startswith('/') or parsed.query or parsed.fragment:
        raise RuntimeError('网络继承不支持远程 Docker daemon；请在 Docker 所在的 Linux 主机/虚拟机上运行脚本。')
    path = Path(parsed.path).resolve()
    if not stat.S_ISSOCK(path.stat().st_mode):
        raise RuntimeError('当前 Docker context 没有可用的本地 Unix socket。')
    return path


def host_routes():
    if not shutil.which('ip'):
        return []
    result = subprocess.run(['ip', '-j', 'route', 'show', 'table', 'all'], text=True, capture_output=True)
    if result.returncode:
        return []
    routes = []
    for entry in json.loads(result.stdout):
        destination = entry.get('dst')
        if not destination or destination == 'default':
            continue
        try:
            network = ipaddress.ip_network(destination, strict=False)
        except ValueError:
            continue
        if network.version == 4 and network.prefixlen > 1:
            routes.append((network, entry.get('dev', '')))
    return routes


def host_addresses():
    # VPN/TUN clients can SNAT a Docker or local connection to one of the
    # machine's own interface addresses. Permit those exact addresses, not LANs.
    result = ['127.0.0.0/8', '::1/128']
    if shutil.which('ip'):
        process = subprocess.run(['ip', '-j', 'address', 'show'], text=True, capture_output=True)
        if process.returncode == 0:
            for interface in json.loads(process.stdout):
                for entry in interface.get('addr_info', []):
                    try:
                        ip = ipaddress.ip_address(entry['local'])
                    except (KeyError, ValueError):
                        continue
                    if not ip.is_unspecified and not ip.is_multicast:
                        value = str(ip) + ('/32' if ip.version == 4 else '/128')
                        if value not in result:
                            result.append(value)
    return result


def choose_subnet(routes, explicit=None):
    choices = [explicit] if explicit else ['172.30.240.0/24', '172.31.240.0/24', '10.253.240.0/24', '192.168.240.0/24', '100.127.240.0/24']
    for value in choices:
        candidate = ipaddress.ip_network(value, strict=True)
        address = candidate.network_address
        if candidate.version != 4 or not 24 <= candidate.prefixlen <= 28 or candidate.is_global or address.is_loopback or address.is_link_local or address.is_multicast or address.is_unspecified or address.is_reserved:
            raise RuntimeError('SKYSHIELD_DOCKER_SUBNET 必须是非公网的 IPv4 /24 或更小网段。')
        if not any(candidate.overlaps(route) for route, _ in routes):
            return str(candidate)
    raise RuntimeError('没有找到与部署机路由不冲突的 Docker 网段；请配置 SKYSHIELD_DOCKER_SUBNET。')


def prepare(root, hub=False):
    env = settings(root)
    destination = root / '.local/network'
    overlay_path = destination / 'compose.network.yaml'
    enabled = env.get('SKYSHIELD_INHERIT_HOST_NETWORK', '1')
    if enabled not in ('0', '1'):
        raise RuntimeError('SKYSHIELD_INHERIT_HOST_NETWORK 只能是 0 或 1。')
    if enabled == '0':
        if overlay_path.is_symlink():
            raise RuntimeError('网络配置不能是符号链接。')
        overlay_path.unlink(missing_ok=True)
        overlay_path.with_suffix('.json').unlink(missing_ok=True)
        return None
    if sys.platform != 'linux':
        raise RuntimeError('宿主网络继承需要在运行 Docker Engine 的 Linux 主机/虚拟机内执行。')
    upstream_socket = docker_socket(env)
    mode = env.get('SKYSHIELD_NETWORK_MODE', 'host')
    if mode not in ('bridge', 'host'):
        raise RuntimeError('SKYSHIELD_NETWORK_MODE 只能是 bridge 或 host。')
    resolv = host_resolv_conf(env)
    hosts = Path('/etc/hosts').resolve()
    servers, search = resolver_search(resolv)
    compose_args = ['compose', '-f', str(root / 'compose.tianqiong.yaml')]
    if hub:
        compose_args += ['-f', str(root / 'compose.hub.yaml')]
    projection = json.loads(docker(*compose_args, 'config', '--format', 'json'))
    project = projection.get('name', 'tianqiong')
    if project != 'tianqiong':
        raise RuntimeError('当前固定数据卷部署需要 Compose 项目名 tianqiong。')
    network_name = project + '_default'
    network = network_info(network_name)
    if network is None:
        docker('network', 'create', '--label', 'com.docker.compose.project=' + project,
               '--label', 'com.docker.compose.network=default', '--subnet', choose_subnet(host_routes(), env.get('SKYSHIELD_DOCKER_SUBNET')), network_name)
        network = network_info(network_name)
    if network is None or network.get('Driver') != 'bridge' or network.get('Labels', {}).get('com.docker.compose.project') != project:
        raise RuntimeError('现有 Docker 网络不属于当前项目；未修改其网络配置。')
    ipv4 = [item for item in network.get('IPAM', {}).get('Config', [])
            if item.get('Gateway') and ipaddress.ip_address(item['Gateway']).version == 4]
    if len(ipv4) != 1:
        raise RuntimeError('项目网络需要一个可确认的 IPv4 网关。')
    bind_ip, subnet = ipv4[0]['Gateway'], ipv4[0]['Subnet']
    own_interface = 'br-' + network['Id'][:12]
    conflicts = [str(route) for route, device in host_routes()
                 if device != own_interface and ipaddress.ip_network(subnet).overlaps(route)]
    if conflicts:
        print('警告：现有项目 Docker 网段 ' + subnet + ' 与部署机路由冲突：' + ', '.join(conflicts) + '。现有网络已保留，需要先处理网段冲突才能完整访问对应内网。', file=sys.stderr)
    allowed = [subnet]
    default_bridge = network_info('bridge')
    if default_bridge:
        allowed += [item['Subnet'] for item in default_bridge.get('IPAM', {}).get('Config', [])
                    if item.get('Subnet') and item['Subnet'] not in allowed]
    allowed += [value for value in host_addresses() if value not in allowed]
    if (root / '.local').is_symlink() or destination.is_symlink():
        raise RuntimeError('网络配置目录不能是符号链接。')
    destination.mkdir(parents=True, exist_ok=True, mode=0o700)
    if destination.is_symlink() or not destination.is_dir():
        raise RuntimeError('网络配置目录必须是普通目录。')
    destination.chmod(0o700)
    token_path = destination / 'proxy-token'
    if token_path.is_symlink():
        raise RuntimeError('网络适配凭据不能是符号链接。')
    if token_path.exists():
        if stat.S_IMODE(token_path.stat().st_mode) != 0o600:
            raise RuntimeError('网络适配凭据权限必须为 0600。')
        token = token_path.read_text().strip()
    else:
        token = secrets.token_hex(32)
        private_write(token_path, token + '\n')
    if len(token) != 64 or any(char not in '0123456789abcdef' for char in token):
        raise RuntimeError('网络适配凭据已损坏；原文件已保留，请检查后修复。')
    config = dict(bindIP=bind_ip, dnsPort=53, proxyPort=31280, allowedCIDRs=allowed,
                  resolvConf='/etc/resolv.conf', hostsFile='/etc/hosts',
                  dockerSocket='/run/skyshield-network/docker.sock', dockerUpstream='/var/run/docker-host.sock',
                  proxyToken=token, proxyEnvironment={key: env[key] for key in PROXY_KEYS if env.get(key)},
                  searchDomains=search, engineNetworkMode=mode, projectName=project)
    config_path = destination / 'config.json'
    private_write(config_path, json.dumps(config, ensure_ascii=False) + '\n')
    resolver_path = destination / 'resolvers.txt'
    private_write(resolver_path, bind_ip + '\n')
    proxy = 'http://skyshield:' + token + '@' + bind_ip + ':31280'
    inherited = {key: proxy for key in ('HTTP_PROXY', 'HTTPS_PROXY', 'ALL_PROXY', 'http_proxy', 'https_proxy', 'all_proxy')}
    inherited.update(NO_PROXY=INTERNAL_NO_PROXY, no_proxy=INTERNAL_NO_PROXY, NODE_USE_ENV_PROXY='1')
    service = projection['services']['bootstrap']
    helper = dict(image=service['image'], build=service['build'], network_mode='host', read_only=True,
                  entrypoint=['/usr/local/bin/skyshield-network-helper'], command=['--config', '/network/config.json'],
                  volumes=[str(config_path) + ':/network/config.json:ro', str(resolv) + ':/etc/resolv.conf:ro',
                           str(hosts) + ':/etc/hosts:ro', str(upstream_socket) + ':/var/run/docker-host.sock',
                           'network_socket:/run/skyshield-network'],
                  healthcheck=dict(test=['CMD', '/usr/local/bin/skyshield-network-helper', '--health-socket', '/run/skyshield-network/docker.sock'],
                                   interval='3s', timeout='3s', retries=20), restart='unless-stopped')
    # Bind-mounted files and environment snapshots are refreshed when network
    # settings change, even if the selected application image tag stays fixed.
    network_hash = hashlib.sha256(config_path.read_bytes() + resolv.read_bytes() + hosts.read_bytes()).hexdigest()
    helper['labels'] = {'org.skyshield.network.configuration': network_hash}
    services = {'network-helper': helper,
                'volume-preflight': {'volumes': [str(upstream_socket) + ':/var/run/docker.sock:ro']}}
    for name in ('migrate', 'bootstrap', 'server'):
        override = dict(dns=[bind_ip], dns_search=search, environment=inherited.copy(),
                        depends_on={'network-helper': {'condition': 'service_healthy'}})
        if name == 'bootstrap':
            override['environment'].update(SKYSHIELD_SYSTEM_DNS='1', SKYSHIELD_SYSTEM_RESOLVERS_FILE='/network/resolvers.txt')
            override['volumes'] = [str(resolver_path) + ':/network/resolvers.txt:ro']
        if mode == 'host':
            override['network_mode'] = 'host'
            override['dns_opt'] = ['ndots:0']
        services[name] = override
    services['agent'] = dict(dns=[bind_ip], dns_search=search,
                             volumes=[str(upstream_socket) + ':/var/run/docker.sock', 'network_socket:/run/skyshield-network:ro'],
                             environment={'DOCKER_HOST': 'unix:///run/skyshield-network/docker.sock'},
                             depends_on={'network-helper': {'condition': 'service_healthy'}})
    if mode == 'host':
        services['agent']['network_mode'] = 'host'
        services['agent']['dns_opt'] = ['ndots:0']
        services['agent']['environment']['AGENT_CONTROL_URL'] = 'http://' + bind_ip + ':9090'
        services['frontend'] = dict(network_mode='host', dns=[bind_ip], dns_search=search, dns_opt=['ndots:0'],
                                    environment={**inherited, 'API_HOST': '127.0.0.1'})
        nginx = (root / 'docker/nginx/nginx.conf').read_text()
        if 'server server:8080;' not in nginx or 'server server:9090 resolve;' not in nginx:
            raise RuntimeError('Nginx 上游配置与宿主网络模板不匹配，请检查当前源码版本。')
        public_port = int(env.get('PUBLIC_PORT', '8443'))
        if not 1 <= public_port <= 65535 or public_port in (3000, 8080, 9090):
            raise RuntimeError('宿主网络模式的 PUBLIC_PORT 必须与执行组件的 3000/8080/9090 端口不同。')
        nginx = nginx.replace('server server:8080;', 'server 127.0.0.1:8080;')
        nginx = nginx.replace('server server:9090 resolve;', 'server 127.0.0.1:9090;')
        nginx = nginx.replace('server frontend:3000 resolve;', 'server 127.0.0.1:3000;')
        nginx = nginx.replace('resolver 127.0.0.11 ', 'resolver ' + bind_ip + ' ')
        if 'listen 443 ssl;' not in nginx:
            raise RuntimeError('Nginx TLS 监听配置与宿主网络模板不匹配。')
        nginx = nginx.replace('listen 443 ssl;', 'listen ' + str(public_port) + ' ssl;')
        nginx_path = destination / 'nginx.conf'
        private_write(nginx_path, nginx)
        services['nginx'] = dict(network_mode='host', ports=[], dns=[bind_ip], dns_search=search, dns_opt=['ndots:0'],
                                volumes=[str(nginx_path) + ':/etc/nginx/nginx.conf:ro'],
                                labels={'org.skyshield.network.nginx': hashlib.sha256((nginx + env.get('PUBLIC_HOST', 'localhost')).encode()).hexdigest(),
                                        'org.skyshield.network.public-port': str(public_port)})
    overlay = dict(services=services, networks={'default': {'name': network_name, 'external': True}},
                   volumes={'network_socket': {}})
    write_overlay(overlay_path, overlay)
    print('宿主机网络继承已准备：DNS/hosts 与代理由当前主机提供，执行组件网络模式为 ' + mode + '。', file=sys.stderr)
    return overlay_path


def check(root, target):
    env = settings(root)
    resolvers, search = resolver_search(host_resolv_conf(env))
    print('部署机 DNS：' + ', '.join(resolvers))
    print('搜索域：' + (', '.join(search) or '无'))
    print('环境代理：' + ('已配置' if any(env.get(key) for key in PROXY_KEYS if 'NO_PROXY' not in key.upper()) else '未配置'))
    if not target:
        print('可附加一个内网域名或 IP，比较部署机和 Server 容器的解析结果。')
        return
    parsed = urllib.parse.urlsplit(target if '://' in target else '//' + target)
    if parsed.username or parsed.password or not parsed.hostname:
        raise RuntimeError('诊断目标请使用不含凭据的域名、IP、IP:端口或 HTTP(S) URL。')
    host = parsed.hostname
    port = parsed.port or ({'http': 80, 'https': 443}.get(parsed.scheme))
    try:
        addresses = sorted({item[4][0] for item in socket.getaddrinfo(host, None)})
    except socket.gaierror as error:
        raise RuntimeError('部署机本身无法解析该地址；请先检查虚拟机 DNS/VPN：' + str(error)) from None
    print('部署机解析：' + ', '.join(addresses))
    args = ['compose', '-f', str(root / 'compose.tianqiong.yaml')]
    overlay = root / '.local/network/compose.network.yaml'
    if overlay.exists():args += ['-f', str(overlay)]
    container = docker(*args, 'ps', '--quiet', 'server')
    if not container or '\n' in container:raise RuntimeError('没有找到唯一的运行中 Server 容器。')
    code = "import json,socket,sys;print(json.dumps(sorted({r[4][0] for r in socket.getaddrinfo(sys.argv[1],None)})))"
    result = subprocess.run(['docker', 'exec', container, 'python3', '-c', code, host], text=True, capture_output=True)
    if result.returncode:raise RuntimeError('Server 容器解析失败；请检查网络适配器和容器日志。')
    resolved = json.loads(result.stdout)
    print('Server 容器解析：' + ', '.join(resolved))
    if set(resolved).isdisjoint(addresses):raise RuntimeError('部署机与容器的解析结果不一致，请检查 DNS 或 hosts。')
    if port:
        try:
            with socket.create_connection((host, port), timeout=3):
                print('部署机 TCP 连接：成功')
        except OSError:
            raise RuntimeError('部署机本身无法连接该端口；请检查虚拟机路由、VPN、目标监听地址和防火墙。') from None
        code = "import socket,sys; s=socket.create_connection((sys.argv[1],int(sys.argv[2])),timeout=3);s.close()"
        result = subprocess.run(['docker', 'exec', container, 'python3', '-c', code, host, str(port)], text=True, capture_output=True)
        if result.returncode:
            raise RuntimeError('部署机可以连接，但 Server 容器无法连接；请检查网络模式、Docker 网段与 VPN/防火墙规则。')
        print('Server TCP 连接：成功（仅确认端口连通，不代表应用认证或 TLS 验证通过）')


def preflight(root):
    env = settings(root)
    if env.get('SKYSHIELD_INHERIT_HOST_NETWORK', '1') == '0' or env.get('SKYSHIELD_NETWORK_MODE', 'host') != 'host':
        return
    public_port = int(env.get('PUBLIC_PORT', '8443'))
    owned = set()
    for service, ports in [('server', (8080, 9090)), ('frontend', (3000,)), ('nginx', (public_port,))]:
        existing = docker('compose', '-f', str(root / 'compose.tianqiong.yaml'), 'ps', '--all', '--quiet', service)
        if not existing or '\n' in existing:
            continue
        container = json.loads(docker('inspect', existing))[0]
        if not container.get('State', {}).get('Running'):
            continue
        host_config = container.get('HostConfig', {})
        if host_config.get('NetworkMode') == 'host':
            owned.update(ports)
        else:
            for bindings in (host_config.get('PortBindings') or {}).values():
                owned.update(int(binding['HostPort']) for binding in (bindings or []) if binding.get('HostPort'))
    for port in (3000, 8080, 9090, public_port):
        if port in owned:
            continue
        with socket.socket() as probe:
            probe.setsockopt(socket.SOL_SOCKET, socket.SO_REUSEADDR, 1)
            try:
                probe.bind(('0.0.0.0', port))
            except OSError:
                raise RuntimeError('宿主网络模式需要 3000/8080/9090 和 PUBLIC_PORT 端口，端口 ' + str(port) + ' 已被其他程序占用。请先停止对应本地服务，或显式配置 SKYSHIELD_NETWORK_MODE=bridge。') from None


def stop_disabled(root):
    if settings(root).get('SKYSHIELD_INHERIT_HOST_NETWORK', '1') != '0':
        return
    ids = docker('ps', '--quiet', '--filter', 'label=com.docker.compose.project=tianqiong',
                 '--filter', 'label=com.docker.compose.service=network-helper').splitlines()
    for container in ids:
        docker('stop', '--time', '30', container)


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument('action', choices=['prepare', 'check', 'preflight', 'stop-disabled'])
    parser.add_argument('--root', type=Path, default=Path(__file__).resolve().parent.parent)
    parser.add_argument('--hub', action='store_true')
    parser.add_argument('target', nargs='?')
    args = parser.parse_intermixed_args()
    try:
        if args.action == 'prepare':
            path = prepare(args.root.resolve(), args.hub)
            if path:print(path)
        elif args.action == 'preflight':preflight(args.root.resolve())
        elif args.action == 'stop-disabled':stop_disabled(args.root.resolve())
        else:check(args.root.resolve(), args.target)
    except (OSError, RuntimeError, ValueError, KeyError) as error:
        print('网络配置失败：' + str(error), file=sys.stderr)
        return 1
    return 0


if __name__ == '__main__':
    raise SystemExit(main())
