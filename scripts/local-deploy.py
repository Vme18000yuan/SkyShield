#!/usr/bin/env python3
"""Build and supervise the native, single-machine development deployment."""

import argparse
import base64
import fcntl
import hashlib
import json
import os
import pathlib
import re
import shlex
import shutil
import signal
import socket
import subprocess
import sys
import tempfile
import time
import urllib.error
import urllib.request
import uuid

ROOT = pathlib.Path(__file__).resolve().parent.parent
DATA = pathlib.Path(os.environ.get("SKYSHIELD_LOCAL_DATA_DIR", str(ROOT / ".local/dev-runtime"))).expanduser().resolve()
STATE = DATA / "local-processes.json"
FRONTEND_BUILD = DATA / "frontend-build.json"
LOGS = DATA / "logs"
BIN = DATA / "bin"
ENGINES = BIN / "engines"
TOOLS = DATA / "tools/bin"
PGDATA = DATA / "postgres/data"
PGSOCKET = DATA / "postgres"
SECRETS = DATA / "secrets"
AGENT_CREDENTIALS = DATA / "agent/bootstrap.json"
EXECUTION_ROOT = pathlib.Path(os.path.abspath(os.path.expanduser(os.environ.get("SKYSHIELD_LOCAL_EXECUTION_ROOT", f"/tmp/skyshield-{os.getuid()}-engines"))))
DB_NAME = "tianqiong_local_dev"
PG_PORT = int(os.environ.get("SKYSHIELD_LOCAL_PG_PORT", "41303"))
REDIS_PORT = int(os.environ.get("SKYSHIELD_LOCAL_REDIS_PORT", "51563"))
HTTP_PORT = int(os.environ.get("SKYSHIELD_LOCAL_HTTP_PORT", "8080"))
GRPC_PORT = int(os.environ.get("SKYSHIELD_LOCAL_GRPC_PORT", "9090"))
FRONTEND_PORT = int(os.environ.get("SKYSHIELD_LOCAL_FRONTEND_PORT", "3000"))
LISTEN_HOST = "0.0.0.0"
VERSION = "0.0.1-alpha.229"
ENGINE_NAMES = ("subdomain_discovery", "port_scan", "website_discovery", "url_collection", "fingerprint_detection", "directory_scan", "screenshot", "nuclei_vulnerability")


def fail(message):
    raise RuntimeError(message)


def call(args, *, cwd=None, env=None, capture=False):
    result = subprocess.run([str(item) for item in args], cwd=cwd, env=env, text=True, capture_output=capture)
    if result.returncode:
        if capture and result.stderr:
            print(result.stderr[-3000:], file=sys.stderr)
        fail(f"命令执行失败：{args[0]}（退出码 {result.returncode}）")
    return result.stdout.strip() if capture else ""


def executable(name):
    value = shutil.which(name)
    if not value:
        fail(f"缺少本地依赖：{name}")
    return value


def go_binary():
    candidate = os.environ.get("SKYSHIELD_GO") or shutil.which("go")
    if candidate and os.access(candidate, os.X_OK):
        return candidate
    matches = sorted((pathlib.Path.home() / "go/pkg/mod/golang.org").glob("toolchain@v0.0.1-go1.26.10.*/bin/go"))
    if matches and os.access(matches[-1], os.X_OK):
        return str(matches[-1])
    fail("需要 Go 1.26.10；可设置 SKYSHIELD_GO 指向 go 可执行文件")


def pg_binary(name):
    direct = shutil.which(name)
    if direct:
        return direct
    pg_config = executable("pg_config")
    result = pathlib.Path(call([pg_config, "--bindir"], capture=True)) / name
    if not os.access(result, os.X_OK):
        fail(f"缺少 PostgreSQL 程序：{name}")
    return str(result)


def prepare_directories():
    # Wordlist bootstrap uses the absence of its root as its first-install
    # marker; do not pre-create it or remove any existing user-managed root.
    for directory in (DATA, LOGS, BIN, ENGINES, TOOLS, PGSOCKET, SECRETS, DATA / "redis", DATA / "agent", DATA / "fingerprints", DATA / "login-visuals", DATA / "nuclei-poc", DATA / "ai", DATA / "engine-packages", DATA / "upgrade"):
        directory.mkdir(parents=True, exist_ok=True, mode=0o700)
    if EXECUTION_ROOT == pathlib.Path("/") or not EXECUTION_ROOT.is_absolute():
        fail("单机执行目录无效")
    EXECUTION_ROOT.mkdir(parents=True, exist_ok=True, mode=0o700)
    if EXECUTION_ROOT.is_symlink() or EXECUTION_ROOT.stat().st_uid != os.getuid() or EXECUTION_ROOT.stat().st_mode & 0o077:
        fail(f"单机执行目录必须归当前用户所有且权限为 0700：{EXECUTION_ROOT}")
    for name in ("db_password", "jwt_secret", "ai_encryption_key"):
        path = SECRETS / name
        if path.is_symlink() or (path.exists() and not path.is_file()):
            fail(f"密钥路径不是普通文件：{path}")
        if not path.exists():
            descriptor = os.open(path, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
            with os.fdopen(descriptor, "wb") as output:
                output.write(base64.b64encode(os.urandom(32)) + b"\n")
        if path.stat().st_mode & 0o077:
            fail(f"密钥文件权限必须为 0600：{path}")


def environment():
    env = os.environ.copy()
    env.update({
        "DB_HOST": "127.0.0.1", "DB_PORT": str(PG_PORT), "DB_USER": "postgres", "DB_NAME": DB_NAME,
        "DB_PASSWORD_FILE": str(SECRETS / "db_password"), "DB_SSLMODE": "disable", "DB_TIMEZONE": "UTC",
        "JWT_SECRET_FILE": str(SECRETS / "jwt_secret"), "TIANQIONG_AI_ENCRYPTION_KEY_FILE": str(SECRETS / "ai_encryption_key"),
        "REDIS_HOST": "127.0.0.1", "REDIS_PORT": str(REDIS_PORT), "LOKI_URL": os.environ.get("SKYSHIELD_LOCAL_LOKI_URL", "http://127.0.0.1:3100"),
        "TIANQIONG_LOCAL_MODE": "true", "TIANQIONG_LOCAL_DATA_ROOT": str(DATA), "SERVER_HOST": LISTEN_HOST, "SERVER_GRPC_HOST": LISTEN_HOST,
        "SERVER_PORT": str(HTTP_PORT), "SERVER_GRPC_PORT": str(GRPC_PORT), "GIN_MODE": "release", "PUBLIC_URL": f"http://127.0.0.1:{FRONTEND_PORT}",
        "WORDLISTS_BASE_PATH": str(DATA / "wordlists"), "FINGERPRINT_ARTIFACTS_BASE_PATH": str(DATA / "fingerprints"),
        "LOGIN_VISUALS_BASE_PATH": str(DATA / "login-visuals"), "NUCLEI_POC_WORKSPACE_ROOT": str(DATA / "nuclei-poc"),
        "ENGINE_PACKAGE_CACHE_ROOT": str(DATA / "engine-packages"), "WORKFLOW_DEFINITIONS_ROOT": str(ROOT / "extensions/workflows"),
        "TIANQIONG_AI_DATA_ROOT": str(DATA / "ai"), "TIANQIONG_AI_SKILLS_ROOT": str(ROOT / "resources/skills"),
        "TIANQIONG_INFO_GATHER_TOOLS_DIR": str(TOOLS),
        "TIANQIONG_LOCAL_PROXY_SOCKET": str(DATA / "agent/proxy.sock"),
        "ENGINE_INSTALL_INVENTORY_PATH": str(ROOT / "engine-inventory.yaml"), "ENGINE_INSTALL_REGISTRY": os.environ.get("SKYSHIELD_ENGINE_REGISTRY", "docker.io"),
        "MIGRATION_POLICY_PATH": str(ROOT / "server/cmd/server/migrations/policy.json"), "RELEASE_MANIFEST_PATH": str(ROOT / "release.manifest.yaml"),
        "LUNAFOX_UPGRADE_DEPLOYMENT_ROOT": str(ROOT), "LUNAFOX_UPGRADE_SOCKET_PATH": str(DATA / "upgrade/upgrader.sock"),
        "RELEASE_VERSION": VERSION, "AGENT_VERSION": VERSION,
        "PGPASSWORD": (SECRETS / "db_password").read_text().strip(),
    })
    return env


def process_start(pid):
    try:
        fields = pathlib.Path(f"/proc/{pid}/stat").read_text().rsplit(")", 1)[1].split()
        return None if fields[0] == "Z" else fields[19]
    except (OSError, IndexError):
        return None


def read_state():
    try:
        return json.loads(STATE.read_text())
    except FileNotFoundError:
        return {}


def write_state(state):
    temporary = STATE.with_suffix(".tmp")
    temporary.write_text(json.dumps(state, indent=2) + "\n")
    temporary.chmod(0o600)
    temporary.replace(STATE)


def alive(entry):
    return bool(entry and process_start(entry.get("pid", 0)) == entry.get("start"))


def record(state, name, pid, owned=True):
    start = process_start(pid)
    if not start:
        fail(f"{name} 启动后立即退出，请查看 {LOGS}")
    state[name] = {"pid": pid, "start": start, "owned": owned}
    write_state(state)


def free_port(port, host="127.0.0.1"):
    with socket.socket() as sock:
        sock.setsockopt(socket.SOL_SOCKET, socket.SO_REUSEADDR, 1)
        try:
            sock.bind((host, port))
        except OSError:
            fail(f"本地端口 {port} 已被占用；请先停止旧进程或设置对应的 SKYSHIELD_LOCAL_*_PORT")


def spawn(state, name, args, env, cwd):
    if alive(state.get(name)):
        return
    with (LOGS / f"local-{name}.log").open("ab") as log:
        process = subprocess.Popen([str(item) for item in args], cwd=cwd, env=env, stdin=subprocess.DEVNULL, stdout=log, stderr=subprocess.STDOUT, start_new_session=True)
    record(state, name, process.pid)


def wait_for(predicate, description, timeout=45, process=None):
    deadline = time.monotonic() + timeout
    while time.monotonic() < deadline:
        if predicate():
            return
        if process is not None and not alive(process):
            fail(f"{description} 启动后退出，请查看 {LOGS}")
        time.sleep(0.5)
    fail(f"等待 {description} 超时，请查看 {LOGS}")


def http_ready(port, path):
    try:
        with urllib.request.urlopen(f"http://127.0.0.1:{port}{path}", timeout=1) as response:
            return response.status < 400
    except (OSError, urllib.error.URLError):
        return False


def redis_ready(port):
    try:
        with socket.create_connection(("127.0.0.1", port), timeout=1) as connection:
            connection.sendall(b"*1\r\n$4\r\nPING\r\n")
            return connection.recv(16).startswith(b"+PONG")
    except OSError:
        return False


def active_frontend_stage():
    try:
        stage = json.loads(FRONTEND_BUILD.read_text())["stage"]
    except FileNotFoundError:
        return None
    except (ValueError, KeyError, TypeError):
        fail("本地前端构建记录无效，不会覆盖正在运行的版本")
    if not isinstance(stage, str) or not re.fullmatch(r"\.next-skyshield-stage-[a-zA-Z0-9-]+", stage):
        fail("本地前端构建记录无效，不会覆盖正在运行的版本")
    directory = ROOT / "frontend" / stage
    if directory.is_symlink() or not (directory / "BUILD_ID").is_file():
        fail("当前本地前端构建目录不存在或无效")
    return stage


def frontend_environment(env, stage=None):
    result = env.copy()
    result.update({"NODE_ENV": "production", "API_HOST": "127.0.0.1", "API_PORT": str(HTTP_PORT)})
    result.pop("SKYSHIELD_NEXT_STAGE_DIR", None)
    if stage:
        result["SKYSHIELD_NEXT_STAGE_DIR"] = stage
    return result


def record_frontend_stage(stage):
    temporary = FRONTEND_BUILD.with_suffix(".tmp")
    temporary.write_text(json.dumps({"stage": stage}) + "\n")
    temporary.chmod(0o600)
    temporary.replace(FRONTEND_BUILD)


def frontend_needs_build():
    frontend = ROOT / "frontend"
    active = active_frontend_stage() or ".next"
    build_id = frontend / active / "BUILD_ID"
    inputs = (frontend / "app", frontend / "components", frontend / "hooks", frontend / "services", frontend / "lib", frontend / "types", frontend / "messages", frontend / "i18n")
    marker = frontend / active / "skyshield-local-api-origin"
    expected = f"127.0.0.1:{HTTP_PORT}"
    config_inputs = (frontend / "next.config.ts", frontend / "tsconfig.json", frontend / "package.json", frontend / "pnpm-lock.yaml")
    return not build_id.exists() or not marker.exists() or marker.read_text().strip() != expected or any(path.is_file() and path.stat().st_mtime > build_id.stat().st_mtime for path in config_inputs) or any(path.is_file() and path.stat().st_mtime > build_id.stat().st_mtime for base in inputs if base.exists() for path in base.rglob("*"))


def digest(path):
    result = hashlib.sha256()
    with path.open("rb") as source:
        for chunk in iter(lambda: source.read(1024 * 1024), b""):
            result.update(chunk)
    return result.digest()


def build_binary(go, output, package, cwd, env):
    temporary = output.with_name(output.name + ".new")
    try:
        call([go, "build", "-trimpath", "-o", temporary, package], cwd=cwd, env=env)
        if output.exists() and output.stat().st_size == temporary.stat().st_size and digest(output) == digest(temporary):
            temporary.unlink()
            return False
        temporary.replace(output)
        return True
    finally:
        if temporary.exists():
            temporary.unlink()


def build(env, validate=None):
    go = go_binary()
    env = env.copy()
    env["GOTOOLCHAIN"] = "local"
    env["API_HOST"] = "127.0.0.1"
    env["API_PORT"] = str(HTTP_PORT)
    print("构建本地 Server 和 Agent……", flush=True)
    # Compile everything before replacing any executable. A failed later build
    # must not leave a future --no-build restart with a mixture of versions.
    with tempfile.TemporaryDirectory(prefix=".local-build-", dir=DATA) as temporary:
        stage = pathlib.Path(temporary)
        (stage / "engines").mkdir()
        items = []
        for name, package in (("skyshield-server", "./cmd/server"), ("skyshield-local-agent", "./cmd/local-agent"), ("skyshield-local-engines", "./cmd/local-engine-bootstrap")):
            candidate = stage / name
            build_binary(go, candidate, package, ROOT / "server", env)
            items.append((candidate, BIN / name))
        for name in ENGINE_NAMES:
            source = ROOT / "extensions/engines" / name
            commands = [path for path in (source / "cmd").iterdir() if path.is_dir()]
            if len(commands) != 1:
                fail(f"无法确定 Engine 入口：{name}")
            candidate = stage / "engines" / name
            build_binary(go, candidate, "./cmd/" + commands[0].name, source, env)
            items.append((candidate, ENGINES / name))
        changed = {target for _, target in changed_binaries(items)}
        server_changed = BIN / "skyshield-server" in changed
        agent_changed = any(path == BIN / "skyshield-local-agent" or path.parent == ENGINES for path in changed)
        if validate is not None:
            validate(server_changed, agent_changed)
        promote_binaries(items, stage)
        return server_changed, agent_changed


def changed_binaries(items):
    changed = []
    for candidate, target in items:
        if target.is_symlink() or (target.exists() and not target.is_file()):
            fail(f"本地可执行文件路径无效：{target}")
        if not target.exists() or target.stat().st_size != candidate.stat().st_size or digest(target) != digest(candidate):
            changed.append((candidate, target))
    return changed


def promote_binaries(items, stage):
    changed = changed_binaries(items)
    applied = []
    try:
        for index, (candidate, target) in enumerate(changed):
            backup = None
            if target.exists():
                backup = stage / f"old-{index}"
                try:
                    os.link(target, backup)
                except OSError:
                    shutil.copy2(target, backup)
            candidate.replace(target)
            applied.append((target, backup))
    except Exception:
        for target, backup in reversed(applied):
            if backup is None:
                target.unlink(missing_ok=True)
            else:
                backup.replace(target)
        raise
    return {target for _, target in changed}


def preview_frontend(stage, env):
    frontend = ROOT / "frontend"
    build_id = (frontend / stage / "BUILD_ID").read_text().strip()
    if not re.fullmatch(r"[a-zA-Z0-9_-]{1,128}", build_id) or not (frontend / stage / "static" / build_id / "_buildManifest.js").is_file():
        fail("候选前端静态资源不完整，现有服务仍在运行")
    with socket.socket() as listener:
        listener.bind(("127.0.0.1", 0))
        port = listener.getsockname()[1]
    with (LOGS / "local-frontend-preview.log").open("ab") as log:
        process = subprocess.Popen([str(frontend / "node_modules/.bin/next"), "start", "--hostname", "127.0.0.1", "--port", str(port)], cwd=frontend, env=frontend_environment(env, stage), stdin=subprocess.DEVNULL, stdout=log, stderr=subprocess.STDOUT, start_new_session=True)
    try:
        deadline = time.monotonic() + 60
        while time.monotonic() < deadline:
            if process.poll() is not None:
                fail("候选前端启动失败，现有服务仍在运行；请查看 local-frontend-preview.log")
            if http_ready(port, "/") and http_ready(port, f"/_next/static/{build_id}/_buildManifest.js"):
                return
            time.sleep(0.25)
        fail("候选前端健康检查超时，现有服务仍在运行；请查看 local-frontend-preview.log")
    finally:
        if process.poll() is None:
            try:
                os.killpg(process.pid, signal.SIGTERM)
            except ProcessLookupError:
                pass
            try:
                process.wait(timeout=5)
            except subprocess.TimeoutExpired:
                try:
                    os.killpg(process.pid, signal.SIGKILL)
                except ProcessLookupError:
                    pass
                process.wait()


def build_frontend(env):
    frontend = ROOT / "frontend"
    next_bin = frontend / "node_modules/.bin/next"
    if not next_bin.exists():
        call([executable("pnpm"), "install", "--frozen-lockfile"], cwd=frontend, env=env)
    stage = ".next-skyshield-stage-" + uuid.uuid4().hex
    stage_dir = frontend / stage
    stage_tsconfig = frontend / ("tsconfig." + stage + ".json")
    stage_tsconfig.write_bytes((frontend / "tsconfig.json").read_bytes())
    generated = frontend / "next-env.d.ts"
    previous = generated.read_bytes() if generated.is_file() else None
    stage_env = frontend_environment(env, stage)
    port_marker = stage_dir / "skyshield-local-api-origin"
    api_origin = f"127.0.0.1:{HTTP_PORT}"
    print("在独立目录构建本地前端……", flush=True)
    try:
        build_args = [next_bin, "build", "--no-lint"]
        if env.get("SKYSHIELD_LOCAL_TURBOPACK") == "1":
            build_args.append("--turbopack")
        call(build_args, cwd=frontend, env=stage_env)
    finally:
        # Next regenerates this file. Restore it only when replacing the stage
        # path is the sole change, so concurrent developer edits are preserved.
        if previous is not None and generated.is_file():
            current = generated.read_bytes()
            if current.replace(("./" + stage + "/").encode(), b"./.next/") == previous:
                generated.write_bytes(previous)
    if stage_dir.is_symlink() or not (stage_dir / "BUILD_ID").is_file():
        fail("候选前端构建不完整，现有服务仍在运行")
    port_marker.write_text(api_origin + "\n")
    preview_frontend(stage, env)
    return stage


def matches_binary(entry, binary):
    if not alive(entry):
        return True
    try:
        return os.path.samefile(f"/proc/{entry['pid']}/exe", binary)
    except OSError:
        return False


def local_agent_busy(env):
    if not AGENT_CREDENTIALS.is_file():
        fail("本机 Agent 正在运行，但无法读取其身份；不会中断扫描")
    agent_id = json.loads(AGENT_CREDENTIALS.read_text()).get("agentId")
    if not isinstance(agent_id, int) or agent_id <= 0:
        fail("本机 Agent 身份无效；不会中断扫描")
    return query(env, f"SELECT count(*) FROM scan_task WHERE status='running' AND assigned_agent_id={agent_id}") != "0"


def stop_managed(state, name):
    entry = state.get(name)
    if alive(entry):
        if not entry.get("owned"):
            fail(f"{name} 由其他方式启动，请先由原启动方式停止")
        os.kill(entry["pid"], signal.SIGTERM)
        deadline = time.monotonic() + 20
        while alive(entry) and time.monotonic() < deadline:
            time.sleep(0.2)
        if alive(entry):
            fail(f"{name} 未能在 20 秒内停止，请检查日志")
    state.pop(name, None)
    write_state(state)


def postgres_start(state, env):
    pgctl = pg_binary("pg_ctl")
    initdb = pg_binary("initdb")
    if not (PGDATA / "PG_VERSION").exists():
        PGDATA.mkdir(parents=True, exist_ok=True, mode=0o700)
        call([initdb, "-D", PGDATA, "-U", "postgres", "--auth-local=trust", "--auth-host=scram-sha-256", "--pwfile=" + str(SECRETS / "db_password"), "--no-instructions"], env=env)
    if subprocess.run([pgctl, "-D", str(PGDATA), "status"], stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL).returncode != 0:
        free_port(PG_PORT)
        options = f"-h 127.0.0.1 -p {PG_PORT} -k {shlex.quote(str(PGSOCKET))}"
        call([pgctl, "-D", PGDATA, "-l", LOGS / "local-postgres.log", "-o", options, "-w", "start"], env=env)
        pid = int((PGDATA / "postmaster.pid").read_text().splitlines()[0])
        record(state, "postgres", pid)
    elif not alive(state.get("postgres")):
        pid = int((PGDATA / "postmaster.pid").read_text().splitlines()[0])
        record(state, "postgres", pid, owned=False)
    ready = pg_binary("pg_isready")
    wait_for(lambda: subprocess.run([ready, "-h", "127.0.0.1", "-p", str(PG_PORT), "-U", "postgres"], stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL).returncode == 0, "PostgreSQL")
    psql = pg_binary("psql")
    present = call([psql, "-X", "-h", "127.0.0.1", "-p", PG_PORT, "-U", "postgres", "-d", "postgres", "-Atc", f"SELECT 1 FROM pg_database WHERE datname='{DB_NAME}'"], env=env, capture=True)
    if present != "1":
        call([pg_binary("createdb"), "-h", "127.0.0.1", "-p", PG_PORT, "-U", "postgres", DB_NAME], env=env)


def query(env, sql):
    return call([pg_binary("psql"), "-X", "-h", "127.0.0.1", "-p", PG_PORT, "-U", "postgres", "-d", DB_NAME, "-Atc", sql], env=env, capture=True)


def agent_args():
    return [BIN / "skyshield-local-agent", "--credentials", AGENT_CREDENTIALS, "--server", f"127.0.0.1:{GRPC_PORT}",
            "--execution-root", EXECUTION_ROOT, "--engine-bin-dir", ENGINES, "--engine-source-dir", ROOT / "extensions/engines",
            "--package-cache-root", DATA / "engine-packages", "--tools-dir", TOOLS, "--proxy-socket", DATA / "agent/proxy.sock", "--version", VERSION]


def check_existing_runtime(args):
    """Check an existing installation before starting or stopping its processes."""
    required = [BIN / "skyshield-server", BIN / "skyshield-local-agent", ROOT / "frontend/node_modules/.bin/next"]
    if any(not path.is_file() or not os.access(path, os.X_OK) for path in required):
        fail("本地程序或前端依赖尚未部署，请先执行 ./local.sh start")
    stage = active_frontend_stage() or ".next"
    if not (ROOT / "frontend" / stage / "BUILD_ID").is_file():
        fail("本地前端尚未构建，请先执行 ./local.sh start")
    if not (PGDATA / "PG_VERSION").is_file() or not AGENT_CREDENTIALS.is_file():
        fail("本地数据库或 Agent 尚未初始化，请先执行 ./local.sh start")
    try:
        identity = json.loads(AGENT_CREDENTIALS.read_text())
        if (not isinstance(identity, dict) or type(identity.get("agentId")) is not int or identity["agentId"] <= 0
                or not isinstance(identity.get("instanceId"), str) or not identity["instanceId"].strip()
                or not isinstance(identity.get("agentAuthenticationToken"), str) or not identity["agentAuthenticationToken"].strip()):
            raise ValueError("invalid identity")
    except (OSError, ValueError, TypeError):
        fail("本地 Agent 凭据无效；请先检查现有部署")
    for name in ("db_password", "jwt_secret", "ai_encryption_key"):
        path = SECRETS / name
        if path.is_symlink() or not path.is_file() or path.stat().st_mode & 0o077 or not path.read_bytes().strip():
            fail(f"本地密钥缺失或权限无效：{path}")
    for name in ("pg_ctl", "initdb", "pg_isready", "psql", "createdb"):
        pg_binary(name)
    executable("redis-server")
    executable("node")
    if not args.ui_only:
        check = subprocess.run([str(value) for value in agent_args() + ["--check"]], env=environment())
        if check.returncode:
            fail("本地扫描工具未准备完成；请先执行 ./local.sh start")


def run_existing(args):
    check_existing_runtime(args)
    args.no_build = True
    args.no_tools_install = True
    start(args)


def restart_existing(args):
    check_existing_runtime(args)
    state = read_state()
    for name in ("agent", "frontend", "server", "redis"):
        if alive(state.get(name)) and not state[name].get("owned"):
            fail(f"{name} 由其他方式启动，请先由原启动方式停止")
    stop(args)
    args.no_build = True
    args.no_tools_install = True
    start(args)


def start(args):
    prepare_directories()
    env = environment()
    state = read_state()
    previous_frontend_stage = active_frontend_stage()
    staged_frontend = None
    frontend_was_running = alive(state.get("frontend"))
    if not args.no_build:
        if alive(state.get("agent")) and local_agent_busy(env):
            fail("本机 Agent 有正在执行的扫描；为避免中断，请待任务结束后再次启动以应用新构建")
        rebuild_frontend = frontend_needs_build()
        # Build and boot the candidate on another port while the existing
        # services are still available. A failed build must not stop them.
        if rebuild_frontend:
            staged_frontend = build_frontend(env)
        server_restart = agent_restart = False
        def validate_build(server_changed, agent_changed):
            nonlocal server_restart, agent_restart
            server_restart = alive(state.get("server")) and (server_changed or not matches_binary(state.get("server"), BIN / "skyshield-server"))
            agent_restart = alive(state.get("agent")) and (server_restart or agent_changed or not matches_binary(state.get("agent"), BIN / "skyshield-local-agent"))
            if agent_restart and local_agent_busy(env):
                fail("本机 Agent 有正在执行的扫描；为避免中断，请待任务结束后再次启动以应用新构建")
            for name, restart in (("agent", agent_restart), ("server", server_restart), ("frontend", bool(staged_frontend) and frontend_was_running)):
                if restart and not state[name].get("owned"):
                    fail(f"{name} 由其他方式启动，请先由原启动方式停止")
        build(env, validate_build)
        if agent_restart:
            stop_managed(state, "agent")
        if server_restart:
            stop_managed(state, "server")
    if not (BIN / "skyshield-server").is_file() or not (BIN / "skyshield-local-agent").is_file():
        fail("本地程序尚未构建，请去掉 --no-build 后重试")
    postgres_start(state, env)
    if not alive(state.get("redis")):
        free_port(REDIS_PORT)
        spawn(state, "redis", [executable("redis-server"), "--bind", "127.0.0.1", "--port", REDIS_PORT, "--dir", DATA / "redis", "--appendonly", "yes"], env, ROOT)
    wait_for(lambda: redis_ready(REDIS_PORT), "Redis", process=state.get("redis"))
    server = BIN / "skyshield-server"
    call([server, "migrate", "up"], cwd=ROOT / "server", env=env)
    if query(env, "SELECT count(*) FROM engine") == "0":
        print("首次安装扫描引擎清单……", flush=True)
        call([server, "engine-bootstrap"], cwd=ROOT / "server", env=env)
    if (BIN / "skyshield-local-engines").is_file():
        call([BIN / "skyshield-local-engines", ROOT / "extensions/engines"], cwd=ROOT / "server", env=env)
    call([server, "fingerprint-bootstrap", ROOT / "resources/fingerprints/web_fingerprint_v4.json"], cwd=ROOT / "server", env=env)
    call([server, "wordlist-bootstrap", ROOT / "resources/wordlists/manifest.json", ROOT / "resources/wordlists"], cwd=ROOT / "server", env=env)
    call([server, "local-agent-bootstrap", AGENT_CREDENTIALS, "skyshield-local", VERSION], cwd=ROOT / "server", env=env)
    env["TIANQIONG_LOCAL_AGENT_INSTANCE_ID"] = json.loads(AGENT_CREDENTIALS.read_text())["instanceId"]
    if not alive(state.get("server")):
        free_port(HTTP_PORT, LISTEN_HOST); free_port(GRPC_PORT, LISTEN_HOST)
        spawn(state, "server", [server], env, ROOT / "server")
    wait_for(lambda: http_ready(HTTP_PORT, "/healthChecks/readiness"), "Server", process=state.get("server"))
    if staged_frontend and frontend_was_running:
        stop_managed(state, "frontend")
    if not alive(state.get("frontend")):
        free_port(FRONTEND_PORT, LISTEN_HOST)
        frontend_args = [ROOT / "frontend/node_modules/.bin/next", "start", "--hostname", LISTEN_HOST, "--port", FRONTEND_PORT]
        try:
            spawn(state, "frontend", frontend_args, frontend_environment(env, staged_frontend or previous_frontend_stage), ROOT / "frontend")
            wait_for(lambda: http_ready(FRONTEND_PORT, "/"), "前端", process=state.get("frontend"))
            if staged_frontend:
                record_frontend_stage(staged_frontend)
        except Exception:
            if staged_frontend and frontend_was_running:
                stop_managed(state, "frontend")
                free_port(FRONTEND_PORT, LISTEN_HOST)
                spawn(state, "frontend", frontend_args, frontend_environment(env, previous_frontend_stage), ROOT / "frontend")
                wait_for(lambda: http_ready(FRONTEND_PORT, "/"), "恢复原前端", process=state.get("frontend"))
            raise
    else:
        wait_for(lambda: http_ready(FRONTEND_PORT, "/"), "前端", process=state.get("frontend"))
    if args.ui_only:
        print(f"本地界面已启动：监听 {LISTEN_HOST}:{FRONTEND_PORT}，本机访问 http://127.0.0.1:{FRONTEND_PORT}，其他设备访问 http://<服务器IP>:{FRONTEND_PORT}（本次不启动扫描 Agent）")
        return
    check = subprocess.run([str(value) for value in agent_args() + ["--check"]], env=env)
    if check.returncode and not args.no_tools_install:
        print("准备本地扫描工具……", flush=True)
        tools_env = env.copy(); tools_env.update({"SKYSHIELD_GO": go_binary(), "SKYSHIELD_LOCAL_DATA_DIR": str(DATA)})
        call(["bash", ROOT / "scripts/local-tools.sh"], cwd=ROOT, env=tools_env)
        check = subprocess.run([str(value) for value in agent_args() + ["--check"]], env=env)
    if check.returncode:
        fail("本地扫描工具未准备完成；Server 和前端仍在运行，Agent 未领取任务")
    if not alive(state.get("agent")):
        agent_env = {key: os.environ[key] for key in ("PATH", "LANG", "LC_ALL", "TZ", "HTTP_PROXY", "HTTPS_PROXY", "NO_PROXY", "http_proxy", "https_proxy", "no_proxy", "SSL_CERT_FILE", "SSL_CERT_DIR", "CHROME_PATH") if key in os.environ}
        spawn(state, "agent", agent_args(), agent_env, ROOT)
    identity = env["TIANQIONG_LOCAL_AGENT_INSTANCE_ID"].replace("'", "''")
    wait_for(lambda: query(env, f"SELECT count(*) FROM agent a JOIN agent_runtime_status r ON r.agent_id=a.id WHERE a.instance_id='{identity}' AND a.status='online' AND r.last_heartbeat > now() - interval '1 minute'") == "1", "本地扫描 Agent", process=state.get("agent"))
    print(f"单机模式已启动：监听 {LISTEN_HOST}:{FRONTEND_PORT}，本机访问 http://127.0.0.1:{FRONTEND_PORT}，其他设备访问 http://<服务器IP>:{FRONTEND_PORT}")


def stop(_args):
    state = read_state()
    for name in ("agent", "frontend", "server", "redis"):
        entry = state.get(name)
        if alive(entry) and not entry.get("owned"):
            state.pop(name, None); write_state(state)
            continue
        stop_managed(state, name)
    entry = state.get("postgres")
    if alive(entry) and entry.get("owned"):
        call([pg_binary("pg_ctl"), "-D", PGDATA, "-m", "fast", "-w", "stop"])
    state.pop("postgres", None)
    write_state(state)
    print("已停止由单机启动脚本管理的进程。")


def status(_args):
    state = read_state()
    for name in ("postgres", "redis", "server", "frontend", "agent"):
        entry = state.get(name)
        print(f"{name}: {'运行中' if alive(entry) else '未运行'}" + (f" (pid {entry['pid']})" if alive(entry) else ""))


def main():
    parser = argparse.ArgumentParser(description="SkyShield 本地单机部署")
    sub = parser.add_subparsers(dest="command", required=True)
    start_parser = sub.add_parser("start")
    start_parser.add_argument("--no-build", action="store_true")
    start_parser.add_argument("--no-tools-install", action="store_true")
    start_parser.add_argument("--ui-only", action="store_true")
    start_parser.set_defaults(function=start)
    run_parser = sub.add_parser("run", help="启动已部署的程序，不构建或安装工具")
    run_parser.add_argument("--ui-only", action="store_true")
    run_parser.set_defaults(function=run_existing)
    restart_parser = sub.add_parser("restart", help="重启已部署的程序，不构建或安装工具")
    restart_parser.add_argument("--ui-only", action="store_true")
    restart_parser.set_defaults(function=restart_existing)
    sub.add_parser("stop").set_defaults(function=stop)
    sub.add_parser("status").set_defaults(function=status)
    args = parser.parse_args()
    try:
        DATA.mkdir(parents=True, exist_ok=True, mode=0o700)
        with (DATA / "local-deploy.lock").open("a") as lock:
            fcntl.flock(lock, fcntl.LOCK_EX | fcntl.LOCK_NB)
            args.function(args)
    except BlockingIOError:
        print("单机部署操作正在进行，请稍后重试。", file=sys.stderr)
        return 1
    except (OSError, RuntimeError, ValueError, KeyError, json.JSONDecodeError) as error:
        print(f"单机部署失败：{error}", file=sys.stderr)
        return 1
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
