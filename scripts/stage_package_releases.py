#!/usr/bin/env python3
"""Stage recent signed-index inputs from the immutable desktop release assets."""

import argparse
import json
from pathlib import Path
import shutil
import subprocess
from tempfile import TemporaryDirectory

from release_desktop import digest, version_key


REPOSITORY = "araihu/oie"
POOL = "apt/pool/main/o/oie"


def gh(*args):
    return subprocess.check_output(["gh", *args], text=True)


def download_release(tag, assets, directory):
    patterns = [arg for name in (*assets, "SHA256SUMS") for arg in ("--pattern", name)]
    gh("release", "download", tag, "--repo", REPOSITORY, "--dir", str(directory), *patterns)
    entries = [line.split() for line in (directory / "SHA256SUMS").read_text().splitlines()]
    for asset in assets:
        checksums = [entry[0] for entry in entries if len(entry) == 2 and entry[1] == asset]
        if checksums != [digest(directory / asset)]:
            raise ValueError(f"release checksum mismatch for {asset}")


def stage_deb(tag, asset, version, directory):
    with TemporaryDirectory() as temporary:
        download = Path(temporary)
        download_release(tag, (asset,), download)
        package_version = subprocess.check_output(["dpkg-deb", "-f", str(download / asset), "Version"],
                                                  text=True).strip()
        if package_version != version:
            raise ValueError(f"unexpected version in {asset}")
        shutil.copyfile(download / asset, directory / f"oie_{version}_amd64.deb")


def published_releases():
    pages = json.loads(gh("api", "--paginate", "--slurp", f"repos/{REPOSITORY}/releases?per_page=100"))
    for page in pages:
        for release in page:
            if release["draft"]:
                continue
            tag = release["tag_name"]
            try:
                precedence = version_key(tag)
            except ValueError:
                continue
            yield precedence, tag, {asset["name"] for asset in release["assets"]}


def apt_packages(releases):
    for precedence, tag, assets in releases:
        version = tag[1:].replace("-", "~", 1) + "-1"
        debs = assets & {f"oie_{tag}_linux_amd64.deb", f"oie_{version}_amd64.deb"}
        if len(debs) > 1:
            raise ValueError(f"multiple Oiê amd64 packages in {tag}")
        if debs:
            yield precedence, tag, debs.pop(), version


def linux_packages(releases):
    for _, tag, assets in sorted(releases, reverse=True):
        rpm = [name for name in assets if name.startswith("oie-") and name.endswith("-1.x86_64.rpm")]
        pacman = [name for name in assets if name.startswith("oie-") and name.endswith("-1-x86_64.pkg.tar.zst")]
        flatpak = f"oie-{tag}-x86_64.flatpak"
        if len(rpm) > 1 or len(pacman) > 1:
            raise ValueError(f"multiple Oiê packages for the same format in {tag}")
        if rpm and pacman and flatpak in assets:
            return tag, (rpm[0], pacman[0], flatpak)
    raise ValueError("no published desktop release includes an RPM, pacman package and Flatpak bundle")


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("output", type=Path)
    parser.add_argument("--linux-packages", type=Path, required=True)
    args = parser.parse_args()
    releases = list(published_releases())
    tag, assets = linux_packages(releases)
    download_release(tag, assets, args.linux_packages)
    print(f"Staged native packages and Flatpak bundle from {tag}")
    directory = args.output / POOL
    directory.mkdir(parents=True)
    for _, tag, asset, version in sorted(apt_packages(releases), reverse=True)[:3]:
        stage_deb(tag, asset, version, directory)


if __name__ == "__main__":
    main()
