import contextlib
import fcntl
import importlib.util
import io
import json
import os
from pathlib import Path
import shutil
import subprocess
import sys
import tempfile
import time
from types import SimpleNamespace
import unittest
from unittest import mock

SPEC = importlib.util.spec_from_file_location('skyshield_uninstall', Path(__file__).with_name('uninstall.py'))
uninstall = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(uninstall)


class FakeDocker:
    def __init__(self, project='tianqiong'):
        self.project = project
        self.calls = []
        self.volumes = {name: {'Name': name, 'Mountpoint': '/var/lib/docker/volumes/' + name + '/_data',
                              'Labels': {'com.docker.compose.project': project}}
                        for name in uninstall.FIXED_VOLUMES | {project + '_' + name for name in uninstall.COMPOSE_VOLUMES}}
        self.containers = {'server-id': self.container('server-id', project, 'server'),
                           'agent-id': self.container('agent-id', project, 'agent'),
                           'other-id': self.container('other-id', 'another-project', 'server')}
        self.networks = {'network-id': {'Id': 'network-id', 'Name': project + '_default',
                                       'Labels': {'com.docker.compose.project': project},
                                       'Containers': {'server-id': {}, 'agent-id': {}}}}
        self.images = [{'Repository': 'solder110/skyshield-server', 'Tag': 'latest'},
                       {'Repository': 'tianqiong-bootstrap', 'Tag': 'local'},
                       {'Repository': 'postgres', 'Tag': '16'},
                       {'Repository': 'another-project/server', 'Tag': 'latest'}]
        self.fail = None
        self.available = True

    @staticmethod
    def container(identifier, project, service):
        return {'Id': identifier, 'Name': '/' + project + '-' + service,
                'Config': {'Labels': {'com.docker.compose.project': project, 'com.docker.compose.service': service}},
                'State': {'Running': True}, 'Mounts': []}

    def __call__(self, *args, check=True):
        self.calls.append(args)
        code, output = 0, ''
        if self.fail == args:
            code = 1
        elif args[0] == 'info':
            code = 0 if self.available else 1
        elif args[0] == 'ps':
            identifiers = list(self.containers)
            if '--filter' in args:
                identifiers = [value for value in identifiers if value == args[args.index('--filter') + 1].removeprefix('id=')]
            output = '\n'.join(identifiers)
        elif args[0] == 'inspect':
            output = json.dumps([self.containers[value] for value in args[1:]])
        elif args[:2] == ('volume', 'ls'):
            names = list(self.volumes)
            if '--filter' in args:
                names = [name for name in names if (self.volumes[name].get('Labels') or {}).get('com.docker.compose.project') == self.project]
            output = '\n'.join(names)
        elif args[:2] == ('volume', 'inspect'):
            output = json.dumps([self.volumes[args[2]]])
        elif args[:2] == ('network', 'ls'):
            output = '\n'.join(self.networks)
        elif args[:2] == ('network', 'inspect'):
            values = [value for value in self.networks.values() if args[2] in (value['Id'], value['Name'])]
            output, code = json.dumps(values), 0 if values else 1
        elif args[0] == 'stop':
            self.containers[args[-1]]['State']['Running'] = False
        elif args[0] == 'rm':
            self.containers.pop(args[1])
            for network in self.networks.values():
                network['Containers'].pop(args[1], None)
        elif args[:2] == ('network', 'rm'):
            self.networks = {key: value for key, value in self.networks.items() if value['Name'] != args[2]}
        elif args[:2] == ('volume', 'rm'):
            self.volumes.pop(args[2])
        elif args[:2] == ('image', 'ls'):
            output = '\n'.join(json.dumps(value) for value in self.images)
        elif args[:2] == ('image', 'rm'):
            self.images = [value for value in self.images if value['Repository'] + ':' + value['Tag'] != args[2]]
        else:
            raise AssertionError('Unexpected Docker command: ' + str(args))
        if code and check:
            raise RuntimeError('Fixture Docker operation failed')
        return subprocess.CompletedProcess(args, code, output, 'No such resource' if code else '')

    def mutations(self):
        return [args for args in self.calls if args[0] in ('stop', 'rm') or args[:2] in
                (('volume', 'rm'), ('network', 'rm'), ('image', 'rm'))]


class UninstallTests(unittest.TestCase):
    def setUp(self):
        self.work = Path(self.enterContext(tempfile.TemporaryDirectory(prefix='skyshield-uninstall-test-')))
        self.root = self.work / 'deployment'
        self.root.mkdir()
        self.data = self.root / '.local/dev-runtime'
        self.execution = self.work / 'execution'
        self.execution.mkdir(mode=0o700)
        self.output = self.enterContext(contextlib.redirect_stdout(io.StringIO()))
        self.docker = FakeDocker()
        self.enterContext(mock.patch.object(uninstall.shutil, 'which', return_value='/fixture/docker'))
        self.enterContext(mock.patch.object(uninstall.Uninstaller, 'docker',
                                           lambda _self, *args, **kwargs: self.docker(*args, **kwargs)))

    def args(self, **overrides):
        values = dict(mode='all', purge=True, yes=True, dry_run=False, remove_env=False, remove_images=False,
                      local_data_dir=str(self.data), execution_root=str(self.execution), project_name='tianqiong')
        values.update(overrides)
        return SimpleNamespace(**values)

    def plan(self, **options):
        return uninstall.Uninstaller(self.root, self.args(**options)).collect()

    def file(self, relative, content='fixture', mode=0o600):
        path = self.root / relative
        path.parent.mkdir(parents=True, exist_ok=True)
        path.write_text(content)
        path.chmod(mode)
        return path

    def native_data(self):
        self.file('.local/dev-runtime/postgres/data/PG_VERSION', '16')
        self.file('.local/dev-runtime/local-processes.json', '{}')
        for name in ('db_password', 'jwt_secret', 'ai_encryption_key'):
            self.file('.local/dev-runtime/secrets/' + name, 'old-native-' + name)

    def docker_data(self):
        for name in ('db_password', 'jwt_secret', 'ai_encryption_key'):
            self.file('.local/secrets/' + name, 'old-docker-' + name)
        self.file('.local/network/compose.network.yaml')
        self.file('.local/updater/compose.release.json', '{}')
        self.file('.local/updater/operation-backup/database.dump')

    def test_dry_run_leaves_files_processes_and_docker_unchanged(self):
        self.native_data()
        self.docker_data()
        before = {str(path): path.read_bytes() for path in self.root.rglob('*') if path.is_file()}
        plan = self.plan(dry_run=True)
        plan.show()
        self.assertIn('预览完成', self.output.getvalue())
        self.assertEqual(before, {str(path): path.read_bytes() for path in self.root.rglob('*') if path.is_file()})
        self.assertFalse(self.docker.mutations())
        self.assertFalse((self.root / '.local/deployment.lock').exists())

    def test_local_purge_removes_runtime_keys_builds_dependencies_and_local_backups(self):
        self.native_data()
        self.docker_data()
        for path in ('frontend/node_modules/next/package.json', 'frontend/.next/BUILD_ID',
                     'frontend/.next-skyshield-stage-fixture/BUILD_ID', 'frontend/tsconfig..next-skyshield-stage-fixture.json',
                     '.local/updater-local/operation-old/database.dump'):
            self.file(path)
        source = self.file('frontend/tsconfig.json')
        env = self.file('.env', 'PUBLIC_PORT=18443\n')
        self.plan(mode='local').apply()
        self.assertFalse(self.data.exists())
        self.assertFalse(self.execution.exists())
        self.assertFalse((self.root / 'frontend/node_modules').exists())
        self.assertFalse((self.root / 'frontend/.next').exists())
        self.assertFalse(list((self.root / 'frontend').glob('*skyshield-stage*')))
        self.assertFalse((self.root / '.local/updater-local').exists())
        self.assertTrue((self.root / '.local/secrets/db_password').is_file())
        self.assertTrue((self.root / '.local/updater/compose.release.json').is_file())
        self.assertTrue(source.is_file())
        self.assertTrue(env.is_file())
        self.assertFalse(self.docker.calls)

    def test_local_uninstall_preserves_runtime_keys_and_frontend(self):
        self.native_data()
        binary = self.file('frontend/node_modules/next/package.json')
        self.plan(mode='local', purge=False).apply()
        self.assertTrue((self.data / 'postgres/data/PG_VERSION').is_file())
        self.assertEqual((self.data / 'secrets/db_password').read_text(), 'old-native-db_password')
        self.assertTrue(binary.is_file())

    def test_docker_default_removes_services_and_network_but_keeps_data_and_keys(self):
        self.docker_data()
        original = set(self.docker.volumes)
        self.plan(mode='docker', purge=False).apply()
        self.assertEqual(set(self.docker.containers), {'other-id'})
        self.assertFalse(self.docker.networks)
        self.assertEqual(set(self.docker.volumes), original)
        self.assertTrue((self.root / '.local/secrets/db_password').is_file())
        self.assertTrue((self.root / '.local/updater/compose.release.json').is_file())

    def test_docker_purge_removes_orphaned_unlabelled_volumes_and_all_docker_private_state(self):
        self.native_data()
        self.docker_data()
        self.docker.containers = {'other-id': self.docker.containers['other-id']}
        self.docker.networks.clear()
        for value in self.docker.volumes.values():
            value['Labels'] = None
        self.docker.volumes['unrelated-data'] = {'Name': 'unrelated-data', 'Labels': None}
        self.plan(mode='docker').apply()
        self.assertEqual(set(self.docker.volumes), {'unrelated-data'})
        for name in ('secrets', 'network', 'updater'):
            self.assertFalse((self.root / '.local' / name).exists())
        self.assertTrue((self.data / 'postgres/data/PG_VERSION').is_file())
        self.assertTrue(self.execution.exists())
        self.plan(mode='docker').apply()
        self.assertEqual(set(self.docker.volumes), {'unrelated-data'})

    def test_all_purge_clears_both_deployments_but_keeps_source_and_lock_inode(self):
        self.native_data()
        self.docker_data()
        source = self.file('README.md', 'source')
        lock = self.file('.local/deployment.lock', '')
        inode = lock.stat().st_ino
        self.plan().apply()
        self.assertFalse(self.data.exists())
        self.assertFalse((self.root / '.local/secrets').exists())
        self.assertFalse(self.docker.volumes)
        self.assertTrue(source.is_file())
        self.assertEqual(lock.stat().st_ino, inode)
        self.plan().apply()

    def test_docker_failure_keeps_original_keys_configuration_and_local_data_for_retry(self):
        self.native_data()
        self.docker_data()
        self.docker.fail = ('volume', 'rm', 'tianqiong_postgres_data')
        with self.assertRaisesRegex(RuntimeError, 'Fixture Docker'):
            self.plan().apply()
        self.assertEqual((self.root / '.local/secrets/db_password').read_text(), 'old-docker-db_password')
        self.assertTrue((self.root / '.local/network/compose.network.yaml').exists())
        self.assertTrue((self.data / 'postgres/data/PG_VERSION').exists())
        self.docker.fail = None
        self.plan().apply()
        self.assertFalse(self.docker.volumes)
        self.assertFalse((self.root / '.local/secrets').exists())

    def test_foreign_fixed_volume_is_rejected_before_any_mutation(self):
        self.docker_data()
        self.docker.volumes['lunafox_data']['Labels']['com.docker.compose.project'] = 'lunafox'
        with self.assertRaisesRegex(RuntimeError, '属于其他项目 lunafox'):
            self.plan()
        self.assertFalse(self.docker.mutations())
        self.assertTrue((self.root / '.local/secrets/db_password').exists())

    def test_data_volume_used_by_unrelated_container_blocks_purge(self):
        self.docker.containers['other-id']['Mounts'] = [{'Name': 'lunafox_data', 'Destination': '/backup'}]
        with self.assertRaisesRegex(RuntimeError, '其他容器'):
            self.plan(mode='docker')
        self.assertFalse(self.docker.mutations())

    def test_bind_mount_of_volume_data_by_unrelated_container_blocks_purge(self):
        self.docker.containers['other-id']['Mounts'] = [
            {'Type': 'bind', 'Source': '/var/lib/docker/volumes/tianqiong_postgres_data/_data', 'Destination': '/backup'}]
        with self.assertRaisesRegex(RuntimeError, '其他容器'):
            self.plan(mode='docker')
        self.assertFalse(self.docker.mutations())

    def test_network_used_by_unrelated_container_blocks_uninstall(self):
        self.docker.networks['network-id']['Containers']['other-id'] = {}
        with self.assertRaisesRegex(RuntimeError, '网络.*其他容器'):
            self.plan(mode='docker', purge=False)
        self.assertFalse(self.docker.mutations())

    def test_short_and_full_docker_network_ids_are_removed_only_once(self):
        short_id = 'abcdef123456'
        full_id = short_id + '0' * 52
        self.docker.networks = {short_id: {'Id': full_id, 'Name': 'tianqiong_default',
                                          'Labels': {'com.docker.compose.project': 'tianqiong'}, 'Containers': {}}}
        original = self.docker.__call__
        def handle(*args, **kwargs):
            if args[:3] == ('network', 'inspect', short_id):
                args = ('network', 'inspect', full_id)
            return original(*args, **kwargs)
        with mock.patch.object(uninstall.Uninstaller, 'docker', lambda _self, *args, **kwargs: handle(*args, **kwargs)):
            self.plan(mode='docker').apply()
        self.assertEqual(self.docker.calls.count(('network', 'rm', 'tianqiong_default')), 1)

    def test_dynamic_engine_is_identified_by_owned_volume_and_engine_mounts(self):
        engine = {'Id': 'engine-id', 'Name': '/temporary-engine', 'Config': {'Labels': {}},
                  'State': {'Running': True}, 'Mounts': [{'Name': 'lunafox_data', 'Destination': '/opt/lunafox'},
                    *[{'Source': '/var/lib/docker/volumes/lunafox_engine_execution/_data/run/' + name,
                       'Destination': destination} for name, destination in (
                        ('context', '/run/lunafox/context/execution.pb'), ('credential', '/run/lunafox/credential/token'),
                        ('socket', '/run/lunafox/socket/engine.sock'))]]}
        self.docker.containers['engine-id'] = engine
        self.plan(mode='docker').apply()
        self.assertNotIn('engine-id', self.docker.containers)
        self.assertIn('other-id', self.docker.containers)
        calls = self.docker.mutations()
        self.assertLess(calls.index(('stop', '--time', '30', 'agent-id')), calls.index(('stop', '--time', '30', 'engine-id')))

    def test_agent_shutdown_can_remove_another_planned_container(self):
        original = self.docker.__call__
        def handle(*args, **kwargs):
            result = original(*args, **kwargs)
            if args == ('stop', '--time', '30', 'agent-id'):
                self.docker.containers.pop('server-id')
                self.docker.networks['network-id']['Containers'].pop('server-id')
            return result
        with mock.patch.object(uninstall.Uninstaller, 'docker', lambda _self, *args, **kwargs: handle(*args, **kwargs)):
            self.plan(mode='docker').apply()
        self.assertEqual(set(self.docker.containers), {'other-id'})
        self.assertFalse(self.docker.volumes)

    def test_remove_images_only_removes_skyshield_application_tags(self):
        self.plan(mode='docker', remove_images=True).apply()
        self.assertEqual({item['Repository'] for item in self.docker.images}, {'postgres', 'another-project/server'})
        self.assertFalse(any('prune' in args or '--force' in args for args in self.docker.calls))

    def test_remove_env_is_explicit_and_keeps_examples(self):
        self.native_data()
        paths = [self.file(name) for name in ('.env', '.env.local', 'server/.env', 'frontend/.env.production.local')]
        example = self.file('.env.example')
        self.plan(mode='local', remove_env=True).apply()
        self.assertTrue(all(not path.exists() for path in paths))
        self.assertTrue(example.exists())

    def test_docker_unavailable_does_not_remove_local_data_in_all_mode(self):
        self.native_data()
        self.docker.available = False
        with self.assertRaisesRegex(RuntimeError, '无法连接 Docker'):
            self.plan()
        self.assertTrue(self.data.exists())
        self.assertFalse(self.docker.mutations())

    def test_all_without_docker_cli_can_remove_a_local_only_installation(self):
        self.native_data()
        with mock.patch.object(uninstall.shutil, 'which', return_value=None):
            self.plan().apply()
        self.assertFalse(self.data.exists())
        self.assertFalse(self.docker.calls)

    def test_unsafe_data_and_execution_directories_are_rejected(self):
        for path in ('/', str(Path.home()), str(self.root), str(self.root.parent), str(self.root / '.local'),
                     '/etc', '/etc/ssh', '/var/lib/docker', str(self.root / 'frontend')):
            for option in ('local_data_dir', 'execution_root'):
                with self.subTest(path=path, option=option), self.assertRaises(RuntimeError):
                    self.plan(mode='local', **{option: path})

    def test_custom_data_directory_requires_deployment_markers(self):
        custom = self.work / 'valuable'
        custom.mkdir()
        (custom / 'keep').write_text('unrelated data')
        with self.assertRaisesRegex(RuntimeError, '没有本地部署标记'):
            self.plan(mode='local', local_data_dir=str(custom))
        self.assertEqual((custom / 'keep').read_text(), 'unrelated data')

    def test_custom_execution_directory_rejects_unrelated_content(self):
        keep = self.execution / 'important'
        keep.write_text('unrelated data')
        with self.assertRaisesRegex(RuntimeError, '无法确认归属'):
            self.plan(mode='local')
        self.assertTrue(keep.is_file())

    def test_execution_directory_used_by_another_process_is_preserved(self):
        process = subprocess.Popen([sys.executable, '-c', 'import time; time.sleep(120)'], cwd=self.execution)
        try:
            time.sleep(.02)
            with self.assertRaisesRegex(RuntimeError, '扫描执行目录正被其他进程'):
                self.plan(mode='local')
            self.assertIsNone(process.poll())
        finally:
            process.terminate()
            process.wait(timeout=5)

    def test_symlinked_runtime_directory_is_rejected_and_cache_link_is_only_unlinked(self):
        external = self.work / 'external'
        external.mkdir()
        (external / 'keep').write_text('preserved')
        self.data.parent.mkdir(parents=True)
        self.data.symlink_to(external, target_is_directory=True)
        with self.assertRaisesRegex(RuntimeError, '符号链接'):
            self.plan(mode='local')
        self.data.unlink()
        self.native_data()
        frontend = self.root / 'frontend'
        frontend.mkdir()
        (frontend / 'node_modules').symlink_to(external, target_is_directory=True)
        self.plan(mode='local').apply()
        self.assertEqual((external / 'keep').read_text(), 'preserved')
        self.assertFalse((frontend / 'node_modules').is_symlink())

    def test_busy_deployment_lock_prevents_stopping_or_removing_anything(self):
        self.native_data()
        lock = self.file('.local/deployment.lock', '')
        with lock.open('a') as held:
            fcntl.flock(held, fcntl.LOCK_EX | fcntl.LOCK_NB)
            with self.assertRaises(BlockingIOError):
                self.plan().apply()
        self.assertTrue(self.data.exists())
        self.assertFalse(self.docker.mutations())

    def spawn_fixture(self, *, managed=True):
        env = os.environ.copy()
        if managed:
            env.update(TIANQIONG_LOCAL_MODE='true', TIANQIONG_LOCAL_DATA_ROOT=str(self.data))
        else:
            env.pop('TIANQIONG_LOCAL_MODE', None)
            env.pop('TIANQIONG_LOCAL_DATA_ROOT', None)
        process = subprocess.Popen([sys.executable, '-c', 'import time; time.sleep(120)'], cwd=self.root, env=env)
        def cleanup():
            if process.poll() is None:
                process.terminate()
            process.wait(timeout=5)
        self.addCleanup(cleanup)
        for _ in range(200):
            if uninstall.process_environment(process.pid).get('TIANQIONG_LOCAL_MODE') == ('true' if managed else None):
                break
            time.sleep(.01)
        return process

    def test_orphaned_native_process_is_stopped_even_without_process_state(self):
        self.native_data()
        process = self.spawn_fixture()
        plan = self.plan(mode='local')
        self.assertIn(process.pid, plan.processes)
        plan.apply()
        process.wait(timeout=5)
        self.assertFalse(self.data.exists())

    def test_reused_pid_does_not_stop_an_unrelated_process(self):
        self.native_data()
        unrelated = self.spawn_fixture(managed=False)
        self.file('.local/dev-runtime/local-processes.json', json.dumps({'server': {'pid': unrelated.pid, 'start': '0', 'owned': True}}))
        self.plan(mode='local').apply()
        self.assertIsNone(unrelated.poll())

    def test_matching_pid_with_unrelated_identity_or_unmanaged_owner_is_rejected(self):
        self.native_data()
        process = self.spawn_fixture(managed=False)
        tick = uninstall.process_info(process.pid)['start']
        for owned, expected in ((True, '路径或环境'), (False, '其他方式启动')):
            self.file('.local/dev-runtime/local-processes.json', json.dumps({'server': {'pid': process.pid, 'start': tick, 'owned': owned}}))
            with self.subTest(owned=owned), self.assertRaisesRegex(RuntimeError, expected):
                self.plan(mode='local')
            self.assertIsNone(process.poll())

    def test_custom_compose_project_name_can_be_read_without_working_compose(self):
        self.file('.env', "COMPOSE_PROJECT_NAME='old-skyshield'\n")
        self.docker = FakeDocker('old-skyshield')
        plan = self.plan(mode='docker', project_name=None)
        self.assertEqual(plan.project, 'old-skyshield')
        plan.apply()
        self.assertFalse(self.docker.volumes)

    def test_legacy_local_updater_is_stopped_before_its_state_is_removed(self):
        self.native_data()
        daemon_script = self.file('scripts/system-update.py', 'import time; time.sleep(120)\n')
        process = subprocess.Popen([sys.executable, str(daemon_script), 'serve', '--mode', 'local'], cwd=self.root)
        try:
            time.sleep(.02)
            tick = uninstall.process_info(process.pid)['start']
            self.file('.local/updater/daemon.json', json.dumps({'pid': process.pid, 'startTicks': tick,
                                                              'root': str(self.root), 'mode': 'local'}))
            self.file('.local/updater/operation-backup/database.dump')
            plan = self.plan(mode='local')
            self.assertEqual(plan.processes[process.pid]['kind'], 'updater')
            plan.apply()
            process.wait(timeout=5)
            self.assertFalse((self.root / '.local/updater').exists())
        finally:
            if process.poll() is None:
                process.terminate()
            process.wait(timeout=5)

    def test_docker_purge_resets_image_pin_in_a_legacy_local_updater_directory(self):
        self.docker_data()
        self.file('.local/updater/daemon.json', json.dumps({'mode': 'local'}))
        self.plan(mode='docker').apply()
        self.assertFalse((self.root / '.local/updater/compose.release.json').exists())
        self.assertTrue((self.root / '.local/updater/operation-backup/database.dump').exists())

    def test_local_purge_preserves_docker_pin_in_a_shared_legacy_updater_directory(self):
        self.native_data()
        self.docker_data()
        self.file('.local/updater/daemon.json', json.dumps({'mode': 'local'}))
        plan = self.plan(mode='local')
        plan.apply()
        self.assertTrue((self.root / '.local/updater/compose.release.json').exists())
        self.assertFalse(self.data.exists())
        self.assertTrue(plan.notes)

    def test_purge_requires_explicit_confirmation_before_inspecting_or_changing_deployment(self):
        with contextlib.redirect_stderr(io.StringIO()), mock.patch.object(uninstall, 'ROOT', self.root):
            with self.assertRaises(SystemExit) as error:
                uninstall.main(['--mode', 'local', '--purge'])
        self.assertEqual(error.exception.code, 2)
        self.assertFalse(self.docker.calls)
        self.assertFalse((self.root / '.local').exists())


if __name__ == '__main__':
    unittest.main()
