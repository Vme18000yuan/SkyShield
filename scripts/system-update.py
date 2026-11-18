#!/usr/bin/env python3
"""SkyShield host updater: fixed release source, private socket, durable operation.

This process runs on the deployment host, independently of Server/Frontend.
Only a published stable descriptor can supply a target. Never accept commands,
paths, repository URLs or image references from the HTTP client.
"""
import argparse
import contextlib
import datetime
import fcntl
import hashlib
import http.client
import http.server
import importlib.util
import json
import os
from pathlib import Path
import re
import shutil
import signal
import socket
import socketserver
import stat
import subprocess
import sys
import tempfile
import threading
import time
import urllib.error
import urllib.request
import uuid

ROOT = Path(__file__).resolve().parent.parent
STATE = ROOT / '.local/updater'
SOCKET_DIR = STATE / 'socket'
SOCKET = SOCKET_DIR / 'control.sock'
REPOSITORY = 'https://github.com/Vme18000yuan/SkyShield.git'
CHANNEL = 'https://raw.githubusercontent.com/Vme18000yuan/SkyShield/main/updates/stable.json'
COMPONENTS = ('server', 'frontend', 'bootstrap', 'nginx')
ACTIVE = {'queued', 'preparing', 'backing_up', 'updating', 'restarting', 'verifying'}
MODE = None
mutex = threading.RLock()
check_mutex = threading.Lock()


def select_state(mode):
    global STATE, SOCKET_DIR, SOCKET
    STATE = ROOT / ('.local/updater-local' if mode == 'local' else '.local/updater')
    SOCKET_DIR = STATE / 'socket'
    SOCKET = SOCKET_DIR / 'control.sock'


def select_cli_mode(explicit):
    if explicit:
        return explicit
    available = []
    for mode, relative in (('local', '.local/updater-local/socket/control.sock'), ('docker', '.local/updater/socket/control.sock')):
        path = ROOT / relative
        try:
            if path.is_socket() and os.access(path, os.R_OK | os.W_OK):
                available.append(mode)
        except PermissionError:
            continue
    if len(available) > 1:
        raise RuntimeError('同时存在本地和 Docker 更新进程，请用 --mode local 或 --mode docker 指定。')
    return available[0] if available else 'docker'


def now():
    return datetime.datetime.now(datetime.timezone.utc).isoformat()


def private_json(path, value):
    if path.is_symlink():
        raise RuntimeError('状态文件不能是符号链接。')
    fd, temporary = tempfile.mkstemp(prefix='.' + path.name, dir=path.parent)
    try:
        with os.fdopen(fd, 'w') as out:
            json.dump(value, out, ensure_ascii=False, indent=2)
            out.write('\n'); out.flush(); os.fsync(out.fileno())
        os.replace(temporary, path)
    finally:
        Path(temporary).unlink(missing_ok=True)


def read_json(path, default=None):
    try:
        return json.loads(path.read_text())
    except FileNotFoundError:
        return default


def prepare_state():
    for path in (ROOT / '.local', STATE, SOCKET_DIR):
        if path.is_symlink():
            raise RuntimeError('更新目录不能是符号链接。')
        path.mkdir(exist_ok=True, mode=0o700)
    STATE.chmod(0o700); SOCKET_DIR.chmod(0o700)


def command(args, *, cwd=ROOT, env=None, output=None, timeout=1800):
    # No shell interpolation. Output is private and never returned through HTTP.
    if args[0] == 'git':args = ['git', '-c', 'safe.directory=' + str(ROOT), *args[1:]]
    process = subprocess.Popen([str(value) for value in args], cwd=cwd, env=env,
                               stdout=output if output else subprocess.PIPE,
                               stderr=subprocess.STDOUT if output else subprocess.PIPE,
                               text=output is None, start_new_session=True)
    try:
        stdout, _ = process.communicate(timeout=timeout)
    except subprocess.TimeoutExpired:
        with contextlib.suppress(ProcessLookupError):os.killpg(process.pid, signal.SIGTERM)
        try:process.wait(timeout=10)
        except subprocess.TimeoutExpired:
            with contextlib.suppress(ProcessLookupError):os.killpg(process.pid, signal.SIGKILL)
            process.wait()
        raise RuntimeError('更新步骤超时，已停止该步骤，请查看宿主机更新日志。') from None
    if process.returncode:
        raise RuntimeError('步骤失败：' + Path(str(args[0])).name + '（退出码 ' + str(process.returncode) + '），请查看宿主机更新日志。')
    return stdout.strip() if output is None else ''


def native():
    spec = importlib.util.spec_from_file_location('skyshield_native_deploy', ROOT / 'scripts/local-deploy.py')
    module = importlib.util.module_from_spec(spec); spec.loader.exec_module(module)
    return module


def deployment_environment():
    spec = importlib.util.spec_from_file_location('skyshield_network_settings', ROOT / 'scripts/deployment-network.py')
    module = importlib.util.module_from_spec(spec); spec.loader.exec_module(module)
    configured = module.settings(ROOT)
    environment = os.environ.copy()
    for key in module.PROXY_KEYS:
        if key in configured:environment[key] = configured[key]
    return environment


def compose():
    args = ['docker', 'compose', '-f', str(ROOT / 'compose.tianqiong.yaml'), '-f', str(ROOT / 'compose.hub.yaml')]
    # Apply image pins after the generated network profile (which also has an image).
    for path in (ROOT / '.local/network/compose.network.yaml', STATE / 'compose.release.json'):
        if path.is_file():
            args += ['-f', str(path)]
    return args


def service_id(service):
    ids = command(compose() + ['ps', '--all', '--quiet', service]).splitlines()
    if len(ids) != 1:
        raise RuntimeError('没有找到当前部署的唯一容器：' + service)
    info = json.loads(command(['docker', 'inspect', ids[0]]))[0]
    directory = info['Config'].get('Labels', {}).get('com.docker.compose.project.working_dir')
    if not directory or Path(directory).resolve() != ROOT:
        raise RuntimeError('Docker 容器属于其他部署目录，已停止操作。')
    return ids[0]


def current_revision():
    if MODE == 'local':
        deployment = native()
        process = deployment.read_state().get('server')
        if deployment.alive(process):
            try:
                for item in Path('/proc', str(process['pid']), 'environ').read_bytes().split(b'\0'):
                    if item.startswith(b'SKYSHIELD_BUILD_REVISION='):
                        return item.split(b'=', 1)[1].decode()
            except OSError:
                pass
        return ''
    try:
        revisions = []
        for name in COMPONENTS:
            info = json.loads(command(['docker', 'inspect', service_id(name)]))[0]
            revisions.append(info['Config'].get('Labels', {}).get('org.opencontainers.image.revision', ''))
        return revisions[0] if len(set(revisions)) == 1 else ''
    except (RuntimeError, OSError):
        return ''


def validate_manifest(value):
    allowed = {'schemaVersion', 'version', 'revision', 'publishedAt', 'notes', 'images', 'minimumUpdaterVersion'}
    if not isinstance(value, dict) or set(value) != allowed or value['schemaVersion'] != 1 or value['minimumUpdaterVersion'] != 1:
        raise RuntimeError('发布清单版本不兼容，请更新部署脚本。')
    if not re.fullmatch(r'[0-9a-f]{40}', str(value['revision'])):
        raise RuntimeError('发布清单的源码提交无效。')
    if not re.fullmatch(r'[A-Za-z0-9][A-Za-z0-9.+-]{0,63}', str(value['version'])):
        raise RuntimeError('发布版本标识无效。')
    if not isinstance(value['notes'], str) or len(value['notes']) > 16000:
        raise RuntimeError('发布说明无效。')
    datetime.datetime.fromisoformat(value['publishedAt'].replace('Z', '+00:00'))
    if not isinstance(value['images'], dict) or set(value['images']) != set(COMPONENTS):
        raise RuntimeError('发布清单缺少镜像。')
    for name, image in value['images'].items():
        if not isinstance(image, str) or not re.fullmatch(r'docker\.io/solder110/skyshield-' + name + r'@sha256:[0-9a-f]{64}', image):
            raise RuntimeError('发布镜像必须绑定 SkyShield 仓库的不可变摘要。')
    return value


class ReleaseRedirect(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, req, fp, code, msg, headers, newurl):
        # The fixed raw GitHub channel does not require cross-origin redirects.
        if not newurl.startswith('https://raw.githubusercontent.com/Vme18000yuan/SkyShield/'):
            raise RuntimeError('发布源发生了不受支持的重定向。')
        return super().redirect_request(req, fp, code, msg, headers, newurl)


def check_release():
    with check_mutex:
        try:
            request = urllib.request.Request(CHANNEL, headers={'Accept': 'application/json', 'User-Agent': 'SkyShield-Updater/1', 'Cache-Control': 'no-cache'})
            with urllib.request.build_opener(ReleaseRedirect()).open(request, timeout=20) as response:
                raw = response.read(65537)
            if len(raw) > 65536:
                raise RuntimeError('发布清单过大。')
            manifest = validate_manifest(json.loads(raw))
            result = {'checkState': 'checked', 'message': '', 'checkedAt': now(), 'checkedEpoch': time.time(),
                      'candidate': manifest, 'manifestDigest': 'sha256:' + hashlib.sha256(raw).hexdigest()}
        except urllib.error.HTTPError as error:
            if error.code == 404:
                result = {'checkState': 'no_release', 'message': '尚未发布支持自动更新的稳定版本。', 'checkedAt': now()}
            else:
                result = {'checkState': 'check_failed', 'message': '发布源返回 HTTP ' + str(error.code) + '，请稍后重试。', 'checkedAt': now()}
        except (OSError, ValueError, RuntimeError) as error:
            result = {'checkState': 'check_failed', 'message': str(error) if isinstance(error, RuntimeError) else '无法连接或解析 SkyShield 发布源，请检查部署机网络和代理。', 'checkedAt': now()}
        with mutex:
            private_json(STATE / 'check.json', result)
        return status()


def eligibility():
    if not (ROOT / '.git').is_dir():
        return False, '自动更新需要 Git 克隆部署；ZIP 部署请先按更新文档迁移为 Git 工作目录。'
    if not shutil.which('git'):
        return False, '部署机需要安装 Git。'
    if MODE == 'docker':
        info = json.loads(command(['docker', 'info', '--format', '{{json .}}'], timeout=15))
        if info.get('OSType') != 'linux' or info.get('Architecture') not in ('x86_64', 'amd64'):
            return False, '当前自动更新渠道只发布 Linux amd64 镜像。'
    if command(['git', 'status', '--porcelain', '--untracked-files=normal']):
        return False, '源码存在未提交修改，请先提交或备份修改后再更新。'
    if read_json(STATE / 'operation.json', {}).get('status') == 'needs_recovery':
        return False, '上一次更新需要恢复，请先按更新记录处理并执行 ./update.sh acknowledge-recovery。'
    return True, ''


def status():
    with mutex:
        checked = read_json(STATE / 'check.json', {'checkState': 'not_checked', 'message': ''})
        operation = read_json(STATE / 'operation.json')
        revision = current_revision()
        installed = read_json(STATE / 'installed.json', {})
        candidate = checked.get('candidate')
        eligible, reason = eligibility()
        active = bool(operation and operation['status'] in ACTIVE)
        return {**{key: value for key, value in checked.items() if key != 'checkedEpoch'},
                'currentVersion': installed['version'] if revision and installed.get('revision') == revision else (revision[:12] if revision else 'unknown'), 'currentRevision': revision,
                'mode': MODE, 'updaterReady': True, 'hasUpdate': bool(candidate and candidate['revision'] != revision),
                'eligible': eligible and not active, 'eligibilityReason': reason, 'operation': operation}


def set_stage(operation, stage, message):
    with mutex:
        operation.update(status=stage, message=message, updatedAt=now())
        private_json(STATE / 'operation.json', operation)
        directory = STATE / ('operation-' + operation['id'])
        if directory.is_dir():private_json(directory / 'operation.json', operation)


def query(sql):
    if MODE == 'local':
        deployment = native()
        return deployment.query(deployment.environment(), sql)
    return command(['docker', 'exec', service_id('postgres'), 'psql', '-X', '-U', 'tianqiong', '-d', 'tianqiong', '-Atc', sql])


def ensure_idle():
    sql = """SELECT
      (SELECT count(*) FROM scan_task WHERE status IN ('pending','running')) +
      (SELECT count(*) FROM ai_run WHERE status IN ('running','stopping') AND lease_until > clock_timestamp()) +
      (SELECT count(*) FROM ai_task WHERE (queued OR status='running') AND deleted_at IS NULL)
    """
    if query(sql).strip() != '0':
        raise RuntimeError('存在扫描或 AI 任务，请等待任务结束或在界面停止任务后再更新。')


def stop_applications(log):
    if MODE == 'docker':
        # Keep database/cache/helper alive until backup and initialization finish.
        for name in ('agent', 'nginx', 'frontend', 'server'):
            command(['docker', 'stop', '--time', '60', service_id(name)], output=log)
    else:
        deployment = native()
        with (deployment.DATA / 'local-deploy.lock').open('a') as lock:
            fcntl.flock(lock, fcntl.LOCK_EX | fcntl.LOCK_NB)
            state = deployment.read_state()
            for name in ('agent', 'frontend', 'server'):
                deployment.stop_managed(state, name)


def backup_database(directory, log):
    target = directory / 'database.dump'
    if MODE == 'local':
        deployment = native();env = deployment.environment()
        args = [deployment.pg_binary('pg_dump'), '-h', '127.0.0.1', '-p', str(deployment.PG_PORT), '-U', 'postgres', '-d', deployment.DB_NAME, '-Fc']
    else:
        env = None;args = ['docker', 'exec', service_id('postgres'), 'pg_dump', '-U', 'tianqiong', '-d', 'tianqiong', '-Fc']
    with target.open('xb') as output:
        target.chmod(0o600)
        result = subprocess.run(args, env=env, stdout=output, stderr=log, timeout=1800)
        output.flush();os.fsync(output.fileno())
    if result.returncode or target.stat().st_size < 8:
        raise RuntimeError('数据库备份失败，未执行数据库迁移。')
    with target.open('rb') as source:
        if source.read(5) != b'PGDMP':
            raise RuntimeError('数据库备份格式无效。')
    for name in ('.env',):
        source = ROOT / name
        if source.is_file():shutil.copy2(source, directory / name)
    secrets = native().SECRETS if MODE == 'local' else ROOT / '.local/secrets'
    shutil.copytree(secrets, directory / 'secrets')
    if MODE == 'local':
        deployment = native()
        def retain_binary(source, target):
            try:os.link(source, target)
            except OSError:shutil.copy2(source, target)
            return target
        shutil.copytree(deployment.BIN, directory / 'bin', copy_function=retain_binary, symlinks=True)
        for name in ('frontend-build.json', 'build-revision.json'):
            source = deployment.DATA / name
            if source.is_file():shutil.copy2(source, directory / name)
    else:
        images = {name: json.loads(command(['docker', 'inspect', service_id(name)]))[0]['Image'] for name in COMPONENTS}
        private_json(directory / 'previous-images.json', images)
        if (STATE / 'compose.release.json').is_file():shutil.copy2(STATE / 'compose.release.json', directory / 'compose.release.json')


def image_overlay(manifest):
    services = {name: {'image': manifest['images'][name]} for name in COMPONENTS}
    for name in ('migrate', 'volume-preflight', 'cert-init'):
        services[name] = {'image': manifest['images']['bootstrap']}
    if (ROOT / '.local/network/compose.network.yaml').is_file():
        services['network-helper'] = {'image': manifest['images']['bootstrap']}
    return {'services': services}


def verify_runtime(manifest, log):
    if MODE == 'local':
        deployment = native()
        deployment.status(None)
        if not deployment.http_ready(deployment.HTTP_PORT, '/healthChecks/readiness') or not deployment.http_ready(deployment.FRONTEND_PORT, '/'):
            raise RuntimeError('本地服务健康检查未通过。')
        return
    deadline = time.monotonic() + 180
    while time.monotonic() < deadline:
        healthy = True
        for name in COMPONENTS:
            info = json.loads(command(['docker', 'inspect', service_id(name)]))[0]
            state = info['State']
            if name == 'bootstrap':
                healthy &= state['Status'] == 'exited' and state['ExitCode'] == 0
            else:
                healthy &= state['Running'] and state.get('Health', {}).get('Status', 'healthy') == 'healthy'
            expected = json.loads(command(['docker', 'image', 'inspect', manifest['images'][name]]))[0]['Id']
            healthy &= info['Image'] == expected
        for name in ('migrate', 'volume-preflight', 'cert-init'):
            state = json.loads(command(['docker', 'inspect', service_id(name)]))[0]['State']
            healthy &= state['Status'] == 'exited' and state['ExitCode'] == 0
        agent = json.loads(command(['docker', 'inspect', service_id('agent')]))[0]
        healthy &= agent['State']['Running']
        if healthy:
            count = query("SELECT count(*) FROM agent a JOIN agent_runtime_status s ON s.agent_id=a.id JOIN deployment_agent_bootstrap b ON b.instance_id=a.instance_id WHERE a.status='online' AND s.last_heartbeat>now()-interval '1 minute'")
            if count != '0':return
        time.sleep(3)
    raise RuntimeError('新版本服务、镜像或 Agent 健康检查未通过，请查看更新日志。')


def prune_frontend_builds(rollback):
    if MODE != 'local':return
    deployment = native();current = deployment.active_frontend_stage()
    previous = read_json(rollback / 'frontend-build.json', {}).get('stage')
    keep = {current, previous}
    for process in Path('/proc').glob('[0-9]*/environ'):
        try:
            if Path(os.readlink(process.parent/'cwd')).resolve() != ROOT/'frontend':continue
            for value in process.read_bytes().split(b'\0'):
                if value.startswith(b'SKYSHIELD_NEXT_STAGE_DIR='):keep.add(value.split(b'=',1)[1].decode())
        except OSError:pass
    # Only known generated stages; dependencies, runtime data and build pointers survive.
    for path in (ROOT / 'frontend').glob('.next-skyshield-stage-*'):
        if path.name in keep or path.is_symlink() or not path.is_dir():continue
        if not re.fullmatch(r'\.next-skyshield-stage-[A-Za-z0-9-]+', path.name):continue
        shutil.rmtree(path)
        (path.parent / ('tsconfig.' + path.name + '.json')).unlink(missing_ok=True)


def execute_update(operation, manifest):
    directory = STATE / ('operation-' + operation['id']);directory.mkdir(mode=0o700)
    maintenance = SOCKET_DIR / 'maintenance'
    stopped = switched = False
    try:
        with (STATE / 'update.lock').open('a') as update_lock, (ROOT / '.local/deployment.lock').open('a') as deployment_lock, (directory / 'update.log').open('ab') as log:
            fcntl.flock(update_lock, fcntl.LOCK_EX | fcntl.LOCK_NB)
            fcntl.flock(deployment_lock, fcntl.LOCK_EX | fcntl.LOCK_NB)
            set_stage(operation, 'preparing', '检查工作目录、空间和发布内容')
            eligible, reason = eligibility()
            if not eligible:raise RuntimeError(reason)
            database_bytes = int(query('SELECT pg_database_size(current_database())'))
            if shutil.disk_usage(ROOT).free < database_bytes + 10 * 1024**3:
                raise RuntimeError('磁盘可用空间不足以保存数据库备份并预留 10 GiB 构建空间，请先清理或扩容。')
            ensure_idle()
            previous = command(['git', 'rev-parse', 'HEAD'])
            command(['git', 'fetch', '--no-tags', REPOSITORY, manifest['revision']], output=log, timeout=300)
            if command(['git', 'rev-parse', 'FETCH_HEAD']) != manifest['revision']:
                raise RuntimeError('获取的源码提交与发布清单不一致。')
            source_is_current = previous == manifest['revision']
            if not source_is_current:
                forward = subprocess.run(['git', '-c', 'safe.directory='+str(ROOT), 'merge-base', '--is-ancestor', previous, manifest['revision']], cwd=ROOT, stdout=log, stderr=log).returncode
                if forward:
                    # The stable descriptor is committed AFTER its source/images.
                    # A checkout differing only in channel metadata is the same source.
                    command(['git', 'merge-base', '--is-ancestor', manifest['revision'], previous], output=log)
                    difference = command(['git', 'diff', '--name-only', manifest['revision'], previous, '--', '.', ':(exclude)updates'])
                    if difference:raise RuntimeError('当前源码不是目标版本的祖先；请先处理分支或本地提交，避免自动降级。')
                    source_is_current = True
            if previous == manifest['revision'] and current_revision() == manifest['revision']:
                raise RuntimeError('当前已运行目标版本。')
            private_json(directory / 'target.json', manifest)
            private_json(directory / 'previous.json', {'revision': previous, 'mode': MODE})
            if MODE == 'docker':
                for image in manifest['images'].values():
                    command(['docker', 'pull', image], output=log)
                    info = json.loads(command(['docker', 'image', 'inspect', image]))[0]
                    if info['Config'].get('Labels', {}).get('org.opencontainers.image.revision') != manifest['revision']:
                        raise RuntimeError('镜像源码版本与发布清单不一致。')
                # Preserve access to the currently installed infrastructure images.
                for service in ('postgres', 'redis', 'loki', 'agent'):
                    info = json.loads(command(['docker', 'inspect', service_id(service)]))[0]
                    command(['docker', 'image', 'inspect', info['Image']])
            # Do not proceed if an operator edited the checkout while downloads ran.
            if command(['git', 'status', '--porcelain', '--untracked-files=normal']) or command(['git', 'rev-parse', 'HEAD']) != previous:
                raise RuntimeError('下载期间源码发生变更，已停止更新。')
            private_json(maintenance, {'operationId': operation['id']})
            ensure_idle()
            set_stage(operation, 'backing_up', '停止应用并备份数据库、密钥和回滚信息')
            stopped = True;stop_applications(log)
            backup_database(directory, log)
            command(['git', 'update-ref', 'refs/skyshield/rollback', previous], output=log)
            set_stage(operation, 'updating', '安装已发布版本')
            if not source_is_current:
                command(['git', '-c', 'core.hooksPath=/dev/null', 'merge', '--ff-only', manifest['revision']], output=log)
            switched = True
            env = os.environ.copy();env['SKYSHIELD_UPDATER_CHILD'] = '1'
            if MODE == 'docker':
                private_json(STATE / 'compose.release.json', image_overlay(manifest))
                env['SKYSHIELD_IMAGE_TAG'] = 'sha-' + manifest['revision'][:12]
                command(['bash', ROOT / 'docker.sh', 'pull'], env=env, output=log, timeout=3600)
            else:
                command(['pnpm', 'install', '--frozen-lockfile'], cwd=ROOT / 'frontend', env=env, output=log)
                command(['bash', ROOT / 'local.sh', 'start', '--no-tools-install'], env=env, output=log, timeout=7200)
            set_stage(operation, 'verifying', '检查应用、迁移和 Agent 运行状态')
            verify_runtime(manifest, log)
            installed = {'revision': manifest['revision'], 'version': manifest['version'], 'installedAt': now(), 'mode': MODE}
            private_json(STATE / 'installed.json', installed)
            if MODE == 'local':private_json(native().DATA / 'build-revision.json', {'revision': manifest['revision']})
            prune_frontend_builds(directory)
            for previous_backup in STATE.glob('operation-*'):
                if previous_backup == directory or previous_backup.is_symlink() or not previous_backup.is_dir():continue
                if read_json(previous_backup / 'operation.json', {}).get('status') == 'succeeded':
                    shutil.rmtree(previous_backup)
            operation.update(status='succeeded', message='更新完成，已保留更新前的数据库和版本备份', updatedAt=now())
    except Exception as error:
        message = str(error) if isinstance(error, RuntimeError) else '更新步骤失败，请查看宿主机操作日志。'
        # Once stopped, schema/runtime may be partially changed. Never pretend a
        # binary rollback also rolls back data. Keep artifacts for explicit recovery.
        operation.update(status='needs_recovery' if stopped else 'failed', message=message, updatedAt=now())
    finally:
        maintenance.unlink(missing_ok=True)
        with mutex:
            operation['recoveryRequired'] = stopped and operation['status'] != 'succeeded'
            operation['completedAt'] = now();operation['sourceChanged'] = switched
            operation['logFile'] = str(directory / 'update.log')
            private_json(STATE / 'operation.json', operation)
            private_json(directory / 'operation.json', operation)
        if operation['status'] == 'succeeded':
            # Load the newly installed updater code after the operation is durable.
            def reload_when_idle():
                with mutex:
                    latest = read_json(STATE / 'operation.json', {})
                    if latest.get('id') == operation['id'] and latest.get('status') == 'succeeded':
                        os.execv(sys.executable, [sys.executable, str(ROOT / 'scripts/system-update.py'), 'serve', '--mode', MODE])
            threading.Timer(2, reload_when_idle).start()


def apply(request):
    if not isinstance(request, dict) or set(request) != {'requestId', 'manifestDigest', 'confirmed'} or request['confirmed'] is not True:
        raise RuntimeError('需要明确确认更新，并提供检查时的发布摘要。')
    if str(uuid.UUID(request['requestId'])) != request['requestId']:
        raise RuntimeError('请求编号无效。')
    with mutex:
        existing = read_json(STATE / 'operation.json', {})
        if existing.get('requestId') == request['requestId']:
            if existing.get('manifestDigest') != request['manifestDigest']:raise RuntimeError('重复请求的目标发生变化。')
            return status()
        if existing.get('status') in ACTIVE:raise RuntimeError('已有更新正在执行。')
        checked = read_json(STATE / 'check.json', {})
        if checked.get('checkState') != 'checked' or checked.get('manifestDigest') != request['manifestDigest'] or time.time()-checked.get('checkedEpoch',0) > 300:
            raise RuntimeError('版本检查已过期，请重新检查后确认更新。')
        eligible, reason = eligibility()
        if not eligible:raise RuntimeError(reason)
        if checked['candidate']['revision'] == current_revision():raise RuntimeError('当前已是发布版本。')
        operation = {'id': str(uuid.uuid4()), 'requestId': request['requestId'], 'manifestDigest': checked['manifestDigest'],
                     'version': checked['candidate']['version'], 'revision': checked['candidate']['revision'],
                     'status': 'queued', 'message': '更新已排队', 'createdAt': now(), 'updatedAt': now()}
        private_json(STATE / 'operation.json', operation)
        threading.Thread(target=execute_update, args=(operation, checked['candidate']), daemon=False).start()
        return status()


class ControlHandler(http.server.BaseHTTPRequestHandler):
    def log_message(self, *args):pass
    def do_GET(self):self.handle_request()
    def do_POST(self):self.handle_request()
    def handle_request(self):
        try:
            size = int(self.headers.get('Content-Length', '0'))
            if not 0 <= size <= 4096 or self.headers.get('Transfer-Encoding'):raise RuntimeError('请求格式无效。')
            payload = json.loads(self.rfile.read(size)) if size else {}
            if self.command == 'GET' and self.path == '/status':result = status()
            elif self.command == 'POST' and self.path == '/check' and payload == {}:result = check_release()
            elif self.command == 'POST' and self.path == '/apply':result = apply(payload)
            else:raise RuntimeError('不支持的更新操作。')
            code = 200
        except (RuntimeError, ValueError, KeyError, TypeError) as error:
            code = 409;result = {'error': {'code': 409, 'status': 'FAILED_PRECONDITION', 'message': str(error), 'details': []}}
        except Exception:
            code = 503;result = {'error': {'code': 503, 'status': 'UNAVAILABLE', 'message': '更新服务暂时不可用，请检查宿主机进程日志。', 'details': []}}
        raw = json.dumps(result, ensure_ascii=False).encode()
        self.send_response(code);self.send_header('Content-Type','application/json');self.send_header('Content-Length',str(len(raw)));self.end_headers()
        with contextlib.suppress(BrokenPipeError):self.wfile.write(raw)


class ControlServer(socketserver.ThreadingMixIn, socketserver.UnixStreamServer):
    daemon_threads = True


def rpc(path, payload=None):
    connection = http.client.HTTPConnection('localhost', timeout=45)
    connection.sock = socket.socket(socket.AF_UNIX, socket.SOCK_STREAM);connection.sock.settimeout(45);connection.sock.connect(str(SOCKET))
    try:
        connection.request('POST' if payload is not None else 'GET', path, json.dumps(payload) if payload is not None else None, {'Content-Type':'application/json'})
        response=connection.getresponse();result=json.loads(response.read(256*1024));return response.status,result
    finally:connection.close()


def serve(mode):
    global MODE
    MODE = mode;prepare_state();os.environ.update(deployment_environment())
    with (STATE / 'daemon.lock').open('a') as lock:
        fcntl.flock(lock, fcntl.LOCK_EX | fcntl.LOCK_NB)
        if SOCKET.exists() and not stat.S_ISSOCK(SOCKET.lstat().st_mode):
            raise RuntimeError('更新 socket 路径已被普通文件占用。')
        SOCKET.unlink(missing_ok=True)
        operation=read_json(STATE/'operation.json',{})
        if operation.get('status') in ACTIVE:
            operation.update(status='needs_recovery', message='宿主机更新进程曾中断，请检查备份与操作日志后恢复。',updatedAt=now())
            private_json(STATE/'operation.json',operation)
        (SOCKET_DIR/'maintenance').unlink(missing_ok=True)
        private_json(STATE/'daemon.json',{'pid':os.getpid(),'mode':mode,'root':str(ROOT),'startedAt':now(), 'startTicks':Path('/proc/self/stat').read_text().split(') ',1)[1].split()[19]})
        with ControlServer(str(SOCKET),ControlHandler) as server:
            SOCKET.chmod(0o600)
            def periodic():
                while True:
                    with contextlib.suppress(Exception):
                        if read_json(STATE/'operation.json',{}).get('status') not in ACTIVE:check_release()
                    time.sleep(3600)
            threading.Thread(target=periodic,daemon=True).start()
            server.serve_forever()


def start(mode):
    prepare_state()
    try:
        code,result=rpc('/status')
        if code==200:
            if result.get('mode')!=mode:raise RuntimeError('更新进程属于另一部署模式，请先停止该进程再启动。')
            return
    except (ConnectionError,OSError):pass
    with (STATE/'daemon.log').open('ab') as log:
        subprocess.Popen([sys.executable,str(Path(__file__).resolve()),'serve','--mode',mode],cwd=ROOT,stdin=subprocess.DEVNULL,stdout=log,stderr=log,start_new_session=True,close_fds=True)
    for _ in range(40):
        try:
            if rpc('/status')[0]==200:return
        except (ConnectionError,OSError):pass
        time.sleep(.25)
    raise RuntimeError('更新进程启动失败，请查看 .local/updater/daemon.log。')


def stop_daemon():
    code, result = rpc('/status')
    if code != 200 or result.get('operation', {}) and result['operation'].get('status') in ACTIVE:
        raise RuntimeError('更新正在执行或状态未知，不能停止更新进程。')
    record = read_json(STATE / 'daemon.json', {})
    process = Path('/proc', str(record.get('pid', 0)))
    if record.get('root') != str(ROOT) or record.get('startTicks') != (process/'stat').read_text().split(') ',1)[1].split()[19]:
        raise RuntimeError('更新进程身份不匹配，未发送停止信号。')
    os.kill(record['pid'], signal.SIGTERM)
    for _ in range(40):
        if not process.exists():return
        time.sleep(.1)
    raise RuntimeError('更新进程尚未退出。')


def install_service(mode):
    prepare_state()
    if not shutil.which('systemctl'):raise RuntimeError('此主机没有 systemd，请使用 ./update.sh start 或配置自己的进程管理器。')
    service_identity = str(ROOT) if mode == 'docker' else str(ROOT) + ':local'
    name = 'skyshield-updater-' + hashlib.sha256(service_identity.encode()).hexdigest()[:12] + '.service'
    user = [] if os.geteuid() == 0 else ['--user']
    directory = Path('/etc/systemd/system') if not user else Path.home()/'.config/systemd/user'
    directory.mkdir(parents=True, exist_ok=True)
    def quote(value):
        if '\n' in value or '\r' in value:raise RuntimeError('systemd 配置不能包含换行。')
        return '"' + value.replace('\\','\\\\').replace('"','\\"').replace('%','%%').replace('$','$$') + '"'
    environment = STATE/'service.env'
    keys = {'PATH','HTTP_PROXY','HTTPS_PROXY','ALL_PROXY','NO_PROXY','http_proxy','https_proxy','all_proxy','no_proxy','SKYSHIELD_GO','SSL_CERT_FILE','SSL_CERT_DIR'}
    values = {key:value for key,value in deployment_environment().items() if key in keys or key.startswith('SKYSHIELD_LOCAL_')}
    fd = os.open(environment, os.O_WRONLY|os.O_CREAT|os.O_TRUNC|os.O_NOFOLLOW, 0o600)
    with os.fdopen(fd,'w') as output:
        # EnvironmentFile has quoting but no systemd specifier/variable expansion.
        for key,value in values.items():
            if '\n' in value or '\r' in value:raise RuntimeError('环境变量不能包含换行。')
            escaped=value.replace('\\','\\\\').replace('"','\\"');output.write(key+'="'+escaped+'"\n')
    unit='[Unit]\nDescription=SkyShield deployment updater\nAfter=network-online.target\nWants=network-online.target\n\n[Service]\nType=simple\nWorkingDirectory='+quote(str(ROOT))+'\nEnvironmentFile='+quote(str(environment))+'\nExecStart='+quote(sys.executable)+' '+quote(str(ROOT/'scripts/system-update.py'))+' serve --mode '+mode+'\nRestart=on-failure\nRestartSec=5\nUMask=0077\nTimeoutStopSec=7200\n\n[Install]\nWantedBy='+('default.target' if user else 'multi-user.target')+'\n'
    target = directory/name
    if target.is_symlink():raise RuntimeError('已有 systemd unit 不能是符号链接。')
    target.write_text(unit);target.chmod(0o644)
    try:stop_daemon()
    except (ConnectionError, FileNotFoundError):pass
    command(['systemctl',*user,'daemon-reload'])
    command(['systemctl',*user,'enable','--now',name])
    print('已安装 systemd 服务：'+name)
    if user:print('若需退出登录后和开机时运行，请由管理员为该用户启用 loginctl enable-linger。')


def main():
    parser=argparse.ArgumentParser();parser.add_argument('action',choices=['start','serve','stop','install-service','status','check','apply','acknowledge-recovery'])
    parser.add_argument('--mode',choices=['local','docker']);parser.add_argument('--confirm',action='store_true')
    args=parser.parse_args()
    try:
        select_state(select_cli_mode(args.mode))
        if args.action in ('serve','start','install-service'):
            if not args.mode:raise RuntimeError('请用 --mode local 或 --mode docker 指定部署方式。')
            {'serve':serve,'start':start,'install-service':install_service}[args.action](args.mode);return 0
        if args.action=='stop':stop_daemon();return 0
        if args.action=='acknowledge-recovery':
            if not args.confirm:raise RuntimeError('确认已完成手工恢复后，使用 --confirm 解除恢复状态。')
            with (STATE/'update.lock').open('a') as lock:
                fcntl.flock(lock,fcntl.LOCK_EX|fcntl.LOCK_NB)
                operation=read_json(STATE/'operation.json',{})
                if operation.get('status')!='needs_recovery':raise RuntimeError('没有需要确认的恢复操作。')
                operation.update(status='failed',message='管理员已确认手工恢复，可重新检查更新。',recoveryAcknowledgedAt=now());private_json(STATE/'operation.json',operation)
            return 0
        if args.action=='apply':
            if not args.confirm:raise RuntimeError('请先检查版本；使用 ./update.sh apply --confirm 确认更新。')
            code,checked=rpc('/check',{})
            if code!=200 or not checked.get('candidate'):raise RuntimeError('没有可安装的发布版本。')
            code,result=rpc('/apply',{'requestId':str(uuid.uuid4()),'manifestDigest':checked['manifestDigest'],'confirmed':True})
        else:code,result=rpc('/check',{}) if args.action=='check' else rpc('/status')
        print(json.dumps(result,ensure_ascii=False,indent=2));return 0 if code==200 else 1
    except (OSError,RuntimeError,ValueError) as error:
        print('系统更新：'+str(error),file=sys.stderr);return 1


if __name__=='__main__':raise SystemExit(main())
