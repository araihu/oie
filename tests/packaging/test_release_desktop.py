import hashlib
import io
import json
from pathlib import Path
import subprocess
import sys
import tarfile
import tempfile
import unittest


ROOT = Path(__file__).resolve().parents[2]
sys.path.insert(0, str(ROOT / "scripts"))
from release_desktop import version_key


class ReleaseVersionTest(unittest.TestCase):
    def test_semver_precedence(self):
        versions = ["v0.0.1-alpha", "v0.0.1-rc.2", "v0.0.1-rc.10", "v0.0.1", "v0.0.2-rc.1"]
        self.assertEqual(sorted(reversed(versions), key=version_key), versions)

    def test_invalid_release_tags(self):
        for tag in ("main", "v00.0.1", "v0.0.1-rc.01", "v0.0.1-rc..1", "v0.0.1+build", "v0.0.1-"):
            with self.subTest(tag=tag), self.assertRaises(ValueError):
                version_key(tag)


class ReleaseAssemblyTest(unittest.TestCase):
    version = "v0.0.1-rc.7"
    revision = "a" * 40

    def make_archive(self, folder, os_name, arch, metadata):
        name = f"oie_{self.version}_{os_name}_{arch}"
        for suffix in (".tar.gz", "_source.tar.gz"):
            asset = folder / (name + suffix)
            with tarfile.open(asset, "w:gz") as archive:
                build = json.dumps({"version": self.version, "sourceRevision": self.revision,
                                    "os": os_name, "arch": arch}).encode()
                item = tarfile.TarInfo(metadata)
                item.size = len(build)
                archive.addfile(item, io.BytesIO(build))
            checksum = hashlib.sha256(asset.read_bytes()).hexdigest()
            (folder / f"{asset.name}.sha256").write_text(f"{checksum}  {asset.name}\n")

    def test_assembles_both_targets_and_sets_installer_default(self):
        with tempfile.TemporaryDirectory() as temporary:
            stage = Path(temporary)
            linux, macos, output = (stage / name for name in ("linux", "macos", "out"))
            linux.mkdir()
            macos.mkdir()
            self.make_archive(linux, "linux", "amd64", "share/doc/oie/build-info.json")
            self.make_archive(macos, "darwin", "arm64", "Oie.app/Contents/Resources/build-info.json")
            report = stage / "report.json"
            report.write_text(json.dumps({"version": self.version, "revision": self.revision, "status": "passed"}))
            (linux / "source-receipt.json").write_text(json.dumps({
                "version": self.version, "revision": self.revision, "packagingRevision": self.revision,
                "sourceArchiveSHA256": "b" * 64,
            }))
            subprocess.run([
                sys.executable, str(ROOT / "scripts/release_desktop.py"), "assemble",
                "--version", self.version, "--revision", self.revision,
                "--linux", str(linux), "--linux-report", str(report),
                "--macos", str(macos), "--output", str(output),
            ], check=True)
            receipt = json.loads((output / "packaging-receipt.json").read_text())
            self.assertEqual(len(receipt["artifacts"]), 4)
            self.assertIn(f"default_version={self.version}\n", (output / "install.sh").read_text())
            checksums = (output / "SHA256SUMS").read_text().splitlines()
            self.assertEqual(len(checksums), 6)
            for line in checksums:
                digest, name = line.split()
                self.assertEqual(hashlib.sha256((output / name).read_bytes()).hexdigest(), digest)
