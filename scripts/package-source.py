#!/usr/bin/env python3
"""Archive the release tree, packaging recipe and exact Go dependency sources."""

import argparse
import gzip
import hashlib
import io
import json
from pathlib import Path
import re
import subprocess
import tarfile
from urllib.request import urlopen


def go(cwd, *args):
    return subprocess.check_output(["go", *args], cwd=cwd, text=True)


def modules(source):
    module_template = "{{if not .Main}}{{.Path}}@{{.Version}}{{end}}"
    package_template = "{{with .Module}}" + module_template + "{{end}}"
    targets = [
        (source, ("-tags=production", "./cmd/oie", "./cmd/oie-desktop")),
        (source / "cli", ("./cmd/oiectl",)),
    ]
    linked = set()
    manifests = set()
    for cwd, packages in targets:
        linked.update(go(cwd, "list", "-buildvcs=false", "-mod=readonly",
                         "-deps", "-f", package_template, *packages).split())
        # Go may read graph-pruning metadata for modules outside the binary.
        manifests.update(go(cwd, "list", "-m", "-mod=readonly",
                            "-f", module_template, "all").split())
    # The generator's templates and runtime are copied into the CLI output.
    generator = "github.com/araihu/oapi-cli-gen"
    pin = go(source / "tools", "list", "-m", "-mod=readonly", "-f", "{{.Version}}", generator).strip()
    linked.add(f"{generator}@{pin}")
    for module in sorted(linked):
        yield json.loads(go(source, "mod", "download", "-json", module)), ("Info", "GoMod", "Zip")

    cache = Path(go(source, "env", "GOMODCACHE").strip()) / "cache/download"
    for module in sorted(manifests - linked):
        path, version = module.split("@", 1)
        mod = cache / proxy_path(path) / "@v" / f"{version}.mod"
        if mod.is_file():
            yield {"Path": path, "GoMod": mod}, ("GoMod",)


def proxy_path(path):
    # Go proxy paths escape upper-case letters with a leading exclamation mark.
    return re.sub(r"[A-Z]", lambda m: "!" + m[0].lower(), path)


def browser_sources(archive, manifest, prefix):
    for item in json.loads(manifest.read_text()):
        with urlopen(item["url"], timeout=60) as response:
            data = response.read()
        if hashlib.sha256(data).hexdigest() != item["sha256"]:
            raise ValueError(f"browser source checksum mismatch for {item['name']}")
        entry = tarfile.TarInfo(f"{prefix}/browser-sources/{item['name']}")
        entry.size = len(data)
        archive.addfile(entry, io.BytesIO(data))


def package(source, tooling, version, revision, output):
    prefix = f"oie_{version}_source"
    with output.open("xb") as stream, gzip.GzipFile(fileobj=stream, mode="wb", mtime=0) as compressed:
        with tarfile.open(fileobj=compressed, mode="w|") as archive:
            if (source / ".git").exists():
                data = subprocess.check_output(["git", "archive", revision], cwd=source)
                with tarfile.open(fileobj=io.BytesIO(data)) as tree:
                    for member in tree:
                        content = tree.extractfile(member) if member.isfile() else None
                        member.name = f"{prefix}/oie/{member.name}"
                        archive.addfile(member, content)
            else:
                archive.add(source, arcname=f"{prefix}/oie")
            for path in ("scripts/package-desktop.sh", "scripts/package-source.py",
                         "scripts/install.sh", "packaging", "tests/packaging"):
                archive.add(tooling / path, arcname=f"{prefix}/release-tooling/{path}",
                            filter=lambda m: None if "__pycache__" in Path(m.name).parts else m)
            for module, fields in modules(source):
                path = proxy_path(module["Path"])
                for field in fields:
                    file = Path(module[field])
                    archive.add(file, arcname=f"{prefix}/go-proxy/{path}/@v/{file.name}")
            browser_sources(archive, tooling / "packaging/browser-source.json", prefix)
            archive.add(tooling / "docs/release-source.md", arcname=f"{prefix}/README.md")


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("source", type=Path)
    parser.add_argument("tooling", type=Path)
    parser.add_argument("version")
    parser.add_argument("revision")
    parser.add_argument("output", type=Path)
    args = parser.parse_args()
    package(args.source, args.tooling, args.version, args.revision, args.output)
