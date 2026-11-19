#!/usr/bin/env python3
"""Upgrade an existing SkyShield deployment from the host terminal."""

import argparse
import contextlib
import datetime
import fcntl
import importlib.util
import json
import os
from pathlib import Path
import re
import shutil
import signal
import subprocess
import sys
import tempfile
import time
import uuid

from release_source import COMPONENTS, REPOSITORY, latest_release_tag, published_images

ROOT = Path(__file__).resolve().parent.parent
MODE = None
STATE = None
ACTIVE = {"queued", "preparing", "backing_up", "updating", "restarting", "verifying"}
SECRET_NAMES = ("db_password", "jwt_secret", "ai_encryption_key")


def now():
    return datetime.datetime.now(datetime.timezone.utc).isoformat()


def private_json(path, value):
    if path.is_symlink():
        raise RuntimeError(f"状态文件不能是符号链接：{path}")
    fd, name = tempfile.mkstemp(prefix="." + path.name, dir=path.parent)
    temporary = Path(name)
    try:
        with os.fdopen(fd, "w", encoding="utf-8") as output:
            json.dump(value, output, ensure_ascii=False, indent=2)
            output.write("\n")
            output.flush()
            os.fsync(output.fileno())
        temporary.replace(path)
    finally:
        temporary.unlink(missing_ok=True)


def read_json(path, default=None):
    try:
        return json.loads(path.read_text(encoding="utf-8"))
    except FileNotFoundError:
        return default


def directory(path):
    if path.is_symlink():
        raise RuntimeError(f"更新目录不能是符号链接：{path}")
    path.mkdir(exist_ok=True, mode=0o700)


def state_path(mode):
    return ROOT / ".local" / ("updater-local" if mode == "local" else "updater")


def import_deployment(name):
    path = ROOT / "scripts" / (name + ".py")
    spec = importlib.util.spec_from_file_location(name, path)
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


def native():
    return import_deployment("local-deploy")


def command(args, *, cwd=None, env=None, output=None, log=None, timeout=1800):
    args = [str(value) for value in args]
    if args[0] == "git":
        args[1:1] = ["-c", "safe.directory=" + str(ROOT)]
    with subprocess.Popen(
        args, cwd=cwd or ROOT, env=env, text=True, start_new_session=True,
        stdout=output or log or subprocess.PIPE,
        stderr=log or subprocess.PIPE,
    ) as process:
        try:
            stdout, stderr = process.communicate(timeout=timeout)
        except (subprocess.TimeoutExpired, KeyboardInterrupt) as error:
            with contextlib.suppress(ProcessLookupError):
                os.killpg(process.pid, signal.SIGTERM)
            try:
                process.communicate(timeout=10)
            except subprocess.TimeoutExpired:
                with contextlib.suppress(ProcessLookupError):
                    os.killpg(process.pid, signal.SIGKILL)
                process.communicate()
            if isinstance(error, KeyboardInterrupt):
                raise
            raise RuntimeError(f"{args[0]} 执行超时，已停止该步骤。") from error
        if process.returncode:
            detail = f" {stderr.strip()}" if stderr and args[0] == "git" else ""
            raise RuntimeError(f"{args[0]} 执行失败（退出码 {process.returncode}）。{detail}")
        return stdout.strip() if stdout else ""


def compose(*, base_only=False):
    args = ["docker", "compose", "-f", ROOT / "compose.tianqiong.yaml"]
    if not base_only:
        args += ["-f", ROOT / "compose.hub.yaml"]
        for path in (ROOT / ".local/network/compose.network.yaml", state_path("docker") / "compose.release.json"):
            if path.is_file() and not path.is_symlink():
                args += ["-f", path]
    return args


def container(service, *, base_only=False):
    ids = command(compose(base_only=base_only) + ["ps", "--all", "--quiet", service], timeout=15).splitlines()
    if len(ids) != 1:
        raise RuntimeError(f"当前目录没有唯一的 Docker 服务容器：{service}。")
    info = json.loads(command(["docker", "inspect", ids[0]], timeout=15))[0]
    labels = info["Config"].get("Labels") or {}
    directory_label = labels.get("com.docker.compose.project.working_dir")
    if not directory_label or Path(directory_label).resolve() != ROOT:
        raise RuntimeError("Docker 容器属于其他部署目录。请在原部署目录运行脚本。")
    return info


def select_mode(explicit):
    if explicit:
        return explicit
    modes = []
    if (native().PGDATA / "PG_VERSION").is_file():
        modes.append("local")
    if shutil.which("docker") and (ROOT / "compose.tianqiong.yaml").is_file():
        try:
            container("postgres", base_only=True)
        except (RuntimeError, OSError):
            pass
        else:
            modes.append("docker")
    if len(modes) == 1:
        return modes[0]
    message = "同时发现本地和 Docker 部署" if modes else "无法确定现有部署方式"
    raise RuntimeError(message + "，请指定 --mode local 或 --mode docker。")


def local_revision():
    deployment = native()
    process = deployment.read_state().get("server")
    if deployment.alive(process):
        try:
            entries = Path("/proc", str(process["pid"]), "environ").read_bytes().split(b"\0")
        except OSError:
            return ""
        for entry in entries:
            if entry.startswith(b"SKYSHIELD_BUILD_REVISION="):
                return entry.partition(b"=")[2].decode("ascii", errors="replace")
        return ""
    return read_json(deployment.DATA / "build-revision.json", {}).get("revision", "")


def current_revision():
    if MODE == "local":
        revision = local_revision()
    else:
        revisions = {
            (container(name)["Config"].get("Labels") or {}).get("org.opencontainers.image.revision", "")
            for name in COMPONENTS
        }
        revision = revisions.pop() if len(revisions) == 1 else ""
    return revision if isinstance(revision, str) and re.fullmatch(r"[0-9a-f]{40}", revision) else ""


@contextlib.contextmanager
def locked_deployment():
    for path in (ROOT / ".local", STATE, STATE / "socket"):
        directory(path)
    lock_path = ROOT / ".local/deployment.lock"
    with os.fdopen(os.open(lock_path, os.O_CREAT | os.O_WRONLY | os.O_NOFOLLOW, 0o600), "a") as lock:
        try:
            fcntl.flock(lock, fcntl.LOCK_EX | fcntl.LOCK_NB)
        except BlockingIOError as error:
            raise RuntimeError("部署或升级正在进行，请等待完成后再试。") from error
        yield


def previous_local_state():
    # The first local updater stored its record and maintenance marker in
    # the Docker state directory. Keep those backups and their paths intact.
    legacy = state_path("docker")
    if MODE == "local" and legacy.is_dir() and not legacy.is_symlink():
        try:
            if read_json(legacy / "daemon.json", {}).get("mode") == "local":
                return legacy
        except PermissionError:
            pass
    return None


def operation_records():
    candidates = [STATE, previous_local_state()]
    for path in candidates:
        if path is None:
            continue
        record_path = path / "operation.json"
        record = read_json(record_path)
        if not record:
            continue
        recorded_mode = record.get("mode") or read_json(path / "daemon.json", {}).get("mode", MODE)
        if recorded_mode == MODE:
            yield record_path, record


def retire_legacy_daemon(path):
    lock_path = path / "daemon.lock"
    if not lock_path.is_file():
        return
    with os.fdopen(os.open(lock_path, os.O_RDONLY | os.O_NOFOLLOW), "r") as lock:
        try:
            fcntl.flock(lock, fcntl.LOCK_EX | fcntl.LOCK_NB)
            return
        except BlockingIOError:
            record = read_json(path / "daemon.json", {})
            pid = record.get("pid")
            if type(pid) is not int or pid <= 1 or record.get("root") != str(ROOT) or record.get("mode") != MODE:
                raise RuntimeError("有旧更新进程占用状态目录，请先由原部署用户停止它。") from None
            proc = Path("/proc", str(pid))
            tick = (proc / "stat").read_text().rsplit(")", 1)[1].split()[19]
            args = (proc / "cmdline").read_bytes().split(b"\0")
            old_command = os.fsencode(ROOT / "scripts/system-update.py")
            if proc.stat().st_uid != os.geteuid() or tick != record.get("startTicks") or args[1:5] != [old_command, b"serve", b"--mode", MODE.encode()]:
                raise RuntimeError("旧更新进程身份不匹配，请先由原部署用户停止它。") from None
            os.kill(pid, signal.SIGTERM)
            for _ in range(50):
                try:
                    fcntl.flock(lock, fcntl.LOCK_EX | fcntl.LOCK_NB)
                    return
                except BlockingIOError:
                    time.sleep(0.1)
    raise RuntimeError("旧更新进程未退出，请停止后重试。")


def retire_previous_updaters():
    for path in (STATE, previous_local_state()):
        if path is not None:
            retire_legacy_daemon(path)


def require_recovery_acknowledgement():
    for path, record in operation_records():
        if record["status"] in ACTIVE:
            # We hold the deployment lock, so a foreground operation cannot
            # still be applying migrations. Its last stage survives a kill.
            status = "failed" if record["status"] in {"queued", "preparing"} else "needs_recovery"
            record.update(status=status, message="上一次升级被中断，请检查日志和备份。", updatedAt=now())
            private_json(path, record)
        if record["status"] == "needs_recovery":
            raise RuntimeError("上一次升级需要恢复。请查看 ./update.sh status，确认恢复后再解除锁定。")


def git_is_ancestor(first, second):
    result = subprocess.run(
        ["git", "-c", "safe.directory=" + str(ROOT), "merge-base", "--is-ancestor", first, second],
        cwd=ROOT, capture_output=True, timeout=30,
    )
    if result.returncode not in (0, 1):
        raise RuntimeError("无法确认版本的 Git 提交关系，请检查源码历史和分支。")
    return result.returncode == 0


def same_release_source(first, second):
    return not command(["git", "diff", "--name-only", first, second, "--", ".", ":(exclude)updates"])


def may_upgrade_from(previous, target):
    return git_is_ancestor(previous, target) or (
        git_is_ancestor(target, previous) and same_release_source(target, previous)
    )


def clean_checkout():
    if not (ROOT / ".git").exists():
        raise RuntimeError("升级需要项目的 Git 克隆目录。")
    if command(["git", "rev-parse", "--show-toplevel"], timeout=10) != str(ROOT):
        raise RuntimeError("请在 SkyShield 的原 Git 克隆目录执行升级。")
    if command(["git", "status", "--porcelain", "--untracked-files=normal"], timeout=30):
        raise RuntimeError("源码存在未提交修改。请先提交或保存修改。")
    return command(["git", "rev-parse", "HEAD"], timeout=10)


def latest_release():
    tag = latest_release_tag()
    ref = "refs/tags/" + tag
    command(["git", "check-ref-format", ref], timeout=10)
    command(["git", "fetch", "--no-tags", REPOSITORY, ref], timeout=180)
    revision = command(["git", "rev-parse", "FETCH_HEAD^{commit}"], timeout=10)
    return {"tag": tag, "version": tag.removeprefix("v"), "revision": revision, "images": {}}


def database_command(program, *args):
    if MODE == "docker":
        return ["docker", "exec", container("postgres")["Id"], program, "-U", "tianqiong", "-d", "tianqiong", *args], None
    deployment = native()
    return [
        deployment.pg_binary(program), "-h", "127.0.0.1", "-p", str(deployment.PG_PORT),
        "-U", "postgres", "-d", deployment.DB_NAME, *args,
    ], deployment.environment()


def query(sql, log=None):
    args, env = database_command("psql", "-X", "-v", "ON_ERROR_STOP=1", "-Atc", sql)
    if log is None:
        return command(args, env=env, timeout=30)
    # Keep the result separate from PostgreSQL diagnostics.
    with tempfile.TemporaryFile(mode="w+") as result:
        command(args, env=env, output=result, log=log, timeout=30)
        result.seek(0)
        return result.read().strip()


def ensure_idle(log):
    sql = """SELECT
      (SELECT count(*) FROM scan_task WHERE status IN ('pending','running')) +
      (SELECT count(*) FROM ai_run WHERE status IN ('running','stopping') AND lease_until > clock_timestamp()) +
      (SELECT count(*) FROM ai_task WHERE (queued OR status='running') AND deleted_at IS NULL)
    """
    if query(sql, log) != "0":
        raise RuntimeError("存在待处理或运行中的任务，请结束任务后重试。")


def preflight(log):
    secret_dir = ROOT / ".local/secrets"
    if MODE == "local":
        deployment = native()
        if not (deployment.PGDATA / "PG_VERSION").is_file() or not deployment.BIN.is_dir():
            raise RuntimeError("未找到现有本地部署，请在原部署目录运行。")
        secret_dir = deployment.SECRETS
        deployment.go_binary()
        for program in ("node", "pnpm"):
            deployment.executable(program)
        state = deployment.read_state()
        for name in ("agent", "frontend", "server"):
            if deployment.alive(state.get(name)) and not state[name].get("owned"):
                raise RuntimeError(f"{name} 由其他方式启动，请先由原启动方式停止。")
    else:
        info = json.loads(command(["docker", "info", "--format", "{{json .}}"], timeout=15))
        if info.get("OSType") != "linux" or info.get("Architecture") not in ("x86_64", "amd64"):
            raise RuntimeError("SkyShield 发布镜像目前仅支持 Linux amd64。")
        for service in ("postgres", "redis", "loki", "agent"):
            command(["docker", "image", "inspect", container(service)["Image"]], log=log, timeout=30)
    for name in SECRET_NAMES:
        path = secret_dir / name
        if path.is_symlink() or not path.is_file() or not path.stat().st_size:
            raise RuntimeError(f"部署密钥缺失或无效：{path}")
    try:
        if MODE == "local" and Path(query("SHOW data_directory", log)).resolve() != deployment.PGDATA.resolve():
            raise RuntimeError("当前 PostgreSQL 数据目录与本地部署不匹配。")
        database_bytes = int(query("SELECT pg_database_size(current_database())", log))
    except (RuntimeError, ValueError) as error:
        raise RuntimeError("无法验证现有数据库。请使用原部署配置启动 PostgreSQL 后重试。") from error
    if shutil.disk_usage(ROOT).free < database_bytes + 10 * 1024**3:
        raise RuntimeError("磁盘可用空间不足以保存数据库备份并预留 10 GiB。")
    ensure_idle(log)


def stop_applications(log):
    if MODE == "docker":
        for name in ("agent", "nginx", "frontend", "server"):
            command(["docker", "stop", "--time", "60", container(name)["Id"]], log=log, timeout=90)
        return
    deployment = native()
    with (deployment.DATA / "local-deploy.lock").open("a") as lock:
        fcntl.flock(lock, fcntl.LOCK_EX | fcntl.LOCK_NB)
        state = deployment.read_state()
        for name in ("agent", "frontend", "server"):
            deployment.stop_managed(state, name)


def backup_database(destination, log):
    dump = destination / "database.dump"
    args, env = database_command("pg_dump", "-Fc")
    with dump.open("xb") as output:
        dump.chmod(0o600)
        command(args, env=env, output=output, log=log, timeout=1800)
        output.flush()
        os.fsync(output.fileno())
    with dump.open("rb") as source:
        if source.read(5) != b"PGDMP":
            raise RuntimeError("数据库备份格式无效。")
    env_file = ROOT / ".env"
    if env_file.is_file():
        shutil.copy2(env_file, destination / ".env")
    secrets = native().SECRETS if MODE == "local" else ROOT / ".local/secrets"
    shutil.copytree(secrets, destination / "secrets")
    if MODE == "local":
        deployment = native()

        def retain_binary(source, target):
            try:
                os.link(source, target)
            except OSError:
                shutil.copy2(source, target)
            return target

        shutil.copytree(deployment.BIN, destination / "bin", copy_function=retain_binary, symlinks=True)
        for name in ("frontend-build.json", "build-revision.json"):
            source = deployment.DATA / name
            if source.is_file():
                shutil.copy2(source, destination / name)
    else:
        private_json(destination / "previous-images.json", {name: container(name)["Image"] for name in COMPONENTS})
        override = STATE / "compose.release.json"
        if override.is_file():
            shutil.copy2(override, destination / override.name)


def image_overlay(images, *, network_enabled):
    services = {name: {"image": images[name]} for name in COMPONENTS}
    bootstrap_services = ("migrate", "volume-preflight", "cert-init")
    if network_enabled:
        bootstrap_services += ("network-helper",)
    services.update({name: {"image": images["bootstrap"]} for name in bootstrap_services})
    return {"services": services}


def prepare_images(target, log):
    target["images"] = published_images(target["revision"])
    for image in target["images"].values():
        command(["docker", "pull", "--platform", "linux/amd64", image], log=log)
        info = json.loads(command(["docker", "image", "inspect", image], timeout=15))[0]
        if (info["Os"], info["Architecture"]) != ("linux", "amd64") or (info["Config"].get("Labels") or {}).get("org.opencontainers.image.revision") != target["revision"]:
            raise RuntimeError("拉取的 Docker 镜像与发布源码不匹配。")


def verify_runtime(target, log):
    if MODE == "local":
        deployment = native()
        if (current_revision() != target["revision"]
                or not deployment.http_ready(deployment.HTTP_PORT, "/healthChecks/readiness")
                or not deployment.http_ready(deployment.FRONTEND_PORT, "/")):
            raise RuntimeError("本地服务健康检查或源码版本检查未通过。")
        return
    expected = {name: json.loads(command(["docker", "image", "inspect", image], timeout=15))[0]["Id"]
                for name, image in target["images"].items()}
    deadline = time.monotonic() + 180
    while time.monotonic() < deadline:
        healthy = True
        for name in COMPONENTS:
            info = container(name)
            state = info["State"]
            if name == "bootstrap":
                healthy &= state["Status"] == "exited" and state["ExitCode"] == 0
            else:
                healthy &= state["Running"] and state.get("Health", {}).get("Status", "healthy") == "healthy"
            healthy &= info["Image"] == expected[name]
        for name in ("migrate", "volume-preflight", "cert-init"):
            info = container(name)
            state = info["State"]
            healthy &= state["Status"] == "exited" and state["ExitCode"] == 0 and info["Image"] == expected["bootstrap"]
        healthy &= container("agent")["State"]["Running"]
        if healthy and query(
            "SELECT count(*) FROM agent a JOIN agent_runtime_status s ON s.agent_id=a.id "
            "JOIN deployment_agent_bootstrap b ON b.instance_id=a.instance_id "
            "WHERE a.status='online' AND s.last_heartbeat>now()-interval '1 minute'", log,
        ) != "0":
            return
        time.sleep(3)
    raise RuntimeError("新版本服务、镜像或 Agent 健康检查未通过。")


def prune_frontend_builds(backup):
    frontend = ROOT / "frontend"
    if MODE != "local" or not frontend.is_dir():
        return
    current = native().active_frontend_stage()
    previous = read_json(backup / "frontend-build.json", {}).get("stage")
    keep = {current, previous}
    for process in Path("/proc").glob("[0-9]*"):
        try:
            if (process / "cwd").resolve() != frontend:
                continue
            for item in (process / "environ").read_bytes().split(b"\0"):
                if item.startswith(b"SKYSHIELD_NEXT_STAGE_DIR="):
                    keep.add(os.fsdecode(item.partition(b"=")[2]))
        except OSError:
            pass
    for path in frontend.glob(".next-skyshield-stage-*"):
        if path.name not in keep and not path.is_symlink() and path.is_dir() and re.fullmatch(r"\.next-skyshield-stage-[A-Za-z0-9-]+", path.name):
            shutil.rmtree(path)
            (frontend / ("tsconfig." + path.name + ".json")).unlink(missing_ok=True)


def prune_successful_backups(current):
    for path in STATE.glob("operation-*"):
        if path == current or path.is_symlink() or not path.is_dir():
            continue
        record = read_json(path / "operation.json", {})
        previous = read_json(path / "previous.json", {})
        if record.get("status") == "succeeded" and (record.get("mode") or previous.get("mode")) == MODE:
            shutil.rmtree(path)


def set_stage(operation, folder, status, message):
    operation.update(status=status, message=message, updatedAt=now())
    private_json(STATE / "operation.json", operation)
    private_json(folder / "operation.json", operation)
    print(message, flush=True)


def install(target, previous, running, network_enabled):
    operation = {"id": str(uuid.uuid4()), "mode": MODE, "version": target["version"], "revision": target["revision"], "createdAt": now()}
    folder = STATE / ("operation-" + operation["id"])
    directory(folder)
    operation["logFile"] = str(folder / "update.log")
    stopped = False
    try:
        with (folder / "update.log").open("xb") as log, contextlib.ExitStack() as cleanup:
            set_stage(operation, folder, "preparing", "检查现有部署、任务和备份空间。")
            preflight(log)
            if MODE == "docker":
                print("校验并拉取新版本的 Docker 镜像。", flush=True)
                prepare_images(target, log)
            if clean_checkout() != previous:
                raise RuntimeError("准备期间源码发生变更，请重新运行升级。")
            private_json(folder / "target.json", target)
            private_json(folder / "previous.json", {"revision": previous, "runtimeRevision": running, "mode": MODE})
            for location in (STATE, previous_local_state()):
                if location is None:
                    continue
                directory(location / "socket")
                marker = location / "socket/maintenance"
                private_json(marker, {"operationId": operation["id"]})
                cleanup.callback(marker.unlink, missing_ok=True)
            ensure_idle(log)
            set_stage(operation, folder, "backing_up", "停止应用并备份数据库、密钥和配置。")
            stopped = True
            stop_applications(log)
            backup_database(folder, log)
            command(["git", "update-ref", "refs/skyshield/rollback", previous], log=log, timeout=15)
            set_stage(operation, folder, "updating", f"安装 SkyShield {target['tag']}。")
            if git_is_ancestor(previous, target["revision"]) and previous != target["revision"]:
                command(["git", "-c", "core.hooksPath=/dev/null", "merge", "--ff-only", target["revision"]], log=log, timeout=120)
            env = dict(os.environ, SKYSHIELD_UPDATER_CHILD="1")
            if MODE == "docker":
                private_json(STATE / "compose.release.json", image_overlay(target["images"], network_enabled=network_enabled))
                env["SKYSHIELD_IMAGE_TAG"] = "sha-" + target["revision"][:12]
                command(["bash", ROOT / "docker.sh", "pull"], env=env, log=log, timeout=3600)
            else:
                env["SKYSHIELD_UPDATE_REVISION"] = target["revision"]
                command(["pnpm", "install", "--frozen-lockfile"], cwd=ROOT / "frontend", env=env, log=log)
                command(["bash", ROOT / "local.sh", "start", "--no-tools-install"], env=env, log=log, timeout=7200)
            set_stage(operation, folder, "verifying", "验证新版本服务。")
            verify_runtime(target, log)
            private_json(STATE / "installed.json", {"revision": target["revision"], "version": target["version"], "installedAt": now(), "mode": MODE})
            set_stage(operation, folder, "succeeded", f"升级完成：SkyShield {target['tag']}。备份位于 {folder}")
            try:
                prune_frontend_builds(folder)
                prune_successful_backups(folder)
            except (OSError, RuntimeError, ValueError) as error:
                print(f"未能清理部分历史构建或备份：{error}", file=sys.stderr)
    except (Exception, KeyboardInterrupt) as error:
        message = "升级已中断。" if isinstance(error, KeyboardInterrupt) else str(error)
        set_stage(operation, folder, "needs_recovery" if stopped else "failed", message)
        hint = "应用已进入停机或安装阶段，请按文档恢复。" if stopped else "升级未进入停机阶段。"
        raise RuntimeError(f"{hint} 日志：{operation['logFile']}") from error


def upgrade(confirmed):
    with locked_deployment():
        require_recovery_acknowledgement()
        previous = clean_checkout()
        module = import_deployment("deployment-network")
        settings = module.settings(ROOT)
        os.environ.update({key: settings[key] for key in module.PROXY_KEYS if key in settings})
        target = latest_release()
        running = current_revision()
        revision = target["revision"]
        if not may_upgrade_from(previous, revision) or (running and not may_upgrade_from(running, revision)):
            raise RuntimeError("当前源码或部署版本与发布版本分叉，或已领先发布版本，无法快进升级。")
        if running == revision:
            retire_previous_updaters()
            print(f"当前已安装最新正式版本 SkyShield {target['tag']}。")
            return
        inherit = settings.get("SKYSHIELD_INHERIT_HOST_NETWORK", "1")
        if MODE == "docker" and inherit not in ("0", "1"):
            raise RuntimeError("SKYSHIELD_INHERIT_HOST_NETWORK 只能是 0 或 1。")
        print(f"部署方式 {MODE}，目标 SkyShield {target['tag']} ({revision[:12]})。升级会暂时中断服务。", flush=True)
        if not confirmed:
            if not sys.stdin.isatty():
                raise RuntimeError("请在终端确认升级，或显式传入 --confirm。")
            if input("确认升级？[y/N] ").strip().lower() not in {"y", "yes"}:
                print("已取消升级。")
                return
        retire_previous_updaters()
        require_recovery_acknowledgement()
        install(target, previous, running, network_enabled=inherit == "1")


def acknowledge_recovery():
    with locked_deployment():
        records = [(path, record) for path, record in operation_records() if record["status"] == "needs_recovery" or record["status"] in ACTIVE]
        if not records:
            raise RuntimeError("没有需要确认的恢复操作。")
        for path, record in records:
            retire_legacy_daemon(path.parent)
            record.update(status="failed", message="管理员已确认恢复，可以重新升级。", updatedAt=now(), recoveryAcknowledgedAt=now())
            private_json(path, record)
            (path.parent / "socket/maintenance").unlink(missing_ok=True)
        print("已解除恢复锁定。")


def interrupt(_signal, _frame):
    raise KeyboardInterrupt


def main():
    global MODE, STATE
    parser = argparse.ArgumentParser(prog="update.sh", description="在部署主机上安装 SkyShield 最新正式版本。")
    parser.add_argument("action", nargs="?", default="upgrade", metavar="{upgrade,status,acknowledge-recovery}",
                        choices=("upgrade", "apply", "status", "acknowledge-recovery", "serve"))
    parser.add_argument("--mode", choices=("local", "docker"), help="本地或 Docker 部署；省略时自动识别")
    parser.add_argument("--confirm", action="store_true", help="明确确认升级或恢复，无需交互")
    args = parser.parse_args()
    if args.action == "serve":
        # A unit created by a previous release can still have this ExecStart.
        # Exit successfully so Restart=on-failure does not schedule more checks.
        return 0
    signal.signal(signal.SIGTERM, interrupt)
    signal.signal(signal.SIGHUP, interrupt)
    try:
        MODE = select_mode(args.mode)
        STATE = state_path(MODE)
        if args.action == "status":
            records = [record for _, record in operation_records()]
            record = max(records, key=lambda item: (item["status"] == "needs_recovery", item.get("updatedAt", "")), default=None)
            print(json.dumps({"mode": MODE, "installed": read_json(STATE / "installed.json"), "operation": record}, ensure_ascii=False, indent=2))
        elif args.action == "acknowledge-recovery":
            if not args.confirm:
                raise RuntimeError("确认服务已恢复后，请附加 --confirm。")
            acknowledge_recovery()
        else:
            upgrade(args.confirm)
    except (OSError, RuntimeError, ValueError, subprocess.TimeoutExpired) as error:
        print(f"系统更新：{error}", file=sys.stderr)
        return 1
    except KeyboardInterrupt:
        print("已中断升级。", file=sys.stderr)
        return 130
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
