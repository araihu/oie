"""Copy current tracked/untracked source without runtime data or Git internals."""
import os
from pathlib import Path
import shutil
import subprocess
import sys


def excluded(path):
    parts = Path(path).parts
    return (not parts or parts[0] in {".git", "data", "bin", "dist"}
            or any(p in {"node_modules", "__pycache__", "playwright-report", "test-results", ".env", ".env.local"} for p in parts)
            or any(p.startswith(".codegen-check.") for p in parts)
            or parts[:3] in {("tests", "browser", "artifacts"), ("tools", "ci", "sdk")})


def materialize(root, destination):
    root, destination = Path(root).resolve(), Path(destination).resolve()
    destination.mkdir(parents=True, exist_ok=True)
    names = subprocess.check_output(["git", "ls-files", "--cached", "--others", "--exclude-standard", "-z"], cwd=root)
    for raw in set(names.split(b"\0")):
        if not raw:
            continue
        name = os.fsdecode(raw)
        if excluded(name):
            continue
        source, target = root / name, destination / name
        if not source.exists() and not source.is_symlink():
            continue
        target.parent.mkdir(parents=True, exist_ok=True)
        if source.is_symlink():
            target.symlink_to(os.readlink(source))
        elif source.is_file():
            shutil.copy2(source, target)


if __name__ == "__main__":
    materialize(sys.argv[1], sys.argv[2])
