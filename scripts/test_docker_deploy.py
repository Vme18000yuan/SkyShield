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
        (self.root / "compose.tianqiong.yaml").write_text("services: {}\n")
        (self.root / "compose.hub.yaml").write_text("services: {}\n")
        fake = self.root / "bin/docker"
        fake.write_text('''#!/usr/bin/env python3
import json, os, sys
args = sys.argv[1:]
with open(os.environ["SKYSHIELD_TEST_DOCKER_CALLS"], "a") as f:
    f.write(json.dumps(args) + "\\n")
owners = json.loads(os.environ.get("SKYSHIELD_TEST_VOLUME_OWNERS", "{}"))
if args[:2] == ["volume", "inspect"]:
    if args[2] not in owners:
        sys.exit(1)
    if "--format" in args:
        print(owners[args[2]])
''')
        fake.chmod(0o755)
        self.env = os.environ.copy()
        self.env["PATH"] = str(self.root / "bin") + os.pathsep + self.env["PATH"]
        self.env["SKYSHIELD_TEST_DOCKER_CALLS"] = str(self.root / "calls.jsonl")

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
        self.assertTrue(all(stat.S_IMODE(path.stat().st_mode) == 0o600 for path in paths))
        self.assertEqual(self.run_docker("start").returncode, 0)
        self.assertEqual([path.read_bytes() for path in paths], before)
        self.assertEqual(sum("up" in call and "--build" in call for call in self.calls()), 2)

    def test_existing_volumes_owned_by_this_project_are_accepted(self):
        result = self.run_docker(owners={volume: "tianqiong" for volume in VOLUMES})
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertTrue(any("up" in call for call in self.calls()))

    def test_pull_mode_fetches_published_images_and_never_builds(self):
        result = self.run_docker("pull")
        self.assertEqual(result.returncode, 0, result.stderr)
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


if __name__ == "__main__":
    unittest.main()
