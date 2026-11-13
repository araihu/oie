import hashlib
import importlib.util
import json
from pathlib import Path
import tarfile
import tempfile
import unittest
from unittest.mock import patch


ROOT = Path(__file__).resolve().parents[2]
SPEC = importlib.util.spec_from_file_location("package_source", ROOT / "scripts/package-source.py")
PACKAGE = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(PACKAGE)


class SourcePackageTest(unittest.TestCase):
    def test_archive_retains_build_inputs_and_upstream_sources(self):
        with tempfile.TemporaryDirectory() as tmp:
            directory = Path(tmp)
            app = directory / "app"
            app.mkdir()
            (app / "go.mod").write_text("module example.com/app\n")
            dep = directory / "v1.0.0.zip"
            dep.write_bytes(b"module sources")
            browser = directory / "runtime.tgz"
            browser.write_bytes(b"editable JavaScript source")
            manifest = directory / "browser-source.json"
            manifest.write_text(json.dumps([{
                "name": browser.name,
                "url": browser.as_uri(),
                "sha256": hashlib.sha256(browser.read_bytes()).hexdigest(),
            }]))

            output = directory / "source.tar.gz"
            dependency = {"Path": "example.com/SomeModule", "Zip": dep}
            add_sources = PACKAGE.browser_sources
            with patch.object(PACKAGE, "modules", return_value=[(dependency, ("Zip",))]):
                with patch.object(PACKAGE, "browser_sources", side_effect=lambda archive, _, prefix:
                                  add_sources(archive, manifest, prefix)):
                    PACKAGE.package(app, ROOT, "v0.0.1", "a" * 40, output)

            with tarfile.open(output) as archive:
                prefix = "oie_v0.0.1_source/"
                for member, expected in {
                    "oie/go.mod": (app / "go.mod").read_bytes(),
                    "go-proxy/example.com/!some!module/@v/v1.0.0.zip": dep.read_bytes(),
                    "browser-sources/runtime.tgz": browser.read_bytes(),
                    "release-tooling/packaging/licenses/pdfjs/LICENSE_BROTLI":
                        (ROOT / "packaging/licenses/pdfjs/LICENSE_BROTLI").read_bytes(),
                }.items():
                    self.assertEqual(archive.extractfile(prefix + member).read(), expected)

    def test_checksum_mismatch_stops_packaging(self):
        with tempfile.TemporaryDirectory() as tmp:
            directory = Path(tmp)
            browser = directory / "runtime.tgz"
            browser.write_bytes(b"unexpected bytes")
            manifest = directory / "browser-source.json"
            manifest.write_text(json.dumps([{
                "name": browser.name, "url": browser.as_uri(), "sha256": "0" * 64,
            }]))
            with tarfile.open(directory / "source.tar", "w") as archive:
                with self.assertRaisesRegex(ValueError, "checksum mismatch"):
                    PACKAGE.browser_sources(archive, manifest, "source")
