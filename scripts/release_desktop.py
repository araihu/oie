#!/usr/bin/env python3
"""Assemble and verify the assets for a tagged Oiê desktop release."""

import argparse
import hashlib
import json
from pathlib import Path
import re
import shutil
import tarfile


# Release tags support SemVer prereleases. Build metadata has no precedence
# and is omitted from package versions, so it is not part of a release tag.
TAG = re.compile(r"v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(?:-([0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*))?")


def version_key(tag):
    match = TAG.fullmatch(tag)
    if not match:
        raise ValueError(f"invalid release tag: {tag}")
    major, minor, patch, prerelease = match.groups()
    parts = []
    for item in prerelease.split(".") if prerelease else ():
        if item.isdigit():
            if len(item) > 1 and item.startswith("0"):
                raise ValueError(f"invalid numeric prerelease identifier in {tag}")
            parts.append((0, int(item)))
        else:
            parts.append((1, item))
    return int(major), int(minor), int(patch), not prerelease, parts


def digest(path):
    checksum = hashlib.sha256()
    with path.open("rb") as stream:
        while chunk := stream.read(1024 * 1024):
            checksum.update(chunk)
    return checksum.hexdigest()


def write_checksums(directory):
    files = sorted(path for path in directory.iterdir() if path.is_file() and path.name != "SHA256SUMS")
    (directory / "SHA256SUMS").write_text("".join(f"{digest(path)}  {path.name}\n" for path in files))


def assemble(args):
    args.output.mkdir()
    artifacts = []
    for directory, os_name, arch, metadata in (
        (args.linux, "linux", "amd64", "share/doc/oie/build-info.json"),
        (args.macos, "darwin", "arm64", "Oie.app/Contents/Resources/build-info.json"),
    ):
        prefix = f"oie_{args.version}_{os_name}_{arch}"
        for name in (f"{prefix}.tar.gz", f"{prefix}_source.tar.gz"):
            asset = directory / name
            checksum = digest(asset)
            if (directory / f"{name}.sha256").read_text().split() != [checksum, name]:
                raise ValueError(f"checksum mismatch for {name}")
            shutil.copyfile(asset, args.output / name)
            artifacts.append({"name": name, "sha256": checksum})
        with tarfile.open(directory / f"{prefix}.tar.gz") as archive:
            with archive.extractfile(metadata) as stream:
                build = json.load(stream)
        if (build["version"], build["sourceRevision"], build["os"], build["arch"]) != (
            args.version, args.revision, os_name, arch
        ):
            raise ValueError(f"unexpected provenance for {prefix}")
        artifacts[-2]["build"] = build

    identity = json.loads((args.linux / "source-receipt.json").read_text())
    if (identity["version"], identity["revision"], identity["packagingRevision"]) != (
        args.version, args.revision, args.revision
    ):
        raise ValueError("unexpected Linux release identity")
    report = json.loads(args.linux_report.read_text())
    if (report["status"], report["version"], report["revision"]) != ("passed", args.version, args.revision):
        raise ValueError("Linux archive runtime verification is missing")
    report["environment"] = "Ubuntu 24.04 x86_64 runtime container with Xvfb"
    installer = Path(__file__).with_name("install.sh")
    text = installer.read_text()
    text, count = re.subn(r"(?m)^default_version=.*$", f"default_version={args.version}", text)
    if count != 1:
        raise ValueError("expected one default_version in install.sh")
    (args.output / "install.sh").write_text(text)
    (args.output / "install.sh").chmod(0o755)
    receipt = {
        "applicationSourceTag": args.version,
        "applicationSourceRevision": args.revision,
        "packagingRevision": args.revision,
        "sourceArchiveSHA256": identity["sourceArchiveSHA256"],
        "packagingScriptSHA256": digest(installer.with_name("package-desktop.sh")),
        "installerSHA256": digest(args.output / "install.sh"),
        "artifacts": artifacts,
        "verification": [report],
    }
    (args.output / "packaging-receipt.json").write_text(json.dumps(receipt, indent=2) + "\n")
    write_checksums(args.output)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    commands = parser.add_subparsers(dest="command", required=True)
    validate = commands.add_parser("validate")
    validate.add_argument("version")
    checksums = commands.add_parser("checksums")
    checksums.add_argument("directory", type=Path)
    build = commands.add_parser("assemble")
    build.add_argument("--version", required=True)
    build.add_argument("--revision", required=True)
    for name in ("linux", "macos", "linux-report", "output"):
        build.add_argument(f"--{name}", type=Path, required=True)
    args = parser.parse_args()
    try:
        if args.command == "checksums":
            write_checksums(args.directory)
        else:
            key = version_key(args.version)
            if args.command == "validate":
                print("false" if key[3] else "true")
            else:
                assemble(args)
    except (OSError, ValueError, KeyError, tarfile.TarError) as error:
        parser.exit(1, f"release-desktop: {error}\n")


if __name__ == "__main__":
    main()
