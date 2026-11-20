#!/usr/bin/env python3
"""Remove this SkyShield deployment, optionally deleting its persistent state."""
import argparse
import contextlib
import fcntl
import json
import os
from pathlib import Path
import re
import shlex
import shutil
import signal
import subprocess
import sys
import time

ROOT = Path(__file__).resolve().parent.parent
FIXED_VOLUMES = {'lunafox_data', 'lunafox_engine_execution', 'lunafox_agent_state'}
COMPOSE_VOLUMES = {'postgres_data', 'upgrade_state', 'loki_data', 'ssl', 'network_socket'}
IMAGE_REPOSITORIES = {prefix + name for prefix in ('solder110/skyshield-', 'tianqiong-')
                      for name in ('server', 'frontend', 'bootstrap', 'nginx')}


def read_json(path, default=None):
    try:
        return json.loads(path.read_text())
    except FileNotFoundError:
        return default


def absolute(value):
    return Path(os.path.abspath(os.path.expanduser(str(value))))


def inside(path, parent):
    return path == parent or parent in path.parents


def process_info(pid):
    try:
        proc = Path('/proc', str(pid))
        fields = (proc / 'stat').read_text().rsplit(')', 1)[1].split()
        if fields[0] == 'Z':
            return None
        return dict(pid=pid, start=fields[19], ppid=int(fields[1]), uid=proc.stat().st_uid,
                    args=[os.fsdecode(value) for value in (proc / 'cmdline').read_bytes().split(b'\0') if value],
                    exe=os.readlink(proc / 'exe').removesuffix(' (deleted)'),
                    cwd=os.readlink(proc / 'cwd').removesuffix(' (deleted)'))
    except (FileNotFoundError, ProcessLookupError):
        return None


def process_environment(pid):
    try:
        values = Path('/proc', str(pid), 'environ').read_bytes().split(b'\0')
        result = {}
        for item in values:
            key, separator, value = item.partition(b'=')
            if separator and key in (b'TIANQIONG_LOCAL_MODE', b'TIANQIONG_LOCAL_DATA_ROOT'):
                result[os.fsdecode(key)] = os.fsdecode(value)
        return result
    except (FileNotFoundError, ProcessLookupError):
        return {}


class Uninstaller:
    def __init__(self, root, args):
        self.root = root.resolve()
        self.args = args
        self.local = args.mode in ('local', 'all')
        self.docker_enabled = args.mode in ('docker', 'all')
        self.data = absolute(args.local_data_dir or os.environ.get('SKYSHIELD_LOCAL_DATA_DIR', self.root / '.local/dev-runtime'))
        self.uid = self.data.stat().st_uid if self.local and self.data.exists() else int(os.environ.get('SUDO_UID', os.getuid()))
        self.execution = absolute(args.execution_root or os.environ.get('SKYSHIELD_LOCAL_EXECUTION_ROOT', f'/tmp/skyshield-{self.uid}-engines'))
        self.project = args.project_name or os.environ.get('COMPOSE_PROJECT_NAME') or self.env_project() or 'tianqiong'
        if not re.fullmatch(r'[a-z0-9][a-z0-9_-]*', self.project):
            raise RuntimeError('Compose 项目名无效，请检查 --project-name。')
        self.processes = {}
        self.containers = []
        self.volumes = []
        self.networks = []
        self.images = []
        self.paths = []
        self.notes = []

    def env_project(self):
        if not self.docker_enabled:
            return None
        path = self.root / '.env'
        if path.is_file():
            for line in path.read_text().splitlines():
                key, separator, value = line.strip().removeprefix('export ').partition('=')
                if separator and key.strip() == 'COMPOSE_PROJECT_NAME':
                    return ' '.join(shlex.split(value, comments=True))
        return None

    def docker(self, *args, check=True):
        result = subprocess.run(['docker', *map(str, args)], text=True, capture_output=True, timeout=90)
        if check and result.returncode:
            raise RuntimeError('Docker 操作失败：' + ' '.join(map(str, args[:2])) + '；未删除后续密钥或配置，请检查 Docker 权限和资源占用后重试。')
        return result

    def validate_directory(self, path, *, runtime=False):
        protected = {Path('/'), Path('/tmp'), Path('/var'), Path('/var/lib'), Path('/home'),
                     Path.home(), self.root, self.root / '.local', self.root / 'frontend'}
        if path in protected or path in self.root.parents or path in Path.home().parents:
            raise RuntimeError(f'拒绝清理不安全的目录：{path}')
        system_roots = ('/etc', '/usr', '/bin', '/sbin', '/lib', '/lib64', '/proc', '/sys', '/dev',
                        '/boot', '/run', '/var/log', '/var/lib/docker', '/var/lib/postgresql', '/var/lib/redis')
        if any(inside(path, Path(parent)) for parent in system_roots):
            raise RuntimeError(f'拒绝清理系统目录：{path}')
        if path.resolve() != path:
            raise RuntimeError(f'清理目录或其父目录不能是符号链接：{path}')
        if inside(path, self.root) and not inside(path, self.root / '.local'):
            raise RuntimeError(f'运行目录与源码目录重叠，拒绝清理：{path}')
        if runtime and path.exists():
            if not path.is_dir():
                raise RuntimeError(f'运行目录不是普通目录：{path}')
            if path != self.root / '.local/dev-runtime' and not any((path / name).exists() for name in
                    ('local-processes.json', 'postgres/data/PG_VERSION', 'secrets/db_password', 'frontend-build.json')):
                raise RuntimeError(f'自定义目录中没有本地部署标记，拒绝清理：{path}')

    def snapshot_processes(self):
        result = {}
        for proc in Path('/proc').glob('[0-9]*'):
            try:
                if proc.stat().st_uid != self.uid:
                    continue
                info = process_info(int(proc.name))
            except (PermissionError, FileNotFoundError, ProcessLookupError):
                continue
            if info and info['pid'] != os.getpid():
                result[info['pid']] = info
        return result

    def local_process_kind(self, info):
        args, exe = info['args'], Path(info['exe'])
        if exe.name == 'postgres' and any(args[index:index + 2] == ['-D', str(self.data / 'postgres/data')]
                                          for index in range(len(args) - 1)):
            return 'postgres'
        if inside(exe, self.data / 'bin') or inside(exe, self.data / 'tools'):
            return 'worker'
        if Path(info['cwd']) in (self.root, self.root / 'frontend'):
            try:
                env = process_environment(info['pid'])
            except PermissionError:
                return None
            if env.get('TIANQIONG_LOCAL_DATA_ROOT') == str(self.data) and env.get('TIANQIONG_LOCAL_MODE') == 'true':
                return 'worker'
        return None

    def local_processes(self):
        if not self.data.exists() and not self.execution.exists():
            return {}
        snapshot = self.snapshot_processes()
        selected = {}
        state = read_json(self.data / 'local-processes.json', {})
        if not isinstance(state, dict):
            raise RuntimeError('本地进程记录无效，请检查 local-processes.json。')
        for name, entry in state.items():
            info = snapshot.get(entry.get('pid')) if isinstance(entry, dict) else None
            if info and str(entry.get('start')) == info['start']:
                if not entry.get('owned'):
                    raise RuntimeError(f'本地进程 {name} 由其他方式启动，请先由原启动方式停止。')
                if not self.local_process_kind(info):
                    raise RuntimeError(f'本地进程 {name} 的路径或环境与当前部署不符，未执行卸载。')
                selected[info['pid']] = {**info, 'kind': name}
        for pid, info in snapshot.items():
            kind = self.local_process_kind(info)
            if kind and pid not in selected:
                selected[pid] = {**info, 'kind': kind}
        # Include tool/Node/AI children even when they have their own sessions.
        while True:
            children = {pid: {**info, 'kind': 'worker'} for pid, info in snapshot.items()
                        if pid not in selected and info['ppid'] in selected and selected[info['ppid']]['kind'] != 'postgres'}
            if not children:
                return selected
            selected.update(children)

    def validate_execution(self):
        if not self.execution.exists():
            return
        info = self.execution.stat()
        if not self.execution.is_dir() or info.st_uid != self.uid or info.st_mode & 0o077:
            raise RuntimeError(f'扫描执行目录必须归部署用户所有且权限为 0700：{self.execution}')
        children = list(self.execution.iterdir())
        if self.execution != Path(f'/tmp/skyshield-{self.uid}-engines') and children:
            if not all(child.is_dir() and not child.is_symlink() and child.name.startswith('run-') and
                       (child / 'runtime/context').is_dir() for child in children):
                raise RuntimeError(f'自定义扫描执行目录包含无法确认归属的内容，拒绝清理：{self.execution}')
        for pid, info in self.snapshot_processes().items():
            uses_directory = inside(Path(info['cwd']), self.execution)
            uses_directory |= any(info['args'][index:index + 2] == ['--execution-root', str(self.execution)]
                                  for index in range(len(info['args']) - 1))
            if uses_directory and pid not in self.processes:
                raise RuntimeError(f'扫描执行目录正被其他进程 PID {pid} 使用，未执行卸载。')

    def collect_updaters(self):
        for name, mode in (('updater', 'docker'), ('updater-local', 'local')):
            path = self.root / '.local' / name
            try:
                record = read_json(path / 'daemon.json', {})
            except PermissionError:
                if self.args.mode == 'local' and name == 'updater':
                    continue
                raise
            mode = record.get('mode', mode)
            if mode not in (('local', 'docker') if self.args.mode == 'all' else (self.args.mode,)):
                # Early local updaters shared this directory with Docker.
                # A Docker purge must still remove its installed-image pin.
                if self.args.purge and self.docker_enabled and name == 'updater':
                    self.paths.append(path / 'compose.release.json')
                continue
            pid = record.get('pid')
            if type(pid) is int and pid > 1:
                info = process_info(pid)
                if info and info['start'] == str(record.get('startTicks')):
                    expected = [str(self.root / 'scripts/system-update.py'), 'serve', '--mode', mode]
                    if record.get('root') != str(self.root) or info['args'][1:5] != expected:
                        raise RuntimeError('旧更新进程身份不匹配，未执行卸载。')
                    self.processes[pid] = {**info, 'kind': 'updater'}
            if self.args.purge:
                if name == 'updater' and self.args.mode == 'local' and (path / 'compose.release.json').exists():
                    self.notes.append('旧 updater 目录同时包含 Docker 版本配置，已保留该共用目录；清除两种部署可用 --mode all。')
                else:
                    self.paths.append(path)

    def collect_docker(self):
        if not shutil.which('docker'):
            markers = [self.root / '.local' / name for name in ('secrets', 'network', 'updater')]
            if self.args.mode == 'docker' or any(path.exists() for path in markers):
                raise RuntimeError('需要 Docker CLI 才能验证并清理 Docker 部署；本次没有删除数据或密钥。')
            self.docker_enabled = False
            self.notes.append('未安装 Docker CLI，未发现 Docker 部署文件，跳过 Docker。')
            return
        if self.docker('info', '--format', '{{.ID}}', check=False).returncode:
            raise RuntimeError('无法连接 Docker daemon，不能确认残留数据卷；本次没有删除数据或密钥。仅卸载本地部署可指定 --mode local。')
        all_ids = self.docker('ps', '--all', '--quiet').stdout.split()
        containers = json.loads(self.docker('inspect', *all_ids).stdout) if all_ids else []
        all_volumes = set(self.docker('volume', 'ls', '--quiet').stdout.split())
        labelled = set(self.docker('volume', 'ls', '--quiet', '--filter', 'label=com.docker.compose.project=' + self.project).stdout.split())
        candidates = ((FIXED_VOLUMES | {self.project + '_' + name for name in COMPOSE_VOLUMES}) & all_volumes) | labelled
        volumes = []
        for name in sorted(candidates):
            info = json.loads(self.docker('volume', 'inspect', name).stdout)[0]
            owner = (info.get('Labels') or {}).get('com.docker.compose.project')
            if owner and owner != self.project:
                if self.args.purge:
                    raise RuntimeError(f'同名卷 {name} 属于其他项目 {owner}，未执行卸载；请先处理该项目的部署。')
                continue
            volumes.append(info)
        names = {info['Name'] for info in volumes}
        volume_points = [Path(info['Mountpoint']) for info in volumes if info.get('Mountpoint')]
        points = [Path(info['Mountpoint']) for info in volumes if info['Name'] == 'lunafox_engine_execution' and info.get('Mountpoint')]
        for info in containers:
            owner = (info.get('Config', {}).get('Labels') or {}).get('com.docker.compose.project')
            mounts = info.get('Mounts', [])
            targets = {mount.get('Destination') for mount in mounts}
            engine = (bool(targets & {'/run/lunafox/context', '/run/lunafox/context/execution.pb'}) and
                      bool(targets & {'/run/lunafox/credential', '/run/lunafox/credential/token'}) and
                      bool(targets & {'/run/lunafox/socket', '/run/lunafox/socket/engine.sock'}))
            binding = any(mount.get('Name') in names or any(inside(Path(mount.get('Source', '/')), point) for point in points)
                          for mount in mounts)
            if owner == self.project or (not owner and engine and binding):
                self.containers.append(info)
        selected = {info['Id'] for info in self.containers}
        if self.args.purge:
            for info in containers:
                if info['Id'] not in selected and any(mount.get('Name') in names or
                        any(inside(Path(mount.get('Source', '/')), point) for point in volume_points)
                        for mount in info.get('Mounts', [])):
                    raise RuntimeError(f'项目数据卷仍被其他容器 {info.get("Name", info["Id"])} 使用，未执行卸载。')
            self.volumes = [info['Name'] for info in volumes]
        network_ids = set(self.docker('network', 'ls', '--quiet', '--filter', 'label=com.docker.compose.project=' + self.project).stdout.split())
        existing = self.docker('network', 'inspect', self.project + '_default', check=False)
        if existing.returncode == 0:
            info = json.loads(existing.stdout)[0]
            owner = (info.get('Labels') or {}).get('com.docker.compose.project')
            if not owner or owner == self.project:
                network_ids.add(info['Id'])
        for network_id in sorted(network_ids):
            info = json.loads(self.docker('network', 'inspect', network_id).stdout)[0]
            if set(info.get('Containers') or {}) - selected:
                raise RuntimeError(f'项目网络 {info["Name"]} 仍被其他容器使用，未执行卸载。')
            if info['Name'] not in self.networks:
                self.networks.append(info['Name'])
        if self.args.remove_images:
            for line in self.docker('image', 'ls', '--format', '{{json .}}').stdout.splitlines():
                info = json.loads(line)
                if info.get('Repository', '').removeprefix('docker.io/') in IMAGE_REPOSITORIES and info.get('Tag') != '<none>':
                    self.images.append(info['Repository'] + ':' + info['Tag'])
        if self.args.purge:
            self.paths += [self.root / '.local' / name for name in ('network', 'secrets')]

    def collect(self):
        if (self.root / '.local').is_symlink():
            raise RuntimeError('.local 不能是符号链接。')
        if self.local:
            self.validate_directory(self.data, runtime=self.args.purge)
            self.validate_directory(self.execution)
            if inside(self.data, self.execution) or inside(self.execution, self.data):
                raise RuntimeError('本地数据目录和扫描执行目录不能互相包含。')
            self.processes.update(self.local_processes())
            if self.args.purge:
                self.validate_execution()
                self.paths += [self.execution, self.data]
                frontend = self.root / 'frontend'
                self.paths += [frontend / name for name in ('.next', 'node_modules', '.pnpm-store')]
                for pattern in ('.next-skyshield-stage-*', 'tsconfig..next-skyshield-stage-*.json',
                                'tsconfig..next-skyshield-stage-*.tsbuildinfo'):
                    self.paths += sorted(frontend.glob(pattern))
                self.paths.append(frontend / 'tsconfig.tsbuildinfo')
        if self.docker_enabled:
            self.collect_docker()
        self.collect_updaters()
        if self.args.remove_env:
            for directory in (self.root, self.root / 'server', self.root / 'frontend'):
                self.paths += [directory / '.env', directory / '.env.local']
                self.paths += sorted(directory.glob('.env.*.local'))
        # No symlinked parent may redirect a known cache/config deletion.
        for path in self.paths:
            if path.parent.resolve() != path.parent:
                raise RuntimeError(f'清理路径的父目录不能是符号链接：{path}')
        self.paths = list(dict.fromkeys(path for path in self.paths if path.exists() or path.is_symlink()))
        return self

    def show(self):
        mode = '彻底清除：永久删除业务数据、密钥和部署状态' if self.args.purge else '保留数据：停止进程、移除容器和项目网络'
        print(f'卸载范围：{self.args.mode}；{mode}。')
        for info in self.processes.values():
            print(f'  停止进程：{info["kind"]} (PID {info["pid"]})')
        for info in self.containers:
            print(f'  移除容器：{info.get("Name", info["Id"]).lstrip("/")}')
        for label, values in (('删除网络', self.networks), ('删除数据卷', self.volumes),
                              ('删除镜像标签', self.images), ('删除路径', self.paths)):
            for value in values:
                print(f'  {label}：{value}')
        for note in self.notes:
            print(note)
        if self.args.dry_run:
            print('预览完成，未停止进程或修改任何文件、容器、数据卷。')

    def stop_processes(self, infos):
        pending = []
        for info in infos:
            current = process_info(info['pid'])
            if current and current['start'] == info['start'] and current['uid'] == info['uid']:
                with contextlib.suppress(ProcessLookupError):
                    os.kill(info['pid'], signal.SIGINT if info['kind'] == 'postgres' else signal.SIGTERM)
                pending.append(info)
        deadline = time.monotonic() + 30
        while pending and time.monotonic() < deadline:
            pending = [info for info in pending if (current := process_info(info['pid'])) and current['start'] == info['start']]
            if pending:
                time.sleep(.1)
        if pending:
            raise RuntimeError('进程未能停止，未删除数据或密钥；请先检查 PID：' + ', '.join(str(info['pid']) for info in pending))

    def apply(self):
        with contextlib.ExitStack() as stack:
            local_root = self.root / '.local'
            local_root.mkdir(exist_ok=True, mode=0o700)
            locks = [local_root / 'deployment.lock']
            if self.local:
                locks += [self.data / 'local-deploy.lock', self.data / 'tools/.install.lock']
            for path in locks:
                if not path.parent.exists():
                    continue
                lock = stack.enter_context(os.fdopen(os.open(path, os.O_CREAT | os.O_WRONLY | os.O_NOFOLLOW, 0o600), 'a'))
                fcntl.flock(lock, fcntl.LOCK_EX | fcntl.LOCK_NB)
            # Refresh the plan under the same locks as deploy/update. Failed
            # Docker operations must leave the credentials available for retry.
            refreshed = Uninstaller(self.root, self.args).collect()
            self.stop_processes([info for info in refreshed.processes.values() if info['kind'] == 'updater'])
            self.stop_processes([info for info in refreshed.processes.values() if info['kind'] not in ('updater', 'postgres')])
            self.stop_processes([info for info in refreshed.processes.values() if info['kind'] == 'postgres'])
            priority = {'agent': 0, 'nginx': 2, 'frontend': 3, 'server': 4, 'postgres': 9}
            for info in sorted(refreshed.containers, key=lambda value: priority.get((value.get('Config', {}).get('Labels') or {}).get('com.docker.compose.service'), 1)):
                # Agent shutdown can automatically remove its engine children.
                if not self.docker('ps', '--all', '--quiet', '--filter', 'id=' + info['Id']).stdout.strip():
                    continue
                if info.get('State', {}).get('Running'):
                    self.docker('stop', '--time', '30', info['Id'])
                self.docker('rm', info['Id'])
            for name in refreshed.networks:
                self.docker('network', 'rm', name)
            for name in refreshed.volumes:
                self.docker('volume', 'rm', name)
            for name in refreshed.images:
                self.docker('image', 'rm', name)
            if self.local and self.local_processes():
                raise RuntimeError('仍发现本地运行进程，未删除数据；请停止后重试。')
            for path in refreshed.paths:
                if path.is_symlink() or path.is_file():
                    path.unlink(missing_ok=True)
                elif path.exists():
                    shutil.rmtree(path)
            # Keep the root lock inode, so a concurrent deployment cannot open
            # a different lock while this uninstall is still completing.
        print('彻底清除完成。现在可执行 ./local.sh start 或 ./docker.sh pull 重新部署。' if self.args.purge
              else '卸载完成，业务数据与密钥已保留。重新部署时请继续使用当前目录。')


def main(argv=None):
    parser = argparse.ArgumentParser(description='卸载 SkyShield 本地/Docker 部署；默认保留数据。')
    parser.add_argument('--mode', choices=('local', 'docker', 'all'), default='all', help='默认同时检查两种部署')
    parser.add_argument('--purge', action='store_true', help='永久删除数据库、密钥、扫描数据、更新备份与本地构建/依赖')
    parser.add_argument('--yes', '--confirm', action='store_true', help='确认永久删除，--purge 执行时必需')
    parser.add_argument('--dry-run', action='store_true', help='只显示清理范围，不做任何变更')
    parser.add_argument('--remove-env', action='store_true', help='同时删除项目 .env / .env.*.local（需要 --purge）')
    parser.add_argument('--remove-images', action='store_true', help='同时删除 SkyShield 应用镜像标签（需要 --purge 和 Docker 模式）')
    parser.add_argument('--local-data-dir', help='原本地部署的数据目录，默认读取 SKYSHIELD_LOCAL_DATA_DIR')
    parser.add_argument('--execution-root', help='原扫描执行目录，默认读取 SKYSHIELD_LOCAL_EXECUTION_ROOT')
    parser.add_argument('--project-name', help='Docker Compose 项目名，默认 tianqiong 或 COMPOSE_PROJECT_NAME')
    args = parser.parse_args(argv)
    if args.purge and not args.yes and not args.dry_run:
        parser.error('--purge 会永久删除业务数据和全部密钥，执行时需要显式添加 --yes；可先用 --dry-run 预览。')
    if (args.remove_env or args.remove_images) and not args.purge:
        parser.error('--remove-env / --remove-images 需要同时指定 --purge。')
    if args.remove_images and args.mode == 'local':
        parser.error('--remove-images 需要 --mode docker 或 --mode all。')
    try:
        plan = Uninstaller(ROOT, args).collect()
        plan.show()
        if not args.dry_run:
            plan.apply()
    except BlockingIOError:
        print('卸载失败：部署、升级或工具安装正在进行，请稍后重试。', file=sys.stderr)
        return 1
    except (OSError, RuntimeError, ValueError, KeyError, TypeError, subprocess.TimeoutExpired) as error:
        print(f'卸载失败：{error}', file=sys.stderr)
        return 1
    return 0


if __name__ == '__main__':
    raise SystemExit(main())
