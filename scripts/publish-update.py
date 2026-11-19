#!/usr/bin/env python3
"""Generate the old update descriptor for deployments with the web updater."""

import argparse
import datetime
import json
from pathlib import Path
import re
import subprocess

from release_source import published_images

ROOT = Path(__file__).resolve().parent.parent


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--revision", required=True)
    parser.add_argument("--version", required=True)
    parser.add_argument("--notes", type=Path, required=True)
    args = parser.parse_args()
    revision = subprocess.check_output(
        ["git", "rev-parse", "--verify", "--end-of-options", args.revision + "^{commit}"], cwd=ROOT, text=True,
    ).strip()
    notes = args.notes.read_text(encoding="utf-8")
    if not re.fullmatch(r"[A-Za-z0-9][A-Za-z0-9.+-]{0,63}", args.version) or len(notes) > 16000:
        parser.error("Invalid legacy release version or notes")
    release = {
        "schemaVersion": 1,
        "minimumUpdaterVersion": 1,
        "version": args.version,
        "revision": revision,
        "publishedAt": datetime.datetime.now(datetime.timezone.utc).isoformat(),
        "notes": notes,
        "images": published_images(revision),
    }
    destination = ROOT / "updates/stable.json"
    destination.parent.mkdir(exist_ok=True)
    destination.write_text(json.dumps(release, ensure_ascii=False, indent=2) + "\n", encoding="utf-8")
    print("Prepared updates/stable.json for legacy update clients.")


if __name__ == "__main__":
    main()
