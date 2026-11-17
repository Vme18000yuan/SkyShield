import contextlib
import importlib.util
import io
import ipaddress
import json
import os
from pathlib import Path
import stat
import tempfile
import unittest
import shutil
import subprocess
from unittest import mock

SPEC = importlib.util.spec_from_file_location('skyshield_deployment_network', Path(__file__).with_name('deployment-network.py'))
network = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(network)


class DeploymentNetworkTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory(prefix='skyshield-network-config-')
        self.root = Path(self.temp.name)
        (self.root / 'docker/nginx').mkdir(parents=True)
        (self.root / 'docker/nginx/nginx.conf').write_text('upstream backend { server server:8080; } upstream grpc { server server:9090 resolve; } server { listen 443 ssl; }')
        self.projection = {'name': 'tianqiong', 'services': {'bootstrap': {
            'image': 'solder110/skyshield-bootstrap:fixture',
            'build': {'context': str(self.root), 'dockerfile': 'docker/bootstrap/Dockerfile'}}}}
        self.project = {'Id': '1234567890abcdef', 'Driver': 'bridge',
                        'Labels': {'com.docker.compose.project': 'tianqiong'},
                        'IPAM': {'Config': [{'Subnet': '172.30.240.0/24', 'Gateway': '172.30.240.1'}]}}
        self.bridge = {'IPAM': {'Config': [{'Subnet': '172.17.0.0/16', 'Gateway': '172.17.0.1'}]}}

    def tearDown(self):
        self.temp.cleanup()

    def prepare(self, **kwargs):
        with mock.patch.object(network, 'docker', return_value=json.dumps(self.projection)), \
             mock.patch.object(network, 'docker_socket', return_value=Path('/var/run/docker.sock')), \
             mock.patch.object(network, 'network_info', side_effect=lambda name: self.bridge if name == 'bridge' else self.project), \
             mock.patch.object(network, 'resolver_search', return_value=(['127.0.0.53'], ['corp.example'])), \
             mock.patch.object(network, 'host_routes', return_value=[]), \
             mock.patch.object(network, 'host_addresses', return_value=['127.0.0.0/8', '::1/128']), \
             mock.patch.dict(os.environ, {'SKYSHIELD_INHERIT_HOST_NETWORK': '1', 'DOCKER_HOST': '',
                                          'HTTP_PROXY': 'http://fixture:private-fixture@127.0.0.1:7897'}, clear=False), \
             contextlib.redirect_stderr(io.StringIO()) as output:
            path = network.prepare(self.root, **kwargs)
            return path, output.getvalue()

    def test_default_inheritance_covers_server_and_dynamic_agent_without_public_dns(self):
        path, output = self.prepare(hub=True)
        overlay = json.loads(path.with_suffix('.json').read_text())
        config_path = self.root / '.local/network/config.json'
        config = json.loads(config_path.read_text())
        self.assertEqual(config['resolvConf'], '/etc/resolv.conf')
        self.assertEqual(config['hostsFile'], '/etc/hosts')
        self.assertEqual(config['allowedCIDRs'], ['172.30.240.0/24', '172.17.0.0/16', '127.0.0.0/8', '::1/128'])
        self.assertEqual(config['engineNetworkMode'], 'host')
        self.assertEqual(overlay['services']['server']['network_mode'], 'host')
        self.assertEqual(overlay['services']['agent']['network_mode'], 'host')
        self.assertEqual(overlay['services']['frontend']['network_mode'], 'host')
        self.assertEqual(overlay['services']['nginx']['network_mode'], 'host')
        self.assertIn('!reset []', path.read_text())
        self.assertNotIn('postgres', overlay['services'])
        self.assertNotIn('redis', overlay['services'])
        self.assertEqual(overlay['services']['server']['dns'], ['172.30.240.1'])
        self.assertEqual(overlay['services']['server']['dns_search'], ['corp.example'])
        self.assertEqual(overlay['services']['agent']['environment']['DOCKER_HOST'], 'unix:///run/skyshield-network/docker.sock')
        self.assertEqual(overlay['services']['network-helper']['image'], 'solder110/skyshield-bootstrap:fixture')
        self.assertEqual(overlay['services']['network-helper']['network_mode'], 'host')
        self.assertEqual((self.root / '.local/network/resolvers.txt').read_text(), '172.30.240.1\n')
        self.assertNotIn('8.8.8.8', path.read_text())
        self.assertNotIn('private-fixture', path.read_text())
        self.assertNotIn('private-fixture', output)
        self.assertEqual(stat.S_IMODE(config_path.stat().st_mode), 0o600)
        self.assertEqual(stat.S_IMODE(config_path.parent.stat().st_mode), 0o700)
        token = (config_path.parent / 'proxy-token').read_bytes()
        self.prepare(hub=True)
        self.assertEqual((config_path.parent / 'proxy-token').read_bytes(), token)

    def test_disable_keeps_existing_resources_and_does_not_touch_daemon(self):
        with mock.patch.dict(os.environ, {'SKYSHIELD_INHERIT_HOST_NETWORK': '0'}), mock.patch.object(network, 'docker') as docker:
            self.assertIsNone(network.prepare(self.root))
            docker.assert_not_called()
        self.assertFalse((self.root / '.local').exists())

    def test_foreign_network_is_rejected_before_private_files_are_created(self):
        self.project['Labels']['com.docker.compose.project'] = 'other-project'
        with self.assertRaisesRegex(RuntimeError, '不属于当前项目'):
            self.prepare()
        self.assertFalse((self.root / '.local').exists())

    def test_subnet_selection_avoids_lan_and_vpn_routes(self):
        routes = [(ipaddress.ip_network('10.0.0.0/8'), 'tun0'),
                  (ipaddress.ip_network('172.16.0.0/12'), 'eth0'),
                  (ipaddress.ip_network('192.168.0.0/16'), 'tun0')]
        self.assertEqual(network.choose_subnet(routes), '100.127.240.0/24')
        with self.assertRaisesRegex(RuntimeError, '不冲突'):
            network.choose_subnet(routes, '10.253.240.0/24')
        for invalid in ['0.0.0.0/24', '127.0.0.0/24', '224.0.0.0/24', '8.8.8.0/24']:
            with self.assertRaises(RuntimeError):
                network.choose_subnet([], invalid)

    def test_missing_dns_does_not_fall_back_to_public_resolvers(self):
        resolv = self.root / 'resolv.conf'
        resolv.write_text('# no resolver configured\n')
        with self.assertRaisesRegex(RuntimeError, '没有配置 DNS'):
            network.resolver_search(resolv)
        resolv.write_text('nameserver 127.0.0.53\nsearch corp.example\n')
        self.assertEqual(network.resolver_search(resolv), (['127.0.0.53'], ['corp.example']))

    def test_resolved_stub_preserves_split_dns_and_explicit_override_wins(self):
        stub, fallback = self.root / 'stub.conf', self.root / 'resolv.conf'
        stub.write_text('nameserver 127.0.0.53\n')
        fallback.write_text('nameserver 192.0.2.53\n')
        with mock.patch.object(network.socket, 'create_connection'):
            self.assertEqual(network.host_resolv_conf({}, stub, fallback), stub)
            self.assertEqual(network.host_resolv_conf({'SKYSHIELD_HOST_RESOLV_CONF': str(fallback)}, stub, fallback), fallback)
        with mock.patch.object(network.socket, 'create_connection', side_effect=OSError):
            self.assertEqual(network.host_resolv_conf({}, stub, fallback), fallback)

    def test_remote_docker_context_is_rejected_before_network_mutation(self):
        with mock.patch.object(network, 'docker', return_value='ssh://remote.example') as docker:
            with self.assertRaisesRegex(RuntimeError, '不支持远程'):
                network.docker_socket({'DOCKER_CONTEXT': 'remote', 'DOCKER_HOST': 'unix:///var/run/docker.sock'})
            self.assertEqual(docker.call_count, 1)
            self.assertEqual(docker.call_args.args[:2], ('context', 'inspect'))

    def test_host_port_conflict_is_reported_without_stopping_other_processes(self):
        probe = mock.MagicMock()
        probe.__enter__.return_value.bind.side_effect = OSError('fixture occupied')
        with mock.patch.object(network, 'settings', return_value={'SKYSHIELD_NETWORK_MODE': 'host'}), \
             mock.patch.object(network, 'docker', return_value='') as docker, \
             mock.patch.object(network.socket, 'socket', return_value=probe):
            with self.assertRaisesRegex(RuntimeError, '8080/9090'):
                network.preflight(self.root)
            self.assertFalse(any(call.args and call.args[0] in ('stop', 'rm') for call in docker.call_args_list))

    @unittest.skipUnless(shutil.which('docker'), 'Docker Compose CLI is unavailable')
    def test_generated_overlay_is_accepted_by_compose(self):
        version = subprocess.run(['docker', 'compose', 'version'], capture_output=True)
        if version.returncode:
            self.skipTest('Docker Compose plugin is unavailable')
        path, _ = self.prepare(hub=True)
        root = Path(__file__).resolve().parent.parent
        result = subprocess.run(['docker', 'compose', '-f', str(root / 'compose.tianqiong.yaml'), '-f', str(path), 'config', '--format', 'json'],
                                capture_output=True, text=True)
        self.assertEqual(result.returncode, 0, result.stderr)
        merged = json.loads(result.stdout)
        for name in ('server', 'frontend', 'nginx', 'agent', 'bootstrap', 'migrate'):
            self.assertEqual(merged['services'][name]['network_mode'], 'host', name)
            self.assertNotIn('networks', merged['services'][name], name)
        self.assertFalse(merged['services']['nginx'].get('ports'))
        for name in ('postgres', 'redis', 'loki'):
            self.assertIn('default', merged['services'][name]['networks'])
            self.assertFalse(merged['services'][name].get('ports'))

    def test_explicit_bridge_mode_keeps_entry_port_and_private_networks(self):
        with mock.patch.dict(os.environ, {'SKYSHIELD_NETWORK_MODE': 'bridge'}):
            path, _ = self.prepare(hub=True)
        overlay = json.loads(path.with_suffix('.json').read_text())
        self.assertNotIn('network_mode', overlay['services']['server'])
        self.assertNotIn('network_mode', overlay['services']['agent'])
        self.assertNotIn('frontend', overlay['services'])
        self.assertNotIn('nginx', overlay['services'])
        self.assertEqual(json.loads((path.parent / 'config.json').read_text())['engineNetworkMode'], 'bridge')


if __name__ == '__main__':
    unittest.main()
