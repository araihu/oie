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
from release_desktop import digest, verify_assets, version_key


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

    def make_archives(self, folder, os_name, arch, docs):
        name = f"oie_{self.version}_{os_name}_{arch}"
        binary = folder / f"{name}.tar.gz"
        source = folder / f"{name}_source.tar.gz"
        with tarfile.open(source, "w:gz"):
            pass
        build = {"version": self.version, "sourceRevision": self.revision, "os": os_name, "arch": arch,
                 "correspondingSourceSHA256": digest(source)}
        source_url = f"https://github.com/araihu/oie/releases/download/{self.version}/{source.name}"
        with tarfile.open(binary, "w:gz") as archive:
            for filename, content in {
                "build-info.json": json.dumps(build),
                "SOURCE.txt": f"Corresponding source: {source_url}\n",
            }.items():
                data = content.encode()
                item = tarfile.TarInfo(f"{docs}/{filename}")
                item.size = len(data)
                archive.addfile(item, io.BytesIO(data))
        for asset in (binary, source):
            (folder / f"{asset.name}.sha256").write_text(f"{digest(asset)}  {asset.name}\n")
        identity = {"version": self.version, "revision": self.revision, "status": "passed", "platform": f"{os_name}/{arch}"}
        (folder / "report.json").write_text(json.dumps(dict(identity, binaryArchiveSHA256=digest(binary))))
        (folder / "source-verification.json").write_text(json.dumps(dict(identity, sourceArchiveSHA256=digest(source))))

    def test_assembles_and_verifies_both_binary_source_pairs(self):
        with tempfile.TemporaryDirectory() as temporary:
            stage = Path(temporary)
            linux, macos, output = (stage / name for name in ("linux", "macos", "out"))
            linux.mkdir()
            macos.mkdir()
            self.make_archives(linux, "linux", "amd64", "share/doc/oie")
            self.make_archives(macos, "darwin", "arm64", "Oie.app/Contents/Resources")
            (linux / "source-receipt.json").write_text(json.dumps({
                "version": self.version, "revision": self.revision, "packagingRevision": self.revision,
                "sourceArchiveSHA256": "b" * 64,
            }))
            command = [
                sys.executable, str(ROOT / "scripts/release_desktop.py"), "assemble",
                "--version", self.version, "--revision", self.revision,
                "--linux", str(linux), "--linux-report", str(linux / "report.json"),
                "--macos", str(macos), "--macos-report", str(macos / "report.json"),
                "--output", str(output),
            ]
            subprocess.run(command, check=True)
            receipt = json.loads((output / "packaging-receipt.json").read_text())
            self.assertEqual(len(receipt["artifacts"]), 4)
            self.assertEqual(len(receipt["verification"]), 4)
            self.assertIn(f"default_version={self.version}\n", (output / "install.sh").read_text())
            verify_assets(self.version, self.revision, output, None)
            remote = stage / "github-assets.json"
            assets = [{"name": file.name, "state": "uploaded", "digest": f"sha256:{digest(file)}"}
                      for file in output.iterdir()]
            remote.write_text(json.dumps(assets))
            verify_assets(self.version, self.revision, output, remote)

            with self.subTest("GitHub reports a corrupted source upload"):
                source = next(asset for asset in assets if asset["name"].endswith("_source.tar.gz"))
                source["digest"] = "sha256:" + "0" * 64
                remote.write_text(json.dumps(assets))
                with self.assertRaisesRegex(ValueError, "GitHub release asset names or digests"):
                    verify_assets(self.version, self.revision, output, remote)

            with self.subTest("an offline verification report is stale"):
                report = macos / "source-verification.json"
                record = json.loads(report.read_text())
                report.write_text(json.dumps(dict(record, sourceArchiveSHA256="0" * 64)))
                command[-1] = str(stage / "second-assembly")
                result = subprocess.run(command, capture_output=True, text=True)
                self.assertNotEqual(result.returncode, 0)
                self.assertIn("verification does not match", result.stderr)
