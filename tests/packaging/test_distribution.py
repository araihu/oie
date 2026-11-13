import hashlib
import io
import json
from pathlib import Path
import subprocess
import sys
import tarfile
import tempfile
import unittest
from xml.etree import ElementTree


ROOT = Path(__file__).resolve().parents[2]
VERSION = "v0.0.1-rc.1"


class DistributionTest(unittest.TestCase):
    def setUp(self):
        directory = tempfile.TemporaryDirectory()
        self.addCleanup(directory.cleanup)
        self.root = Path(directory.name)
        self.assets = self.root / "assets"
        self.assets.mkdir()
        self.output = self.root / "output"
        self.linux = self.assets / f"oie_{VERSION}_linux_amd64.tar.gz"
        self.macos = self.assets / f"oie_{VERSION}_darwin_arm64.tar.gz"
        self.macos.write_bytes(b"macOS archive fixture")
        self.write_linux_archive()

    def write_linux_archive(self, extra_member=None):
        info = json.dumps({"version": VERSION, "os": "linux", "arch": "amd64",
                           "builtAt": "2026-11-12T14:20:52Z"}).encode()
        files = {
            "bin/oie-desktop": b"executable",
            "share/applications/org.araihu.oie.desktop": b"Exec=@OIE_EXEC@\n",
            "share/icons/hicolor/512x512/apps/org.araihu.oie.png": b"icon",
            "share/doc/oie/LICENSE": b"license",
            "share/doc/oie/build-info.json": info,
        }
        if extra_member is not None:
            files[extra_member] = b"outside the payload"
        with tarfile.open(self.linux, "w:gz") as archive:
            for name, content in files.items():
                item = tarfile.TarInfo(name)
                item.size = len(content)
                item.mode = 0o600
                archive.addfile(item, io.BytesIO(content))
        (self.assets / "SHA256SUMS").write_text("".join(
            f"{hashlib.sha256(path.read_bytes()).hexdigest()}  {path.name}\n"
            for path in (self.linux, self.macos)
        ))

    def generate(self, *options):
        return subprocess.run(
            [sys.executable, str(ROOT / "scripts/package-distribution.py"),
             "--version", VERSION, "--assets", str(self.assets), "--output", str(self.output), *options],
            capture_output=True, text=True, check=False,
        )

    def test_recipes_pin_the_published_archives(self):
        result = self.generate("--recipes-only")
        self.assertEqual(result.returncode, 0, result.stderr)
        cask = (self.output / "homebrew/Casks/oie.rb").read_text()
        self.assertIn('version "0.0.1-rc.1"', cask)
        self.assertIn(hashlib.sha256(self.macos.read_bytes()).hexdigest(), cask)
        manifest = json.loads((self.output / "flatpak/com.araihu.oie.json").read_text())
        self.assertEqual(manifest["app-id"], "com.araihu.oie")
        source = manifest["modules"][0]["sources"][0]
        self.assertTrue(source["url"].endswith(f"/{VERSION}/{self.linux.name}"))
        self.assertEqual(source["sha256"], hashlib.sha256(self.linux.read_bytes()).hexdigest())
        metadata = ElementTree.parse(self.output / "flatpak/com.araihu.oie.metainfo.xml")
        self.assertEqual(metadata.findtext("id"), manifest["app-id"])
        self.assertEqual(metadata.findtext("launchable"), f'{manifest["app-id"]}.desktop')
        self.assertEqual(metadata.find("releases/release").get("type"), "development")

    def test_modified_archive_leaves_no_output(self):
        self.macos.write_bytes(b"modified")
        result = self.generate("--recipes-only")
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("SHA-256 mismatch", result.stderr)
        self.assertFalse(self.output.exists())

    def test_declared_source_archive_must_be_present(self):
        with (self.assets / "SHA256SUMS").open("a") as checksums:
            checksums.write(f"{'0' * 64}  oie_{VERSION}_linux_amd64_source.tar.gz\n")
        result = self.generate("--recipes-only")
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("linux_amd64_source.tar.gz", result.stderr)
        self.assertFalse(self.output.exists())

    def test_traversal_leaves_no_output(self):
        self.write_linux_archive("../../escaped")
        result = self.generate("--nfpm", sys.executable)
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("outside the destination", result.stderr)
        self.assertFalse(self.output.exists())

    def test_native_versions_and_readable_launcher(self):
        nfpm = self.root / "nfpm"
        nfpm.write_text(f"""#!{sys.executable}
from pathlib import Path
import os
import stat
import sys

assert Path("payload/share/applications/org.araihu.oie.desktop").read_text() == "Exec=/usr/bin/oie-desktop\\n"
assert "<id>org.araihu.oie</id>" in Path("payload/share/metainfo/org.araihu.oie.metainfo.xml").read_text()
assert stat.S_IMODE(Path("payload/share/doc/oie/LICENSE").stat().st_mode) == 0o644
assert stat.S_IMODE(Path("payload/bin/oie-desktop").stat().st_mode) == 0o755
Path("native", sys.argv[5]).write_text(os.environ["OIE_PACKAGE_VERSION"] + "/" + os.environ["OIE_PACKAGE_PRERELEASE"])
""")
        nfpm.chmod(0o755)
        result = self.generate("--nfpm", str(nfpm))
        self.assertEqual(result.returncode, 0, result.stderr)
        for format_name, version in (("deb", "0.0.1/rc.1"), ("rpm", "0.0.1/rc.1"), ("archlinux", "0.0.1rc.1/")):
            self.assertEqual((self.output / "native" / format_name).read_text().strip(), version)
        self.assertFalse((self.output / "payload").exists())
