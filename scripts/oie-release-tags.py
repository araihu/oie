#!/usr/bin/env python3
"""List canonical Oiê application tags in descending release order."""

import subprocess

from release_desktop import version_key


def main():
    refs = subprocess.check_output(
        ["git", "ls-remote", "--refs", "--tags", "origin", "refs/tags/v*.*.*"],
        text=True,
    )
    versions = []
    for line in refs.splitlines():
        tag = line.split("\t", 1)[1].removeprefix("refs/tags/")
        try:
            key = version_key(tag)
        except ValueError:
            continue
        versions.append((key, tag))
    for _, tag in sorted(versions, reverse=True):
        print(tag)


if __name__ == "__main__":
    main()
