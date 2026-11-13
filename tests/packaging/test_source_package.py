import hashlib
import importlib.util
import io
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
    def test_filtered_archive_keeps_source_and_drops_fixtures(self):
        original = io.BytesIO()
        with tarfile.open(fileobj=original, mode="w:gz") as archive:
            for path in ("LICENSE", "src/core.js", "test/pdfs/freeculture.pdf"):
                member = tarfile.TarInfo(f"upstream/{path}")
                data = path.encode()
                member.size = len(data)
                archive.addfile(member, io.BytesIO(data))
        original.seek(0)
        selected = io.BytesIO()
        PACKAGE.filtered_tar(original, selected, ("test",))
        selected.seek(0)
        with tarfile.open(fileobj=selected) as archive:
            self.assertEqual(archive.getnames(), ["upstream/LICENSE", "upstream/src/core.js"])
            self.assertEqual(archive.extractfile("upstream/src/core.js").read(), b"src/core.js")

    def test_archive_retains_build_inputs_and_upstream_sources(self):
        with tempfile.TemporaryDirectory() as tmp:
            directory = Path(tmp)
            app = directory / "app"
            app.mkdir()
            (app / "go.mod").write_text("module example.com/app\ngo 1.27.4\n")
            (app / "main.go").write_text("package main\nfunc main() {}\n")
            (app / "cli").mkdir()
            (app / "cli/go.mod").write_text("module example.com/app/cli\ngo 1.27.4\n")
            (app / "cli/main.go").write_text("package main\nfunc main() {}\n")
            dep = directory / "v1.0.0.zip"
            dep.write_bytes(b"module sources")
            browser = directory / "runtime.tgz"
            browser.write_bytes(b"editable JavaScript source")
            manifest = directory / "browser-source.json"
            manifest.write_text(json.dumps({"archives": [{
                "name": browser.name,
                "url": browser.as_uri(),
                "sha256": hashlib.sha256(browser.read_bytes()).hexdigest(),
            }]}))

            output = directory / "source.tar.gz"
            dependency = {"Path": "example.com/SomeModule", "Zip": dep}
            add_sources = PACKAGE.upstream_sources

            def vendor(cwd, *args):
                self.assertEqual(args[:-1], ("mod", "vendor", "-o"))
                directory = Path(args[-1])
                directory.mkdir()
                (directory / "modules.txt").write_text(cwd.relative_to(app).as_posix())
            with patch.object(PACKAGE, "go", side_effect=vendor), patch.object(PACKAGE, "modules", return_value=[dependency]):
                with patch.object(PACKAGE, "upstream_sources", side_effect=lambda archive, _, prefix:
                                  add_sources(archive, manifest, prefix)):
                    PACKAGE.package(app, ROOT, "v0.0.1", "a" * 40, output)

            with tarfile.open(output) as archive:
                prefix = "oie_v0.0.1_source/"
                for member, expected in {
                    "oie/vendor/modules.txt": b".",
                    "oie/cli/vendor/modules.txt": b"cli",
                    "oie/go.mod": (app / "go.mod").read_bytes(),
                    "go-sources/example.com/!some!module/v1.0.0.zip": dep.read_bytes(),
                    "source-info.json": json.dumps({"version": "v0.0.1", "revision": "a" * 40}).encode(),
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
            manifest.write_text(json.dumps({"archives": [{
                "name": browser.name, "url": browser.as_uri(), "sha256": "0" * 64,
            }]}))
            with tarfile.open(directory / "source.tar", "w") as archive:
                with self.assertRaisesRegex(ValueError, "checksum mismatch"):
                    PACKAGE.upstream_sources(archive, manifest, "source")
