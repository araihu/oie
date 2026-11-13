#!/usr/bin/env python3
"""Generate package-manager artifacts from verified Oiê desktop release assets."""

import argparse
from datetime import datetime
import json
import os
from pathlib import Path
import shutil
import subprocess
import tarfile
import tempfile
from release_desktop import digest, version_key


ROOT = Path(__file__).resolve().parent.parent
APP_ID = "org.araihu.oie"
FLATPAK_ID = "com.araihu.oie"


def verify_archive(assets, name, sums):
    archive = assets / name
    checksum = sums.get(name, "")
    if len(checksum) != 64 or any(char not in "0123456789abcdef" for char in checksum):
        raise ValueError(f"missing SHA-256 in SHA256SUMS for {name}")
    if digest(archive) != checksum:
        raise ValueError(f"SHA-256 mismatch for {name}")
    return archive, checksum


def release_checksums(assets):
    checksums = {}
    for line in (assets / "SHA256SUMS").read_text().splitlines():
        if not line.strip():
            continue
        digest, name = line.split(maxsplit=1)
        if name in checksums:
            raise ValueError(f"duplicate checksum for {name}")
        checksums[name] = digest
    return checksums


def render(source, destination, values):
    content = source.read_text(encoding="utf-8")
    for name, value in values.items():
        content = content.replace(f"@{name}@", value)
    destination.parent.mkdir(parents=True, exist_ok=True)
    destination.write_text(content, encoding="utf-8")


def linux_packages(stage, linux_archive, release, prerelease, date, nfpm, formats, values):
    payload = stage / "payload"
    payload.mkdir()
    with tarfile.open(linux_archive) as archive:
        # Only regular files and directories may be staged, including on
        # Python versions without tarfile extraction filters.
        for member in archive:
            destination = payload / member.name
            if not destination.resolve().is_relative_to(payload):
                raise ValueError(f"archive path outside the destination: {member.name}")
            if member.isdir():
                destination.mkdir(parents=True, exist_ok=True)
                destination.chmod(0o755)
            elif member.isfile():
                destination.parent.mkdir(parents=True, exist_ok=True)
                with archive.extractfile(member) as source, destination.open("xb") as target:
                    shutil.copyfileobj(source, target)
                destination.chmod(0o755 if destination == payload / "bin/oie-desktop" else 0o644)
            else:
                raise ValueError(f"unexpected file type in Linux release archive: {member.name}")
    desktop = payload / f"share/applications/{APP_ID}.desktop"
    desktop.write_text(desktop.read_text(encoding="utf-8").replace("@OIE_EXEC@", "/usr/bin/oie-desktop"),
                       encoding="utf-8")
    metainfo_dir = payload / "share/metainfo"
    render(ROOT / "packaging/linux/oie.metainfo.xml.in", metainfo_dir / f"{APP_ID}.metainfo.xml",
           dict(values, APP_ID=APP_ID))
    (metainfo_dir / f"{APP_ID}.metainfo.xml").chmod(0o644)

    native = stage / "native"
    native.mkdir()
    env = os.environ.copy()
    epoch = int(date.timestamp())
    env["SOURCE_DATE_EPOCH"] = str(epoch)
    # nFPM keeps source file times for tree entries. Normalize those too so
    # regenerating the same package version keeps its checksum.
    for path in payload.rglob("*"):
        os.utime(path, (epoch, epoch))
    config = ROOT / "packaging/linux/nfpm.yaml"
    for packager in formats:
        # nFPM's Arch metadata omits its prerelease field unless there is
        # an epoch. Use a native Arch pkgver so stable sorts after the RC.
        version = release
        suffix = prerelease or ""
        if packager == "archlinux" and suffix:
            arch_suffix = suffix.replace("-", ".").lstrip(".")
            if not suffix[0].isalpha():
                arch_suffix = f"pre.{arch_suffix}"
            version += arch_suffix
            suffix = ""
        env.update(OIE_PACKAGE_VERSION=version, OIE_PACKAGE_PRERELEASE=suffix)
        subprocess.run([nfpm, "package", "--config", str(config), "--packager", packager,
                        "--target", str(native)], cwd=stage, env=env, check=True)


def generate(args):
    version_key(args.version)
    release, _, prerelease = args.version[1:].partition("-")
    assets = args.assets.resolve()
    output = args.output.resolve()
    if output.exists():
        raise ValueError(f"output already exists: {output}")

    checksums = release_checksums(assets)
    linux_name = f"oie_{args.version}_linux_amd64"
    macos_name = f"oie_{args.version}_darwin_arm64"
    linux_archive, linux_sha = verify_archive(assets, f"{linux_name}.tar.gz", checksums)
    if args.deb_only:
        verify_archive(assets, f"{linux_name}_source.tar.gz", checksums)
        macos_sha = ""
    else:
        _, macos_sha = verify_archive(assets, f"{macos_name}.tar.gz", checksums)
    # Newer releases publish the corresponding source beside each binary.
    for name in (linux_name, macos_name):
        source_name = f"{name}_source.tar.gz"
        if not args.deb_only and source_name in checksums:
            verify_archive(assets, source_name, checksums)
    with tarfile.open(linux_archive) as archive:
        with archive.extractfile("share/doc/oie/build-info.json") as info:
            build = json.load(info)
    if (build["version"], build["os"], build["arch"]) != (args.version, "linux", "amd64"):
        raise ValueError("unexpected version or target in Linux archive build metadata")
    date = datetime.fromisoformat(build["builtAt"].replace("Z", "+00:00"))
    values = {
        "VERSION": args.version[1:],
        "DATE": date.date().isoformat(),
        "RELEASE_TYPE": "development" if prerelease else "stable",
        "MACOS_SHA256": macos_sha,
        "LINUX_SHA256": linux_sha,
    }

    output.parent.mkdir(parents=True, exist_ok=True)
    with tempfile.TemporaryDirectory(prefix=".oie-distribution-", dir=output.parent) as temporary:
        stage = Path(temporary) / "distribution"
        if not args.deb_only:
            for source in sorted((ROOT / "packaging/homebrew").glob("*/*.rb.in")):
                render(source, stage / "homebrew" / source.parent.name / source.stem, values)
        if not args.deb_only:
            render(ROOT / f"packaging/flatpak/{FLATPAK_ID}.json.in", stage / f"flatpak/{FLATPAK_ID}.json", values)
            render(ROOT / "packaging/linux/oie.metainfo.xml.in", stage / f"flatpak/{FLATPAK_ID}.metainfo.xml",
                   dict(values, APP_ID=FLATPAK_ID))
        if not args.recipes_only:
            nfpm = shutil.which(args.nfpm)
            if not nfpm:
                raise ValueError(f"nFPM executable not found: {args.nfpm}")
            formats = ("deb",) if args.deb_only else ("deb", "rpm", "archlinux")
            linux_packages(stage, linux_archive, release, prerelease, date, nfpm, formats, values)
            shutil.rmtree(stage / "payload")
        stage.rename(output)
    print(output)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--version", required=True, help="release tag")
    parser.add_argument("--assets", required=True, type=Path, help="directory with both archives and SHA256SUMS")
    parser.add_argument("--output", required=True, type=Path, help="new directory for generated packages and recipes")
    parser.add_argument("--nfpm", default="nfpm", help="path to the nFPM executable")
    mode = parser.add_mutually_exclusive_group()
    mode.add_argument("--recipes-only", action="store_true", help="skip building the native Linux packages")
    mode.add_argument("--deb-only", action="store_true", help="build a DEB from a Linux release with corresponding source")
    args = parser.parse_args()
    try:
        generate(args)
    except (OSError, ValueError, KeyError, tarfile.TarError, subprocess.CalledProcessError) as error:
        parser.exit(1, f"package-distribution: {error}\n")


if __name__ == "__main__":
    main()
