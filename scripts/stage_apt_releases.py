#!/usr/bin/env python3
"""Stage recent signed-index inputs from the immutable desktop release assets."""

import argparse
import json
from pathlib import Path
import re
import shutil
import subprocess
from tempfile import TemporaryDirectory

from release_desktop import digest, version_key


REPOSITORY = "araihu/oie"
POOL = "apt/pool/main/o/oie"
PACKAGE = re.compile(r"oie_[0-9A-Za-z.+~:-]+_amd64\.deb")


def gh(*args):
    return subprocess.check_output(["gh", *args], text=True)


def stage_release(tag, asset, directory):
    with TemporaryDirectory() as temporary:
        download = Path(temporary)
        gh("release", "download", tag, "--repo", REPOSITORY, "--dir", temporary,
           "--pattern", asset, "--pattern", "SHA256SUMS")
        entries = [line.split() for line in (download / "SHA256SUMS").read_text().splitlines()]
        checksums = [entry[0] for entry in entries if len(entry) == 2 and entry[1] == asset]
        if checksums != [digest(download / asset)]:
            raise ValueError(f"release checksum mismatch for {asset}")
        version = subprocess.check_output(["dpkg-deb", "-f", str(download / asset), "Version"], text=True).strip()
        if asset != f"oie_{version}_amd64.deb":
            raise ValueError(f"unexpected version in {asset}")
        shutil.copyfile(download / asset, directory / asset)


def published_packages():
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
            debs = [asset["name"] for asset in release["assets"] if PACKAGE.fullmatch(asset["name"])]
            if len(debs) > 1:
                raise ValueError(f"multiple Oiê amd64 packages in {tag}")
            if debs:
                yield precedence, tag, debs[0]


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("output", type=Path)
    args = parser.parse_args()
    directory = args.output / POOL
    directory.mkdir(parents=True)
    releases = sorted(published_packages(), reverse=True)[:3]
    for _, tag, asset in releases:
        stage_release(tag, asset, directory)


if __name__ == "__main__":
    main()
