#!/usr/bin/env python3
"""Validate an actual macOS bundle install with disposable application data."""
import argparse
import hashlib
import json
import os
from pathlib import Path
import plistlib
import shutil
import signal
import subprocess
import tempfile
import time

from smoke_release import identity, require


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--archive", required=True, type=Path)
    parser.add_argument("--installer", required=True, type=Path)
    parser.add_argument("--version", required=True)
    parser.add_argument("--revision", required=True)
    parser.add_argument("--artifacts", required=True, type=Path)
    args = parser.parse_args()
    args.artifacts.mkdir(parents=True, exist_ok=True)
    report = {"platform": "darwin/arm64", "version": args.version, "revision": args.revision, "checks": [], "status": "failed"}
    owned_pid = None
    try:
        require(os.uname().sysname == "Darwin" and os.uname().machine == "arm64", "run on native Apple Silicon")
        with tempfile.TemporaryDirectory(prefix="oie-bundle-check-") as task:
            root = Path(task)
            prefix = root / "Apps with spaces"
            assets = root / "assets"
            commands = root / "commands"
            assets.mkdir()
            commands.mkdir()
            shutil.copy2(args.archive, assets / args.archive.name)
            checksum = hashlib.sha256(args.archive.read_bytes()).hexdigest()
            report["binaryArchiveSHA256"] = checksum
            (assets / "SHA256SUMS").write_text(f"{checksum}  {args.archive.name}\n")
            curl = commands / "curl"
            curl.write_text('''#!/usr/bin/env python3
import os
from pathlib import Path
import shutil
import sys
arguments = sys.argv[1:]
url = arguments[-1]
base = "https://github.com/araihu/oie/releases/download/" + os.environ["OIE_TEST_VERSION"] + "/"
allowed = [base + "SHA256SUMS", base + os.environ["OIE_TEST_ARCHIVE"]]
if url not in allowed:
    raise SystemExit("unexpected download: " + url)
shutil.copyfile(Path(os.environ["OIE_TEST_ASSETS"]) / url.removeprefix(base), arguments[arguments.index("--output") + 1])
''')
            curl.chmod(0o755)
            env = os.environ.copy()
            env.update(PATH=str(commands) + ":" + env["PATH"], OIE_TEST_ASSETS=str(assets),
                       OIE_TEST_VERSION=args.version, OIE_TEST_ARCHIVE=args.archive.name)
            command = ["sh", str(args.installer), "--version", args.version, "--prefix", str(prefix)]
            installed = subprocess.run(command, env=env, capture_output=True, text=True, check=False)
            (args.artifacts / "install.log").write_text(installed.stdout + installed.stderr)
            require(installed.returncode == 0, "Mac archive installation failed: " + installed.stderr)
            bundle = prefix / "Oie.app"
            plist = plistlib.loads((bundle / "Contents/Info.plist").read_bytes())
            require(plist["CFBundleIdentifier"] == "org.araihu.oie", "unexpected bundle identifier")
            require(plist["OieVersion"] == args.version and plist["OieSourceRevision"] == args.revision, "incorrect bundle source/version")
            subprocess.run(["codesign", "--verify", "--deep", "--strict", str(bundle)], check=True)
            report["checks"].append("verified and installed ad-hoc signed bundle with matching version/source into a path containing spaces")
            # NSRunningApplication is public AppKit metadata. This does not use
            # Accessibility automation, read other app content or grant permissions.
            swift = root / "probe.swift"
            swift.write_text('''import AppKit
import Foundation
let target = URL(fileURLWithPath: CommandLine.arguments[1]).resolvingSymlinksInPath().standardizedFileURL.path
let apps = NSWorkspace.shared.runningApplications.filter { $0.bundleURL?.resolvingSymlinksInPath().standardizedFileURL.path == target }
let result = apps.map { ["pid": Int($0.processIdentifier), "finished": $0.isFinishedLaunching, "name": $0.localizedName ?? ""] as [String: Any] }
let data = try JSONSerialization.data(withJSONObject: result)
print(String(data: data, encoding: .utf8)!)
''')
            probe = root / "probe"
            subprocess.run(["xcrun", "swiftc", str(swift), "-o", str(probe)], check=True)
            profile = root / "fresh-profile"
            subprocess.run(["open", "-n", str(bundle), "--args", "--data-dir", str(profile)], check=True)
            deadline = time.monotonic() + 45
            while time.monotonic() < deadline:
                apps = json.loads(subprocess.check_output([str(probe), str(bundle)], text=True))
                if apps:
                    owned_pid = apps[0]["pid"]
                    if apps[0]["finished"] and (profile / "oie.sqlite").exists():
                        break
                time.sleep(0.25)
            else:
                raise AssertionError("LaunchServices did not launch the installed bundle and initialize its profile")
            first_identity = identity(profile / "oie.sqlite")
            report["checks"].append("LaunchServices opened the installed application and initialized a fresh embedded SQLite engine")
            os.kill(owned_pid, signal.SIGTERM)
            deadline = time.monotonic() + 20
            while time.monotonic() < deadline:
                if not json.loads(subprocess.check_output([str(probe), str(bundle)], text=True)):
                    owned_pid = None
                    break
                time.sleep(0.25)
            require(owned_pid is None, "owned application did not stop after SIGTERM")
            marker = profile / "packaging-preserve.txt"
            marker.write_text("retained data\n")
            updated = subprocess.run(command, env=env, capture_output=True, text=True, check=False)
            (args.artifacts / "update.log").write_text(updated.stdout + updated.stderr)
            require(updated.returncode == 0, "Mac bundle update failed: " + updated.stderr)
            require(marker.read_text() == "retained data\n" and identity(profile / "oie.sqlite") == first_identity, "update altered retained profile")
            subprocess.run(["codesign", "--verify", "--deep", "--strict", str(bundle)], check=True)
            report["checks"].append("shutdown and repeated installation preserve SQLite identity, retained files and bundle signature")
            report["status"] = "passed"
    finally:
        if owned_pid is not None:
            try:
                os.kill(owned_pid, signal.SIGTERM)
            except ProcessLookupError:
                pass
        (args.artifacts / "report.json").write_text(json.dumps(report, indent=2) + "\n")


if __name__ == "__main__":
    main()
