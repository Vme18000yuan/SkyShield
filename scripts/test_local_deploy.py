import importlib.util
import contextlib
import io
import os
import pathlib
import socket
import subprocess
import tempfile
import time
import unittest
from types import SimpleNamespace
from unittest import mock

SCRIPT = pathlib.Path(__file__).with_name("local-deploy.py")
SPEC = importlib.util.spec_from_file_location("skyshield_local_deploy", SCRIPT)
deploy = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(deploy)


class LocalDeployTests(unittest.TestCase):
    def test_agent_inherits_host_proxy_without_server_credentials(self):
        with mock.patch.dict(os.environ, {"ALL_PROXY": "socks5://127.0.0.1:1080", "no_proxy": ".corp.example",
                                          "DB_PASSWORD": "fixture-only", "TIANQIONG_AI_ENCRYPTION_KEY": "fixture-only"}):
            env = deploy.agent_environment()
        self.assertEqual(env['ALL_PROXY'], 'socks5://127.0.0.1:1080')
        self.assertEqual(env['no_proxy'], '.corp.example')
        self.assertNotIn('DB_PASSWORD', env)
        self.assertNotIn('TIANQIONG_AI_ENCRYPTION_KEY', env)

    def test_old_engine_binary_reports_how_to_rebuild(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = pathlib.Path(temporary)
            binary = root / "skyshield-local-engines"
            binary.touch()
            old_error = "local Engine package update: local mode requires loopback SERVER_HOST and SERVER_GRPC_HOST\n"
            result = SimpleNamespace(returncode=1, stdout="", stderr=old_error)
            with mock.patch.object(deploy, "BIN", root), mock.patch.object(deploy.subprocess, "run", return_value=result), \
                 contextlib.redirect_stderr(io.StringIO()):
                with self.assertRaisesRegex(RuntimeError, "local.sh start --no-tools-install"):
                    deploy.update_local_engines({}, no_build=True)
            result.stderr = "running scan tasks must finish before updating native Engine packages\n"
            with mock.patch.object(deploy, "BIN", root), mock.patch.object(deploy.subprocess, "run", return_value=result), \
                 contextlib.redirect_stderr(io.StringIO()):
                with self.assertRaisesRegex(RuntimeError, "退出码 1") as error:
                    deploy.update_local_engines({}, no_build=True)
                self.assertNotIn("重新构建", str(error.exception))

    def test_daily_start_and_restart_use_existing_build_without_installing_tools(self):
        args = SimpleNamespace(ui_only=False)
        with mock.patch.object(deploy, "check_existing_runtime") as check, mock.patch.object(deploy, "start") as start:
            deploy.run_existing(args)
            check.assert_called_once_with(args)
            start.assert_called_once_with(args)
            self.assertTrue(args.no_build)
            self.assertTrue(args.no_tools_install)

        args = SimpleNamespace(ui_only=True)
        order = []
        with mock.patch.object(deploy, "check_existing_runtime", side_effect=lambda _: order.append("check")), \
             mock.patch.object(deploy, "read_state", return_value={}), \
             mock.patch.object(deploy, "free_port"), \
             mock.patch.object(deploy, "stop", side_effect=lambda _: order.append("stop")), \
             mock.patch.object(deploy, "start", side_effect=lambda _: order.append("start")):
            deploy.restart_existing(args)
        self.assertEqual(order, ["check", "stop", "start"])
        self.assertTrue(args.no_build)
        self.assertTrue(args.no_tools_install)
        self.assertTrue(args.ui_only)

    def test_restart_does_not_stop_services_if_preflight_fails_or_process_is_unmanaged(self):
        args = SimpleNamespace(ui_only=False)
        with mock.patch.object(deploy, "check_existing_runtime", side_effect=RuntimeError("missing build")), \
             mock.patch.object(deploy, "stop") as stop:
            with self.assertRaisesRegex(RuntimeError, "missing build"):
                deploy.restart_existing(args)
            stop.assert_not_called()
        with mock.patch.object(deploy, "check_existing_runtime"), \
             mock.patch.object(deploy, "read_state", return_value={"server": {"owned": False}}), \
             mock.patch.object(deploy, "alive", side_effect=lambda entry: bool(entry)), \
             mock.patch.object(deploy, "stop") as stop:
            with self.assertRaisesRegex(RuntimeError, "其他方式启动"):
                deploy.restart_existing(args)
            stop.assert_not_called()

    def test_existing_runtime_requires_build_database_and_original_secrets(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = pathlib.Path(temporary)
            binary = root / "bin"
            pgdata = root / "postgres/data"
            secrets = root / "secrets"
            agent = root / "agent/bootstrap.json"
            next_bin = root / "frontend/node_modules/.bin/next"
            build = root / "frontend/.next/BUILD_ID"
            for directory in (binary, pgdata, secrets, agent.parent, next_bin.parent, build.parent):
                directory.mkdir(parents=True, exist_ok=True)
            for path in (binary / "skyshield-server", binary / "skyshield-local-agent", next_bin):
                path.touch(mode=0o700)
            (pgdata / "PG_VERSION").write_text("16")
            agent.write_text('{"instanceId":"fixture","agentId":1,"agentAuthenticationToken":"fixture"}')
            build.write_text("existing")
            for name in ("db_password", "jwt_secret", "ai_encryption_key"):
                path = secrets / name
                path.write_text("fixture")
                path.chmod(0o600)
            patches = {"ROOT": root, "BIN": binary, "PGDATA": pgdata, "SECRETS": secrets,
                       "AGENT_CREDENTIALS": agent, "FRONTEND_BUILD": root / "missing-pointer.json"}
            with contextlib.ExitStack() as stack:
                for name, path in patches.items():
                    stack.enter_context(mock.patch.object(deploy, name, path))
                stack.enter_context(mock.patch.object(deploy, "pg_binary", return_value="fixture"))
                stack.enter_context(mock.patch.object(deploy, "executable", return_value="fixture"))
                deploy.check_existing_runtime(SimpleNamespace(ui_only=True))
                (secrets / "ai_encryption_key").unlink()
                with self.assertRaisesRegex(RuntimeError, "密钥缺失"):
                    deploy.check_existing_runtime(SimpleNamespace(ui_only=True))
                self.assertFalse((secrets / "ai_encryption_key").exists())

    def test_prepare_directories_preserves_wordlist_bootstrap_state(self):
        with tempfile.TemporaryDirectory() as temporary:
            data = pathlib.Path(temporary) / "data"
            paths = {
                "DATA": data, "LOGS": data / "logs", "BIN": data / "bin",
                "ENGINES": data / "bin/engines", "TOOLS": data / "tools/bin",
                "PGSOCKET": data / "postgres", "SECRETS": data / "secrets",
                "EXECUTION_ROOT": pathlib.Path(temporary) / "execution",
            }
            with contextlib.ExitStack() as stack:
                for name, path in paths.items():
                    stack.enter_context(mock.patch.object(deploy, name, path))
                deploy.prepare_directories()
                wordlists = data / "wordlists"
                self.assertFalse(wordlists.exists())
                wordlists.mkdir()
                existing = wordlists / "user-list.txt"
                existing.write_text("keep this data\n")
                deploy.prepare_directories()
                self.assertEqual(existing.read_text(), "keep this data\n")

    def test_rebuild_does_not_interrupt_running_scan(self):
        state = {"server": {"pid": 1}, "agent": {"pid": 2}, "frontend": {"pid": 3}}
        with mock.patch.object(deploy, "prepare_directories"), mock.patch.object(deploy, "environment", return_value={}), mock.patch.object(deploy, "active_frontend_stage", return_value=None), \
             mock.patch.object(deploy, "read_state", return_value=state), mock.patch.object(deploy, "frontend_needs_build", return_value=True), \
             mock.patch.object(deploy, "build", return_value=(True, True)), mock.patch.object(deploy, "alive", side_effect=lambda item: bool(item)), \
             mock.patch.object(deploy, "local_agent_busy", return_value=True), mock.patch.object(deploy, "stop_managed") as stop, \
             mock.patch.object(deploy, "build_frontend") as frontend:
            with self.assertRaisesRegex(RuntimeError, "不会|避免中断"):
                deploy.start(SimpleNamespace(no_build=False, ui_only=False, no_tools_install=True))
            stop.assert_not_called()
            frontend.assert_not_called()
            deploy.build.assert_not_called()

    def test_failed_frontend_build_preserves_running_services(self):
        state = {"server": {"pid": 1, "owned": True}, "agent": {"pid": 2, "owned": True}, "frontend": {"pid": 3, "owned": True}}
        with mock.patch.object(deploy, "prepare_directories"), mock.patch.object(deploy, "environment", return_value={}), \
             mock.patch.object(deploy, "read_state", return_value=state), mock.patch.object(deploy, "active_frontend_stage", return_value=None), \
             mock.patch.object(deploy, "frontend_needs_build", return_value=True), mock.patch.object(deploy, "alive", side_effect=lambda item: bool(item)), \
             mock.patch.object(deploy, "local_agent_busy", return_value=False), mock.patch.object(deploy, "stop_managed") as stop, \
             mock.patch.object(deploy, "build") as backend, mock.patch.object(deploy, "build_frontend", side_effect=RuntimeError("fixture build failed")):
            with self.assertRaisesRegex(RuntimeError, "fixture build failed"):
                deploy.start(SimpleNamespace(no_build=False, ui_only=False, no_tools_install=True))
            stop.assert_not_called()
            backend.assert_not_called()
            self.assertEqual(len(state), 3)

    def test_failed_backend_build_keeps_installed_binaries(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = pathlib.Path(temporary)
            binary = root / "bin"
            engines = binary / "engines"
            engines.mkdir(parents=True)
            (binary / "skyshield-server").write_text("old server")
            (binary / "skyshield-local-agent").write_text("old agent")

            def fake_build(go, output, package, cwd, env):
                if package == "./cmd/local-agent":
                    raise RuntimeError("agent build failed")
                output.write_text("new server")
                return True

            with mock.patch.object(deploy, "DATA", root), mock.patch.object(deploy, "ROOT", root), \
                 mock.patch.object(deploy, "BIN", binary), mock.patch.object(deploy, "ENGINES", engines), \
                 mock.patch.object(deploy, "go_binary", return_value="go"), mock.patch.object(deploy, "build_binary", side_effect=fake_build):
                with self.assertRaisesRegex(RuntimeError, "agent build failed"):
                    deploy.build({})
            self.assertEqual((binary / "skyshield-server").read_text(), "old server")
            self.assertEqual((binary / "skyshield-local-agent").read_text(), "old agent")

    def test_failed_binary_promotion_restores_previous_files(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = pathlib.Path(temporary)
            first, second = root / "first", root / "second"
            next_first, next_second = root / "next-first", root / "next-second"
            first.write_text("old first")
            second.write_text("old second")
            next_first.write_text("new first")
            next_second.write_text("new second")
            original_replace = pathlib.Path.replace

            def replace(path, destination):
                if path == next_second:
                    raise OSError("promotion failed")
                return original_replace(path, destination)

            with mock.patch.object(pathlib.Path, "replace", replace):
                with self.assertRaisesRegex(OSError, "promotion failed"):
                    deploy.promote_binaries([(next_first, first), (next_second, second)], root)
            self.assertEqual(first.read_text(), "old first")
            self.assertEqual(second.read_text(), "old second")

    def test_scan_started_during_build_prevents_binary_promotion(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = pathlib.Path(temporary)
            binary = root / "bin"
            engines = binary / "engines"
            engines.mkdir(parents=True)
            for name in ("skyshield-server", "skyshield-local-agent", "skyshield-local-engines"):
                (binary / name).write_text("old-" + name)
            state = {name: {"pid": index, "owned": True} for index, name in enumerate(("server", "agent", "frontend"), 1)}

            def compile_fixture(go, output, package, cwd, env):
                output.write_text("new-" + output.name)
                return True

            with contextlib.ExitStack() as stack:
                def patch(name, *args, **kwargs):
                    return stack.enter_context(mock.patch.object(deploy, name, *args, **kwargs))
                patch("ROOT", root)
                patch("DATA", root)
                patch("BIN", binary)
                patch("ENGINES", engines)
                patch("ENGINE_NAMES", ())
                patch("prepare_directories")
                patch("environment", return_value={})
                patch("read_state", return_value=state)
                patch("active_frontend_stage", return_value=None)
                patch("frontend_needs_build", return_value=False)
                patch("go_binary", return_value="go")
                patch("build_binary", side_effect=compile_fixture)
                stack.enter_context(mock.patch.dict(os.environ, {"SKYSHIELD_UPDATER_CHILD": "1"}))
                patch("alive", side_effect=lambda item: bool(item))
                patch("local_agent_busy", side_effect=[False, True])
                stop = patch("stop_managed")
                with self.assertRaisesRegex(RuntimeError, "避免中断"):
                    deploy.start(SimpleNamespace(no_build=False, ui_only=True, no_tools_install=True))
            stop.assert_not_called()
            self.assertEqual((binary / "skyshield-server").read_text(), "old-skyshield-server")
            self.assertEqual((binary / "skyshield-local-agent").read_text(), "old-skyshield-local-agent")

    def test_staged_frontend_build_keeps_existing_output_and_tsconfig(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = pathlib.Path(temporary)
            frontend = root / "frontend"
            next_bin = frontend / "node_modules/.bin/next"
            next_bin.parent.mkdir(parents=True)
            next_bin.touch()
            (frontend / ".next").mkdir()
            (frontend / ".next/BUILD_ID").write_text("old build")
            (frontend / "tsconfig.json").write_text('{"compilerOptions":{}}\n')
            previous = b'/// <reference path="./.next/types/routes.d.ts" />\n'
            (frontend / "next-env.d.ts").write_bytes(previous)

            def fake_build(args, *, cwd=None, env=None, capture=False):
                stage = env["SKYSHIELD_NEXT_STAGE_DIR"]
                (frontend / stage).mkdir()
                (frontend / stage / "BUILD_ID").write_text("candidate")
                (frontend / "next-env.d.ts").write_bytes(previous.replace(b"./.next/", ("./" + stage + "/").encode()))
                (frontend / ("tsconfig." + stage + ".json")).write_text("generated candidate config")

            with mock.patch.object(deploy, "ROOT", root), mock.patch.object(deploy, "call", side_effect=fake_build), \
                 mock.patch.object(deploy, "preview_frontend") as preview:
                stage = deploy.build_frontend({"PATH": os.environ.get("PATH", "")})
            self.assertEqual((frontend / ".next/BUILD_ID").read_text(), "old build")
            self.assertEqual((frontend / "tsconfig.json").read_text(), '{"compilerOptions":{}}\n')
            self.assertEqual((frontend / "next-env.d.ts").read_bytes(), previous)
            self.assertEqual((frontend / stage / "skyshield-local-api-origin").read_text().strip(), f"127.0.0.1:{deploy.HTTP_PORT}")
            self.assertEqual(preview.call_args.args[0], stage)

    def test_preview_starts_and_stops_a_separate_frontend(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = pathlib.Path(temporary)
            next_bin = root / "frontend/node_modules/.bin/next"
            next_bin.parent.mkdir(parents=True)
            next_bin.write_text("#!/usr/bin/env python3\nimport http.server,sys\nport=int(sys.argv[sys.argv.index('--port')+1])\nhttp.server.HTTPServer(('127.0.0.1',port),http.server.SimpleHTTPRequestHandler).serve_forever()\n")
            next_bin.chmod(0o700)
            stage = root / "frontend/.next-skyshield-stage-fixture"
            (stage / "static/fixture").mkdir(parents=True)
            (stage / "BUILD_ID").write_text("fixture")
            (stage / "static/fixture/_buildManifest.js").touch()
            static = root / "frontend/_next/static/fixture"
            static.mkdir(parents=True)
            (static / "_buildManifest.js").touch()
            logs = root / "logs"
            logs.mkdir()
            with mock.patch.object(deploy, "ROOT", root), mock.patch.object(deploy, "LOGS", logs):
                deploy.preview_frontend(".next-skyshield-stage-fixture", {"PATH": os.environ.get("PATH", "")})
            self.assertTrue((logs / "local-frontend-preview.log").exists())

    def test_invalid_active_frontend_record_is_rejected(self):
        with tempfile.TemporaryDirectory() as temporary:
            pointer = pathlib.Path(temporary) / "frontend-build.json"
            pointer.write_text('{"stage":"../../outside"}')
            with mock.patch.object(deploy, "FRONTEND_BUILD", pointer):
                with self.assertRaisesRegex(RuntimeError, "记录无效"):
                    deploy.active_frontend_stage()

    def test_failed_candidate_start_restores_running_frontend(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = pathlib.Path(temporary)
            binary = root / "bin"
            binary.mkdir()
            for name in ("skyshield-server", "skyshield-local-agent"):
                (binary / name).touch()
            credentials = root / "agent.json"
            credentials.write_text('{"instanceId":"fixture"}')
            state = {name: {"pid": index, "owned": True} for index, name in enumerate(("server", "agent", "frontend", "redis"), 1)}
            launches = []

            def stop(current, name):
                current.pop(name, None)

            def spawn(current, name, args, env, cwd):
                launches.append((name, env.get("SKYSHIELD_NEXT_STAGE_DIR")))
                current[name] = {"pid": 50 + len(launches), "owned": True}

            def wait(predicate, description, **kwargs):
                if description == "前端":
                    raise RuntimeError("candidate failed")

            with contextlib.ExitStack() as stack:
                def patch(name, *args, **kwargs):
                    return stack.enter_context(mock.patch.object(deploy, name, *args, **kwargs))
                patch("ROOT", root)
                patch("BIN", binary)
                patch("AGENT_CREDENTIALS", credentials)
                patch("prepare_directories")
                patch("environment", return_value={})
                patch("read_state", return_value=state)
                patch("active_frontend_stage", return_value=None)
                patch("frontend_needs_build", return_value=True)
                patch("build_frontend", return_value=".next-skyshield-stage-fixture")
                def successful_build(env, validate):
                    validate(False, False)
                    return False, False
                patch("build", side_effect=successful_build)
                patch("alive", side_effect=lambda item: bool(item))
                patch("local_agent_busy", return_value=False)
                patch("matches_binary", return_value=True)
                patch("stop_managed", side_effect=stop)
                patch("spawn", side_effect=spawn)
                patch("wait_for", side_effect=wait)
                patch("postgres_start")
                patch("free_port")
                patch("query", return_value="1")
                patch("call")
                record = patch("record_frontend_stage")
                with self.assertRaisesRegex(RuntimeError, "candidate failed"):
                    deploy.start(SimpleNamespace(no_build=False, ui_only=True, no_tools_install=True))
            self.assertEqual(launches, [("frontend", ".next-skyshield-stage-fixture"), ("frontend", None)])
            self.assertIn("frontend", state)
            record.assert_not_called()

    def test_active_frontend_pointer_only_accepts_completed_stage(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = pathlib.Path(temporary)
            stage = ".next-skyshield-stage-fixture"
            directory = root / "frontend" / stage
            directory.mkdir(parents=True)
            (directory / "BUILD_ID").write_text("candidate")
            pointer = root / "frontend-build.json"
            with mock.patch.object(deploy, "ROOT", root), mock.patch.object(deploy, "FRONTEND_BUILD", pointer):
                deploy.record_frontend_stage(stage)
                self.assertEqual(deploy.active_frontend_stage(), stage)
                (directory / "BUILD_ID").unlink()
                with self.assertRaisesRegex(RuntimeError, "目录不存在或无效"):
                    deploy.active_frontend_stage()

    def test_pid_identity_and_port_collision(self):
        self.assertTrue(deploy.alive({"pid": os.getpid(), "start": deploy.process_start(os.getpid())}))
        self.assertFalse(deploy.alive({"pid": os.getpid(), "start": "not-the-same-process"}))
        with socket.socket() as listener:
            listener.bind(("127.0.0.1", 0))
            listener.listen()
            with self.assertRaises(RuntimeError):
                deploy.free_port(listener.getsockname()[1])

    def test_process_identity_includes_boot_and_rejects_missing_start(self):
        entry = {"pid": os.getpid(), "start": deploy.process_start(os.getpid()), "boot_id": "previous-boot"}
        self.assertFalse(deploy.alive(entry))
        entry['boot_id'] = deploy.boot_id()
        self.assertTrue(deploy.alive(entry))
        self.assertFalse(deploy.alive({"pid": 0, "start": None}))

    def test_stale_postgres_pid_is_archived_without_touching_reused_process(self):
        for started in (1, int(time.time())):
            with self.subTest(started=started), tempfile.TemporaryDirectory() as directory, contextlib.ExitStack() as stack:
                root = pathlib.Path(directory)
                data, logs = root / 'data', root / 'logs'
                data.mkdir(); logs.mkdir()
                with socket.socket() as listener:
                    listener.bind(('127.0.0.1', 0)); port = listener.getsockname()[1]
                contents = f'{os.getpid()}\n{data}\n{started}\n{port}\n{root}\n127.0.0.1\n'
                pid_file = data / 'postmaster.pid'; pid_file.write_text(contents)
                lock = root / f'.s.PGSQL.{port}.lock'; lock.write_text(contents)
                sentinel = data / 'existing-data'; sentinel.write_text('preserved')
                for name, value in {'PGDATA': data, 'PGSOCKET': root, 'PG_PORT': port, 'LOGS': logs, 'STATE': root / 'state.json'}.items():
                    stack.enter_context(mock.patch.object(deploy, name, value))
                identity = deploy.process_start(os.getpid())
                state = {'postgres': {'pid': os.getpid(), 'start': identity, 'owned': False}}
                with contextlib.redirect_stdout(io.StringIO()):
                    self.assertIsNone(deploy.reconcile_postgres_pid(state))
                self.assertFalse(pid_file.exists()); self.assertFalse(lock.exists())
                archive = next(logs.iterdir())
                self.assertEqual((archive / 'postmaster.pid').read_text(), contents)
                self.assertEqual((archive / lock.name).read_text(), contents)
                self.assertEqual(sentinel.read_text(), 'preserved')
                self.assertEqual(deploy.process_start(os.getpid()), identity)
                self.assertNotIn('postgres', state)

    def test_postgres_locks_are_preserved_when_listener_or_lock_owner_disagrees(self):
        for conflict in ('tcp', 'unix', 'lock'):
            with self.subTest(conflict=conflict), tempfile.TemporaryDirectory() as directory, contextlib.ExitStack() as stack:
                root = pathlib.Path(directory); data = root / 'data'; logs = root / 'logs'
                data.mkdir(); logs.mkdir()
                listener = stack.enter_context(socket.socket())
                listener.bind(('127.0.0.1', 0)); port = listener.getsockname()[1]
                if conflict == 'tcp': listener.listen()
                else: listener.close()
                contents = f'{os.getpid()}\n{data}\n1\n{port}\n{root}\n127.0.0.1\n'
                pid_file = data / 'postmaster.pid'; pid_file.write_text(contents)
                lock = root / f'.s.PGSQL.{port}.lock'
                lock.write_text('different owner' if conflict == 'lock' else contents)
                if conflict == 'unix':
                    unix = stack.enter_context(socket.socket(socket.AF_UNIX))
                    unix.bind(str(root / f'.s.PGSQL.{port}')); unix.listen()
                for name, value in {'PGDATA': data, 'PGSOCKET': root, 'PG_PORT': port, 'LOGS': logs}.items():
                    stack.enter_context(mock.patch.object(deploy, name, value))
                with self.assertRaises(RuntimeError):
                    deploy.reconcile_postgres_pid({})
                self.assertEqual(pid_file.read_text(), contents)
                self.assertTrue(lock.exists())
                self.assertEqual(list(logs.iterdir()), [])

    @unittest.skipUnless(os.environ.get('SKYSHIELD_TEST_POSTGRES_BIN'), 'isolated PostgreSQL test not configured')
    def test_postgres_restart_recovers_reused_pid_and_preserves_database(self):
        pg_bin = pathlib.Path(os.environ['SKYSHIELD_TEST_POSTGRES_BIN'])
        with tempfile.TemporaryDirectory(prefix='ss-pg-') as directory, contextlib.ExitStack() as stack:
            root = pathlib.Path(directory); logs = root / 'logs'; logs.mkdir()
            secrets = root / 'secrets'; secrets.mkdir()
            (secrets / 'db_password').write_text('local-deploy-test-only\n')
            with socket.socket() as listener:
                listener.bind(('127.0.0.1', 0)); port = listener.getsockname()[1]
            data = root / 'data'
            for name, value in {'PGDATA': data, 'PGSOCKET': root, 'PG_PORT': port, 'LOGS': logs,
                                'SECRETS': secrets, 'STATE': root / 'state.json', 'DB_NAME': 'tianqiong_local_deploy_test'}.items():
                stack.enter_context(mock.patch.object(deploy, name, value))
            stack.enter_context(mock.patch.object(deploy, 'pg_binary', side_effect=lambda name: str(pg_bin / name)))
            env = dict(os.environ, PGPASSWORD='local-deploy-test-only')
            state = {}
            try:
                deploy.postgres_start(state, env)
                deploy.query(env, "CREATE TABLE retained(value text); INSERT INTO retained VALUES ('preserved')")
                deploy.call([pg_bin / 'pg_ctl', '-D', data, '-m', 'fast', '-w', 'stop'])
                contents = f'{os.getpid()}\n{data}\n1\n{port}\n{root}\n127.0.0.1\n'
                (data / 'postmaster.pid').write_text(contents)
                (root / f'.s.PGSQL.{port}.lock').write_text(contents)
                with socket.socket(socket.AF_UNIX) as stale:
                    stale.bind(str(root / f'.s.PGSQL.{port}'))
                state['postgres'] = {'pid': os.getpid(), 'start': deploy.process_start(os.getpid()), 'owned': False}
                deploy.postgres_start(state, env)
                self.assertEqual(deploy.query(env, 'SELECT value FROM retained'), 'preserved')
                self.assertNotEqual(state['postgres']['pid'], os.getpid())
                self.assertTrue(state['postgres']['owned'])
                self.assertEqual(state['postgres']['boot_id'], deploy.boot_id())
                running = state['postgres']['pid']
                deploy.postgres_start(state, env)
                self.assertEqual(state['postgres']['pid'], running)
                state['postgres'] = {'pid': os.getpid(), 'start': deploy.process_start(os.getpid()), 'owned': False}
                deploy.postgres_start(state, env)
                self.assertEqual(state['postgres']['pid'], running)
                self.assertFalse(state['postgres']['owned'])
                with mock.patch.object(deploy, 'PG_PORT', 1 if port != 1 else 2):
                    with self.assertRaisesRegex(RuntimeError, '端口.*不一致'):
                        deploy.postgres_start(state, env)
                self.assertTrue(deploy.alive(state['postgres']))
            finally:
                pid_file = data / 'postmaster.pid'
                if pid_file.exists():
                    pid = int(pid_file.read_text().splitlines()[0])
                    try:
                        executable = pathlib.Path(os.readlink(f'/proc/{pid}/exe')).name
                        args = pathlib.Path(f'/proc/{pid}/cmdline').read_bytes().split(b'\0')
                    except OSError:
                        executable, args = '', []
                    if executable == 'postgres' and str(data).encode() in args:
                        subprocess.run([str(pg_bin / 'pg_ctl'), '-D', str(data), '-m', 'fast', '-w', 'stop'], check=True)

    def test_wildcard_listener_detects_other_address_collision(self):
        with socket.socket() as listener:
            listener.bind(("127.0.0.2", 0))
            listener.listen()
            port = listener.getsockname()[1]
            deploy.free_port(port, "127.0.0.1")
            with self.assertRaises(RuntimeError):
                deploy.free_port(port, deploy.LISTEN_HOST)

    def test_local_server_listener_configuration(self):
        with tempfile.TemporaryDirectory() as temporary:
            secrets = pathlib.Path(temporary)
            (secrets / "db_password").write_text("fixture-only\n")
            with mock.patch.object(deploy, "SECRETS", secrets):
                env = deploy.environment()
            self.assertEqual(env["SERVER_HOST"], "0.0.0.0")
            self.assertEqual(env["SERVER_GRPC_HOST"], "0.0.0.0")
            self.assertEqual(env["DB_HOST"], "127.0.0.1")

    def test_frontend_port_collision_is_detected_before_restart_and_saved_port_is_reusable(self):
        with tempfile.TemporaryDirectory() as directory, socket.socket() as listener:
            listener.bind(("127.0.0.1", 0))
            port = listener.getsockname()[1]
            listener.listen()
            marker = pathlib.Path(directory) / "frontend-port"
            marker.write_text("3300\n")
            with mock.patch.object(deploy, "FRONTEND_PORT_FILE", marker), mock.patch.object(deploy, "FRONTEND_PORT", port):
                self.assertEqual(deploy.saved_frontend_port(), 3300)
                with self.assertRaisesRegex(RuntimeError, "SKYSHIELD_LOCAL_FRONTEND_PORT"):
                    deploy.check_frontend_binding({}, restarting=True)


if __name__ == "__main__":
    unittest.main()
