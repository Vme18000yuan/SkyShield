import json
import os
from pathlib import Path
import shutil
import stat
import subprocess
import tempfile
import unittest


ROOT = Path(__file__).resolve().parent.parent
VOLUMES = ("lunafox_data", "lunafox_engine_execution", "lunafox_agent_state")


class DockerDeployTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory(prefix="skyshield-docker-test-")
        self.root = Path(self.temp.name)
        (self.root / "scripts").mkdir()
        (self.root / "bin").mkdir()
        shutil.copy2(ROOT / "docker.sh", self.root / "docker.sh")
        shutil.copy2(ROOT / "scripts/tianqiong-dev-init.sh", self.root / "scripts/tianqiong-dev-init.sh")
        shutil.copy2(ROOT / "scripts/deployment-network.py", self.root / "scripts/deployment-network.py")
        (self.root / "compose.tianqiong.yaml").write_text("services: {}\n")
        (self.root / "compose.hub.yaml").write_text("services: {}\n")
        fake = self.root / "bin/docker"
        fake.write_text('''#!/usr/bin/env python3
import json, os, sys
args = sys.argv[1:]
with open(os.environ["SKYSHIELD_TEST_DOCKER_CALLS"], "a") as f:
    f.write(json.dumps(args) + "\\n")
owners = json.loads(os.environ.get("SKYSHIELD_TEST_VOLUME_OWNERS", "{}"))
state_path = os.environ["SKYSHIELD_TEST_CONTAINER_STATES"]
try:
    with open(state_path) as f:
        states = json.load(f)
except FileNotFoundError:
    states = {}
if args[:2] == ["volume", "inspect"]:
    if args[2] not in owners:
        sys.exit(1)
    if "--format" in args:
        print(owners[args[2]])
if "compose" in args and "ps" in args and "--quiet" in args:
    service = args[-1]
    if service != os.environ.get("SKYSHIELD_TEST_MISSING_CONTAINER"):
        print("fixture-" + service)
if args and args[0] == "inspect":
    service = args[-1].removeprefix("fixture-")
    state = states.get(service, "exited")
    fmt = args[args.index("--format") + 1]
    if ".State.ExitCode" in fmt:
        code = 1 if service == os.environ.get("SKYSHIELD_TEST_FAILED_INIT") else 0
        print(f"{state} {code}")
    elif ".State.Health" in fmt:
        health = "healthy" if state == "running" and service in ("postgres", "redis", "loki", "server", "frontend") else "none"
        print(f"{state} {health}")
    else:
        print(state)
if args and args[0] in ("start", "stop"):
    service = args[-1].removeprefix("fixture-")
    states[service] = "running" if args[0] == "start" else "exited"
    with open(state_path, "w") as f:
        json.dump(states, f)
if "port" in args and "nginx" in args:
    print("0.0.0.0:18443")
''')
        fake.chmod(0o755)
        self.env = os.environ.copy()
        self.env["SKYSHIELD_INHERIT_HOST_NETWORK"] = "0"
        self.env["PATH"] = str(self.root / "bin") + os.pathsep + self.env["PATH"]
        self.env["SKYSHIELD_TEST_DOCKER_CALLS"] = str(self.root / "calls.jsonl")
        self.env["SKYSHIELD_TEST_CONTAINER_STATES"] = str(self.root / "states.json")

    def tearDown(self):
        self.temp.cleanup()

    def run_docker(self, *args, owners=None):
        env = self.env.copy()
        env["SKYSHIELD_TEST_VOLUME_OWNERS"] = json.dumps(owners or {})
        return subprocess.run([str(self.root / "docker.sh"), *args], cwd=self.root, env=env, capture_output=True, text=True)

    def calls(self):
        return [json.loads(line) for line in (self.root / "calls.jsonl").read_text().splitlines()]

    def test_existing_foreign_volume_stops_before_initialization_or_start(self):
        for action in ("start", "pull"):
            with self.subTest(action=action):
                result = self.run_docker(action, owners={"lunafox_data": "lunafox"})
                self.assertNotEqual(result.returncode, 0)
                self.assertIn("lunafox_data", result.stderr)
                self.assertFalse((self.root / ".local").exists())
                self.assertFalse(any("up" in call for call in self.calls()))

    def test_fresh_start_generates_private_keys_once_and_reuses_them(self):
        self.assertEqual(self.run_docker().returncode, 0)
        paths = [self.root / ".local/secrets" / key for key in ("db_password", "jwt_secret", "ai_encryption_key")]
        before = [path.read_bytes() for path in paths]
        self.assertEqual(len(set(before)), 3)
        self.assertTrue(all(b"\n" not in value and b"\r" not in value for value in before))
        self.assertTrue(all(stat.S_IMODE(path.stat().st_mode) == 0o600 for path in paths))
        self.assertEqual(self.run_docker("start").returncode, 0)
        self.assertEqual([path.read_bytes() for path in paths], before)
        self.assertEqual(sum("up" in call and "--build" in call for call in self.calls()), 2)

    def test_existing_volumes_owned_by_this_project_are_accepted(self):
        result = self.run_docker(owners={volume: "tianqiong" for volume in VOLUMES})
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertTrue(any("up" in call for call in self.calls()))

    def test_existing_secret_files_with_newlines_are_preserved(self):
        secret_dir = self.root / ".local/secrets"
        secret_dir.mkdir(parents=True, mode=0o700)
        paths = [secret_dir / name for name in ("db_password", "jwt_secret", "ai_encryption_key")]
        for path in paths:
            path.write_bytes(b"existing-fixture-secret\n")
            path.chmod(0o600)
        result = self.run_docker("pull")
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual([path.read_bytes() for path in paths], [b"existing-fixture-secret\n"] * 3)

    def test_pull_mode_fetches_published_images_and_never_builds(self):
        result = self.run_docker("pull")
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertIn("https://localhost:18443", result.stdout)
        self.assertIn("https://<服务器IP>:18443", result.stdout)
        calls = self.calls()
        pulls = [i for i, call in enumerate(calls) if "pull" in call and "up" not in call]
        starts = [i for i, call in enumerate(calls) if "up" in call]
        self.assertEqual(len(pulls), 1)
        self.assertEqual(len(starts), 1)
        self.assertLess(pulls[0], starts[0])
        self.assertIn("--no-build", calls[starts[0]])
        self.assertNotIn("--build", calls[starts[0]])
        self.assertTrue(any(item.endswith("compose.hub.yaml") for item in calls[starts[0]]))

    def test_stop_and_status_do_not_inspect_or_delete_volumes(self):
        self.assertEqual(self.run_docker("status").returncode, 0)
        self.assertEqual(self.run_docker("stop").returncode, 0)
        calls = self.calls()
        self.assertTrue(any("stop" in call for call in calls))
        self.assertFalse(any(call[:2] == ["volume", "inspect"] or "down" in call or "--volumes" in call for call in calls))

    def test_run_starts_existing_containers_in_dependency_order_without_build_or_pull(self):
        result = self.run_docker("run")
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertIn("https://localhost:18443", result.stdout)
        calls = self.calls()
        starts = [call[-1] for call in calls if call[0] == "start"]
        self.assertEqual(starts, ["fixture-" + service for service in ("postgres", "redis", "loki", "server", "frontend", "nginx", "agent")])
        self.assertFalse(any("up" in call or "pull" in call or "--build" in call or "down" in call for call in calls))
        self.assertFalse((self.root / ".local").exists())

    def test_restart_stops_and_starts_existing_services_without_reinitializing(self):
        services = ("postgres", "redis", "loki", "server", "frontend", "nginx", "agent")
        Path(self.env["SKYSHIELD_TEST_CONTAINER_STATES"]).write_text(json.dumps({name: "running" for name in services}))
        secret = self.root / ".local/secrets/db_password"
        secret.parent.mkdir(parents=True)
        secret.write_text("existing fixture")
        result = self.run_docker("restart")
        self.assertEqual(result.returncode, 0, result.stderr)
        calls = self.calls()
        stops = [call[-1] for call in calls if call[0] == "stop"]
        starts = [call[-1] for call in calls if call[0] == "start"]
        self.assertEqual(stops, ["fixture-" + name for name in ("agent", "nginx", "frontend", "server", "loki", "redis", "postgres")])
        self.assertEqual(starts, ["fixture-" + name for name in services])
        self.assertEqual(secret.read_text(), "existing fixture")
        self.assertFalse(any("up" in call or "pull" in call or "--build" in call or "down" in call for call in calls))

    def test_restart_refuses_missing_or_failed_initialization_before_stopping_services(self):
        for key, value in (("SKYSHIELD_TEST_MISSING_CONTAINER", "server"), ("SKYSHIELD_TEST_FAILED_INIT", "bootstrap")):
            with self.subTest(case=key):
                self.env[key] = value
                result = self.run_docker("restart")
                self.assertNotEqual(result.returncode, 0)
                self.assertFalse(any(call[0] in ("start", "stop") for call in self.calls()))
                self.env.pop(key)


class ComposeListenerTests(unittest.TestCase):
    def test_edge_publishes_on_all_ipv4_interfaces(self):
        docker = shutil.which("docker")
        if not docker or subprocess.run([docker, "compose", "version"], stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL).returncode:
            self.skipTest("Docker Compose is not installed")
        env = os.environ.copy()
        env["PUBLIC_PORT"] = "18443"
        result = subprocess.run(
            [docker, "compose", "-f", str(ROOT / "compose.tianqiong.yaml"), "-f", str(ROOT / "compose.hub.yaml"),
             "config", "--format", "json"], cwd=ROOT, env=env, capture_output=True, text=True, check=True,
        )
        ports = json.loads(result.stdout)["services"]["nginx"]["ports"]
        self.assertTrue(any(port.get("host_ip") == "0.0.0.0" and int(port.get("target", 0)) == 443
                            and str(port.get("published")) == "18443" for port in ports))


if __name__ == "__main__":
    unittest.main()
