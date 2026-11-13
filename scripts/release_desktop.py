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
TARGETS = (
    ("linux", "amd64", "share/doc/oie"),
    ("darwin", "arm64", "Oie.app/Contents/Resources"),
)

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
    verification = []
    for (os_name, arch, docs), directory, runtime_report in zip(
        TARGETS, (args.linux, args.macos), (args.linux_report, args.macos_report), strict=True
    ):
        prefix = f"oie_{args.version}_{os_name}_{arch}"
        binary, source = (f"{prefix}{suffix}" for suffix in (".tar.gz", "_source.tar.gz"))
        for name in (binary, source):
            asset = directory / name
            checksum = digest(asset)
            if (directory / f"{name}.sha256").read_text().split() != [checksum, name]:
                raise ValueError(f"checksum mismatch for {name}")
            shutil.copyfile(asset, args.output / name)
            artifacts.append({"name": name, "sha256": checksum})
        with tarfile.open(directory / binary) as archive:
            with archive.extractfile(f"{docs}/build-info.json") as stream:
                build = json.load(stream)
            with archive.extractfile(f"{docs}/SOURCE.txt") as stream:
                source_url = f"https://github.com/araihu/oie/releases/download/{args.version}/{source}"
                if f"Corresponding source: {source_url}" not in stream.read().decode().splitlines():
                    raise ValueError(f"incorrect corresponding source link in {binary}")
        if (build["version"], build["sourceRevision"], build["os"], build["arch"], build["correspondingSourceSHA256"]) != (
            args.version, args.revision, os_name, arch, artifacts[-1]["sha256"]
        ):
            raise ValueError(f"unexpected provenance for {prefix}")
        artifacts[-2]["build"] = build

        for path, asset, field in (
            (runtime_report, artifacts[-2], "binaryArchiveSHA256"),
            (directory / "source-verification.json", artifacts[-1], "sourceArchiveSHA256"),
        ):
            report = json.loads(path.read_text())
            if (report["status"], report["version"], report["revision"], report["platform"], report[field]) != (
                "passed", args.version, args.revision, f"{os_name}/{arch}", asset["sha256"]
            ):
                raise ValueError(f"verification does not match {asset['name']}")
            verification.append(report)

    identity = json.loads((args.linux / "source-receipt.json").read_text())
    if (identity["version"], identity["revision"], identity["packagingRevision"]) != (
        args.version, args.revision, args.revision
    ):
        raise ValueError("unexpected Linux release identity")
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
        "verification": verification,
    }
    (args.output / "packaging-receipt.json").write_text(json.dumps(receipt, indent=2) + "\n")
    write_checksums(args.output)


def verify_assets(version, revision, directory, remote):
    sums = directory / "SHA256SUMS"
    expected = {}
    for line in sums.read_text().splitlines():
        checksum, name = line.split("  ", 1)
        if not re.fullmatch(r"[0-9a-f]{64}", checksum) or name in expected:
            raise ValueError(f"invalid release checksum entry for {name}")
        expected[name] = checksum
    if sums.name in expected:
        raise ValueError("SHA256SUMS cannot list itself")
    expected[sums.name] = digest(sums)
    files = {path.name: path for path in directory.iterdir() if path.is_file()}
    if files.keys() != expected.keys():
        raise ValueError("release asset names differ from SHA256SUMS")
    receipt = json.loads(files["packaging-receipt.json"].read_text())
    if (receipt["applicationSourceTag"], receipt["applicationSourceRevision"], receipt["packagingRevision"]) != (
        version, revision, revision
    ):
        raise ValueError("unexpected release receipt identity")
    required = {
        f"oie_{version}_{os_name}_{arch}{suffix}"
        for os_name, arch, _ in TARGETS
        for suffix in (".tar.gz", "_source.tar.gz")
    }
    recorded = {asset["name"]: asset["sha256"] for asset in receipt["artifacts"]}
    if required != recorded.keys() or any(expected.get(name) != checksum for name, checksum in recorded.items()):
        raise ValueError("release assets are missing the verified binary/source pairs")
    if remote:
        assets = json.loads(remote.read_text())
        uploaded = {asset["name"]: asset["digest"] for asset in assets if asset["state"] == "uploaded"}
        if len(assets) != len(uploaded) or uploaded != {name: f"sha256:{checksum}" for name, checksum in expected.items()}:
            raise ValueError("GitHub release asset names or digests differ from the verified assets")
    else:
        for name, path in files.items():
            if digest(path) != expected[name]:
                raise ValueError(f"release checksum mismatch for {name}")


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    commands = parser.add_subparsers(dest="command", required=True)
    validate = commands.add_parser("validate")
    validate.add_argument("version")
    checksums = commands.add_parser("checksums")
    checksums.add_argument("directory", type=Path)
    verify = commands.add_parser("verify-assets")
    verify.add_argument("version")
    verify.add_argument("revision")
    verify.add_argument("directory", type=Path)
    verify.add_argument("--remote", type=Path)
    build = commands.add_parser("assemble")
    build.add_argument("--version", required=True)
    build.add_argument("--revision", required=True)
    for name in ("linux", "macos", "linux-report", "macos-report", "output"):
        build.add_argument(f"--{name}", type=Path, required=True)
    args = parser.parse_args()
    try:
        if args.command == "checksums":
            write_checksums(args.directory)
        else:
            key = version_key(args.version)
            if args.command == "validate":
                print("false" if key[3] else "true")
            elif args.command == "verify-assets":
                verify_assets(args.version, args.revision, args.directory, args.remote)
            else:
                assemble(args)
    except (OSError, ValueError, KeyError, tarfile.TarError) as error:
        parser.exit(1, f"release-desktop: {error}\n")


if __name__ == "__main__":
    main()
