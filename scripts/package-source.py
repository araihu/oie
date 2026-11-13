#!/usr/bin/env python3
"""Archive the release tree, packaging recipe and exact Go dependency sources."""

import argparse
import gzip
import hashlib
import io
import json
import os
from pathlib import Path, PurePosixPath
import re
import subprocess
import tarfile
from tempfile import TemporaryDirectory, TemporaryFile
from urllib.request import urlopen


def go(cwd, *args, env=None):
    return subprocess.check_output(["go", *args], cwd=cwd, env=env, text=True)


def modules(source):
    module_template = "{{if not .Main}}{{.Path}}@{{.Version}}{{end}}"
    package_template = "{{with .Module}}" + module_template + "{{end}}"
    targets = [
        (source, ("-tags=production", "./cmd/oie", "./cmd/oie-desktop")),
        (source / "cli", ("./cmd/oiectl",)),
    ]
    linked = set()
    for cwd, packages in targets:
        linked.update(go(cwd, "list", "-buildvcs=false", "-mod=readonly",
                         "-deps", "-f", package_template, *packages).split())
    for arch in ("amd64", "arm64"):
        env = dict(os.environ, GOOS="linux", GOARCH=arch, CGO_ENABLED="0")
        linked.update(go(source, "list", "-buildvcs=false", "-mod=readonly", "-deps",
                         "-f", package_template, "-tags=server", "./cmd/oie", env=env).split())
    for module in sorted(linked):
        yield json.loads(go(source, "mod", "download", "-json", module))

    # The generator's templates and runtime are copied into the CLI output.
    generator = "github.com/araihu/oapi-cli-gen"
    yield json.loads(go(source / "tools", "mod", "download", "-json", generator))


def proxy_path(path):
    # Go proxy paths escape upper-case letters with a leading exclamation mark.
    return re.sub(r"[A-Z]", lambda m: "!" + m[0].lower(), path)


def filtered_tar(data, output, exclude, include=()):
    """Keep editable inputs and their notices, dropping unrelated upstream assets."""
    with tarfile.open(fileobj=data, mode="r|gz") as original:
        with tarfile.open(fileobj=output, mode="w|") as selected:
            for member in original:
                path = PurePosixPath(member.name)
                if path.is_absolute() or ".." in path.parts:
                    raise ValueError(f"unsafe upstream source path {member.name}")
                relative = "/".join(path.parts[1:])
                if member.isdir():
                    continue
                if include and not any(relative == part or relative.startswith(part + "/") for part in include):
                    continue
                if any(relative == part or relative.startswith(part + "/") for part in exclude):
                    continue
                if not member.isfile():
                    raise ValueError(f"unexpected upstream source entry {member.name}")
                entry = tarfile.TarInfo(member.name)
                entry.size = member.size
                entry.mode = 0o755 if member.mode & 0o111 else 0o644
                selected.addfile(entry, original.extractfile(member))


def upstream_sources(archive, manifest, prefix):
    for item in json.loads(manifest.read_text())["archives"]:
        with urlopen(item["url"], timeout=60) as response, TemporaryFile() as data, TemporaryFile() as selected:
            checksum = hashlib.sha256()
            while chunk := response.read(1024 * 1024):
                checksum.update(chunk)
                data.write(chunk)
            if checksum.hexdigest() != item["sha256"]:
                raise ValueError(f"upstream source checksum mismatch for {item['name']}")
            data.seek(0)
            directory = item.get("directory", "browser-sources")
            name = item.get("packaged_name", item["name"])
            entry = tarfile.TarInfo(f"{prefix}/{directory}/{name}")
            if "packaged_sha256" in item:
                filtered_tar(data, selected, item.get("exclude", ()), item.get("include", ()))
                entry.size = selected.tell()
                selected.seek(0)
                if hashlib.file_digest(selected, "sha256").hexdigest() != item["packaged_sha256"]:
                    raise ValueError(f"filtered source checksum mismatch for {item['name']}")
                selected.seek(0)
                archive.addfile(entry, selected)
            else:
                entry.size = data.seek(0, os.SEEK_END)
                data.seek(0)
                archive.addfile(entry, data)


def package(source, tooling, version, revision, output):
    prefix = f"oie_{version}_source"
    with TemporaryDirectory(prefix="oie-vendor-") as temporary:
        vendors = Path(temporary)
        go(source, "mod", "vendor", "-o", str(vendors / "app"))
        go(source / "cli", "mod", "vendor", "-o", str(vendors / "cli"))
        with output.open("xb") as stream, gzip.GzipFile(fileobj=stream, mode="wb", mtime=0) as compressed:
            with tarfile.open(fileobj=compressed, mode="w|") as archive:
                add_tree(archive, source, revision, prefix)
                archive.add(vendors / "app", arcname=f"{prefix}/oie/vendor")
                archive.add(vendors / "cli", arcname=f"{prefix}/oie/cli/vendor")
                for path in ("scripts/package-desktop.sh", "scripts/package-source.py",
                             "scripts/verify-source-package.py", "scripts/collect-licenses.sh",
                             "scripts/install.sh", "packaging", "tests/packaging"):
                    archive.add(tooling / path, arcname=f"{prefix}/release-tooling/{path}",
                                filter=lambda m: None if "__pycache__" in Path(m.name).parts else m)
                # Module archives retain generation inputs omitted by go mod vendor,
                # including Wails TypeScript, Goshtoso's asset recipe and SQLite translations.
                for module in modules(source):
                    file = Path(module["Zip"])
                    path = proxy_path(module["Path"])
                    archive.add(file, arcname=f"{prefix}/go-sources/{path}/{file.name}")
                upstream_sources(archive, tooling / "packaging/browser-source.json", prefix)
                archive.add(tooling / "docs/release-source.md", arcname=f"{prefix}/README.md")
                identity = json.dumps({"version": version, "revision": revision}).encode()
                member = tarfile.TarInfo(f"{prefix}/source-info.json")
                member.size = len(identity)
                archive.addfile(member, io.BytesIO(identity))


def add_tree(archive, source, revision, prefix):
    if (source / ".git").exists():
        data = subprocess.check_output(["git", "archive", revision], cwd=source)
        with tarfile.open(fileobj=io.BytesIO(data)) as tree:
            for member in tree:
                content = tree.extractfile(member) if member.isfile() else None
                member.name = f"{prefix}/oie/{member.name}"
                archive.addfile(member, content)
    else:
        archive.add(source, arcname=f"{prefix}/oie")


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("source", type=Path)
    parser.add_argument("tooling", type=Path)
    parser.add_argument("version")
    parser.add_argument("revision")
    parser.add_argument("output", type=Path)
    args = parser.parse_args()
    package(args.source, args.tooling, args.version, args.revision, args.output)
