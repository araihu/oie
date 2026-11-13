#!/usr/bin/env python3
"""Verify the corresponding source and rebuild the commands without module downloads."""

import argparse
import hashlib
import json
import os
from pathlib import Path
import subprocess
import tarfile
from tempfile import TemporaryDirectory


def digest(path):
    with path.open("rb") as stream:
        return hashlib.file_digest(stream, "sha256").hexdigest()


def verify(archive, version, revision, report):
    with TemporaryDirectory(prefix="oie-source-check-") as temporary:
        stage = Path(temporary)
        with tarfile.open(archive, "r|gz") as package:
            package.extractall(stage, filter="data")
        root = stage / f"oie_{version}_source"
        if json.loads((root / "source-info.json").read_text()) != {"version": version, "revision": revision}:
            raise ValueError("source archive version or revision differs from the build")
        tooling = root / "release-tooling"
        manifest = json.loads((tooling / "packaging/browser-source.json").read_text())
        for path, expected in manifest["inputs"].items():
            if digest(root / "oie" / path) != expected:
                raise ValueError(f"update packaging/browser-source.json for changed asset input {path}")
        for item in manifest["archives"]:
            if digest(root / "browser-sources" / item["name"]) != item["sha256"]:
                raise ValueError(f"browser source checksum mismatch for {item['name']}")

        # A fresh module/build cache and disabled module resolution prove that
        # the extracted vendor trees supply the Go build on this platform.
        env = dict(os.environ, GOENV="off", GOWORK="off", GOFLAGS="", GOTOOLCHAIN="local",
                   GOPROXY="off", GOSUMDB="off", GOMODCACHE=str(stage / "modules"),
                   GOCACHE=str(stage / "build-cache"), CGO_ENABLED="1")
        goos, goarch = subprocess.check_output(["go", "env", "GOHOSTOS", "GOHOSTARCH"], env=env, text=True).split()
        env.update(GOOS=goos, GOARCH=goarch)
        if goos == "darwin":
            env.update(MACOSX_DEPLOYMENT_TARGET="13.0", CGO_CFLAGS="-O2 -g -mmacosx-version-min=13.0",
                       CGO_LDFLAGS="-O2 -g -mmacosx-version-min=13.0")
        commands = stage / "bin"
        commands.mkdir()
        build = ["build", "-mod=vendor", "-trimpath", "-buildvcs=false", "-o", str(commands) + "/"]
        subprocess.run(["go", "-C", str(root / "oie"), *build, "-tags=production", "./cmd/oie", "./cmd/oie-desktop"],
                       env=env, check=True)
        subprocess.run(["go", "-C", str(root / "oie/cli"), *build, "./cmd/oiectl"], env=env, check=True)
        if {path.name for path in commands.iterdir()} != {"oie", "oie-desktop", "oiectl"}:
            raise ValueError("offline build did not produce all three commands")
    report.write_text(json.dumps({
        "status": "passed", "platform": f"{goos}/{goarch}", "version": version,
        "revision": revision, "sourceArchiveSHA256": digest(archive),
        "commands": ["oie", "oie-desktop", "oiectl"],
    }, indent=2) + "\n")


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("archive", type=Path)
    parser.add_argument("version")
    parser.add_argument("revision")
    parser.add_argument("report", type=Path)
    args = parser.parse_args()
    verify(args.archive, args.version, args.revision, args.report)


if __name__ == "__main__":
    main()
