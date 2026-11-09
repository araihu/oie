"""Reproduce the pinned browser LRU asset, checking archive and source integrity."""
import base64
import hashlib
import io
import json
from pathlib import Path
import tarfile
import urllib.request

root = Path(__file__).resolve().parents[1]
destination = root / "internal/adapters/web/cache_vendor"
lock = json.loads((destination / "manifest.json").read_text())
with urllib.request.urlopen(lock["archive"], timeout=30) as response:
    archive = response.read(2 * 1024 * 1024)
assert "sha512-" + base64.b64encode(hashlib.sha512(archive).digest()).decode() == lock["integrity"]
with tarfile.open(fileobj=io.BytesIO(archive), mode="r:gz") as package:
    source = package.extractfile(lock["source"]).read()
    license_text = package.extractfile("package/LICENSE.md").read()
assert hashlib.sha256(source).hexdigest() == lock["sourceSHA256"]
notice = b"\n/*! lru-cache 11.2.6. Blue Oak Model License 1.0.0: https://blueoakcouncil.org/license/1.0.0 */\n"
asset = source + notice
assert hashlib.sha256(asset).hexdigest() == lock["sha256"]
(destination / "lru-cache.min.mjs").write_bytes(asset)
(destination / "LICENSE.md").write_bytes(license_text)
