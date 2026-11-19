import contextlib
import importlib.util
import io
import json
from pathlib import Path
import subprocess
import tempfile
from types import SimpleNamespace
import unittest
from unittest.mock import patch

import release_source

SPEC = importlib.util.spec_from_file_location("system_update", Path(__file__).with_name("system-update.py"))
updater = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(updater)


class ScriptUpdateTests(unittest.TestCase):
    def setUp(self):
        work = self.enterContext(tempfile.TemporaryDirectory(prefix="skyshield-update-test-"))
        self.root = Path(work) / "deployment"
        self.remote = Path(work) / "publisher"
        self.remote.mkdir()
        self.git(self.remote, "init", "--quiet", "--initial-branch=main")
        self.git(self.remote, "config", "user.email", "fixture@example.com")
        self.git(self.remote, "config", "user.name", "Test Publisher")
        (self.remote / ".gitignore").write_text("/.local/\n", encoding="utf-8")
        self.before = self.commit("old")
        self.released = self.commit("released")
        self.git(self.remote, "tag", "-a", "v2026.11.19", "-m", "Official release")
        self.ahead = self.commit("unreleased")
        self.git(Path(work), "clone", "--quiet", str(self.remote), str(self.root))
        self.git(self.root, "checkout", "--quiet", "--detach", self.before)
        self.enterContext(patch.object(updater, "ROOT", self.root))
        self.enterContext(patch.object(updater, "REPOSITORY", str(self.remote)))
        self.enterContext(patch.object(updater, "import_deployment", return_value=SimpleNamespace(
            settings=lambda _root: {"SKYSHIELD_INHERIT_HOST_NETWORK": "0"}, PROXY_KEYS=(),
        )))
        self.enterContext(patch.object(release_source, "fetch", return_value=json.dumps({
            "tag_name": "v2026.11.19", "draft": False, "prerelease": False, "target_commitish": "main",
        }).encode()))
        self.running = self.enterContext(patch.object(updater, "current_revision", return_value=self.before))
        self.enterContext(contextlib.redirect_stdout(io.StringIO()))

    def git(self, cwd, *args):
        return subprocess.check_output(["git", "-C", cwd, *args], text=True, stderr=subprocess.PIPE).strip()

    def commit(self, version):
        (self.remote / "version").write_text(version, encoding="utf-8")
        self.git(self.remote, "add", ".")
        self.git(self.remote, "commit", "--quiet", "-m", version)
        return self.git(self.remote, "rev-parse", "HEAD")

    def use_mode(self, mode):
        self.enterContext(patch.object(updater, "MODE", mode))
        self.enterContext(patch.object(updater, "STATE", updater.state_path(mode)))

    def test_local_upgrade_uses_published_tag_and_hands_off_after_backup(self):
        self.use_mode("local")
        events = []
        real_command = updater.command

        def command(args, **settings):
            if args[0] in ("pnpm", "bash"):
                self.assertEqual(self.git(self.root, "rev-parse", "HEAD"), self.released)
                self.assertEqual(settings["env"]["SKYSHIELD_UPDATE_REVISION"], self.released)
                self.assertEqual(settings["env"]["SKYSHIELD_UPDATER_CHILD"], "1")
                events.append(args[0])
                return ""
            return real_command(args, **settings)

        with (patch.object(updater, "preflight"), patch.object(updater, "ensure_idle"),
              patch.object(updater, "stop_applications", side_effect=lambda _log: events.append("stop")),
              patch.object(updater, "backup_database", side_effect=lambda _folder, _log: events.append("backup")),
              patch.object(updater, "verify_runtime", side_effect=lambda _target, _log: events.append("verify")),
              patch.object(updater, "command", side_effect=command), patch.object(updater, "published_images") as images):
            updater.upgrade(True)
            images.assert_not_called()
        self.assertEqual(events, ["stop", "backup", "pnpm", "bash", "verify"])
        self.assertEqual(self.git(self.root, "rev-parse", "HEAD"), self.released)
        self.assertEqual(updater.read_json(updater.STATE / "installed.json")["revision"], self.released)
        self.assertEqual(updater.read_json(updater.STATE / "operation.json")["status"], "succeeded")
        self.assertFalse((updater.STATE / "socket/maintenance").exists())

    def test_docker_upgrade_pins_all_images_before_starting(self):
        self.use_mode("docker")
        images = {name: f"docker.io/solder110/skyshield-{name}@sha256:{index:064x}"
                  for index, name in enumerate(updater.COMPONENTS, 1)}
        events = []
        real_command = updater.command

        def command(args, **settings):
            if args[:2] == ["docker", "pull"]:
                events.append(("pull", args[-1]))
                return ""
            if args[:3] == ["docker", "image", "inspect"]:
                return json.dumps([{"Os": "linux", "Architecture": "amd64", "Config": {
                    "Labels": {"org.opencontainers.image.revision": self.released},
                }}])
            if args[0] == "bash":
                self.assertEqual(self.git(self.root, "rev-parse", "HEAD"), self.released)
                self.assertEqual(settings["env"]["SKYSHIELD_IMAGE_TAG"], "sha-" + self.released[:12])
                overlay = updater.read_json(updater.STATE / "compose.release.json")["services"]
                for name, image in images.items():
                    self.assertEqual(overlay[name]["image"], image)
                self.assertEqual(overlay["migrate"]["image"], images["bootstrap"])
                events.append("start")
                return ""
            return real_command(args, **settings)

        with (patch.object(updater, "preflight"), patch.object(updater, "ensure_idle"),
              patch.object(updater, "published_images", return_value=images),
              patch.object(updater, "stop_applications", side_effect=lambda _log: events.append("stop")),
              patch.object(updater, "backup_database", side_effect=lambda _folder, _log: events.append("backup")),
              patch.object(updater, "verify_runtime"), patch.object(updater, "command", side_effect=command)):
            updater.upgrade(True)
        self.assertEqual(events, [("pull", image) for image in images.values()] + ["stop", "backup", "start"])

    def test_missing_docker_images_leave_source_and_services_available(self):
        self.use_mode("docker")
        with (patch.object(updater, "preflight"),
              patch.object(updater, "published_images", side_effect=RuntimeError("镜像未发布")),
              patch.object(updater, "stop_applications") as stop):
            with self.assertRaisesRegex(RuntimeError, "未进入停机阶段"):
                updater.upgrade(True)
            stop.assert_not_called()
        self.assertEqual(self.git(self.root, "rev-parse", "HEAD"), self.before)
        self.assertEqual(updater.read_json(updater.STATE / "operation.json")["status"], "failed")

    def test_running_version_newer_than_release_is_not_downgraded(self):
        self.use_mode("local")
        self.running.return_value = self.ahead
        with patch.object(updater, "install") as install:
            with self.assertRaisesRegex(RuntimeError, "已领先发布版本"):
                updater.upgrade(True)
            install.assert_not_called()

    def test_post_stop_failure_requires_explicit_recovery(self):
        self.use_mode("local")
        with (patch.object(updater, "preflight"), patch.object(updater, "ensure_idle"),
              patch.object(updater, "stop_applications"),
              patch.object(updater, "backup_database", side_effect=RuntimeError("备份失败"))):
            with self.assertRaisesRegex(RuntimeError, "按文档恢复"):
                updater.upgrade(True)
        self.assertEqual(updater.read_json(updater.STATE / "operation.json")["status"], "needs_recovery")
        self.assertFalse((updater.STATE / "socket/maintenance").exists())
        with self.assertRaisesRegex(RuntimeError, "上一次升级需要恢复"):
            updater.upgrade(True)
        updater.acknowledge_recovery()
        self.assertEqual(updater.read_json(updater.STATE / "operation.json")["status"], "failed")

    def test_first_local_version_keeps_recovery_in_its_original_directory(self):
        self.use_mode("local")
        legacy = updater.state_path("docker")
        (legacy / "socket").mkdir(parents=True)
        updater.private_json(legacy / "daemon.json", {"mode": "local"})
        updater.private_json(legacy / "operation.json", {"status": "needs_recovery", "id": "previous-local"})
        marker = legacy / "socket/maintenance"
        marker.touch()
        with self.assertRaisesRegex(RuntimeError, "上一次升级需要恢复"):
            updater.upgrade(True)
        updater.acknowledge_recovery()
        self.assertEqual(updater.read_json(legacy / "operation.json")["status"], "failed")
        self.assertFalse(marker.exists())
        self.assertFalse((updater.STATE / "operation.json").exists())


if __name__ == "__main__":
    unittest.main()
