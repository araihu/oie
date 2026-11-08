#!/usr/bin/env python3
"""Refresh the pinned PDF display assets, or verify them without network access."""

import argparse
import base64
import hashlib
import io
import json
from pathlib import Path, PurePosixPath
import tarfile
import urllib.request

VERSION = "6.5.438"
INTEGRITY = "vg8Pw1x+AFKy4AMDvhuWl5cOYV8d698UoEANoFFhc79hqXDnZQqnOXoxw3HG907WHOg1JYOvLhP756boddFxpA=="
SOURCE = f"https://registry.npmjs.org/pdfjs-dist/-/pdfjs-dist-{VERSION}.tgz"
DESTINATION = Path(__file__).resolve().parents[1] / "internal/adapters/web/pdfjs"


def selected(name):
    if name in {"LICENSE", "build/pdf.mjs", "build/pdf.worker.mjs", "build/pdf.renderer.mjs", "web/pdf_viewer.css"}:
        return True
    if name.startswith("web/images/"):
        return True
    if name.startswith(("cmaps/", "iccs/")):
        return True
    if name.startswith("standard_fonts/"):
        return name.endswith(".pfb") or name.endswith("LICENSE_FOXIT")
    if name.startswith("wasm/"):
        return "quickjs" not in name.lower()
    return False


def verify():
    manifest = json.loads((DESTINATION / "manifest.json").read_text())
    if manifest["version"] != VERSION or manifest["integrity"] != "sha512-" + INTEGRITY:
        raise ValueError("PDF.js version or upstream integrity differs from the pinned tool")
    actual = {p.relative_to(DESTINATION).as_posix() for p in DESTINATION.rglob("*") if p.is_file()}
    if actual != set(manifest["files"]) | {"manifest.json"}:
        raise ValueError("PDF.js file inventory differs from its manifest")
    for name, expected in manifest["files"].items():
        if not selected(name) or not PurePosixPath(name).is_relative_to(".") or ".." in PurePosixPath(name).parts:
            raise ValueError("Unexpected PDF.js asset path")
        if hashlib.sha256((DESTINATION / name).read_bytes()).hexdigest() != expected:
            raise ValueError(f"PDF.js checksum mismatch: {name}")
    print(f"PDF.js {VERSION}: {len(manifest['files'])} pinned assets verified")


def refresh():
    with urllib.request.urlopen(SOURCE, timeout=30) as response:
        data = response.read((64 << 20) + 1)
    if len(data) > 64 << 20 or base64.b64encode(hashlib.sha512(data).digest()).decode() != INTEGRITY:
        raise ValueError("PDF.js package exceeds the limit or fails its pinned SHA-512 integrity")
    manifest = {"version": VERSION, "source": SOURCE, "integrity": "sha512-" + INTEGRITY, "files": {}}
    with tarfile.open(fileobj=io.BytesIO(data), mode="r:gz") as archive:
        for member in archive.getmembers():
            if not member.isfile() or not member.name.startswith("package/"):
                continue
            name = member.name.removeprefix("package/")
            if ".." in PurePosixPath(name).parts or PurePosixPath(name).is_absolute():
                raise ValueError("Unsafe upstream package path")
            if not selected(name):
                continue
            if member.size > 8 << 20 or name in manifest["files"]:
                raise ValueError("Unexpected PDF.js asset size or duplicate path")
            body = archive.extractfile(member).read()
            target = DESTINATION / name
            target.parent.mkdir(parents=True, exist_ok=True)
            target.write_bytes(body)
            manifest["files"][name] = hashlib.sha256(body).hexdigest()
    (DESTINATION / "manifest.json").write_text(json.dumps(manifest, indent=2, sort_keys=True) + "\n")
    verify()


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--update", action="store_true", help="download the pinned release and refresh its selected assets")
    arguments = parser.parse_args()
    refresh() if arguments.update else verify()
