import json
from pathlib import Path
import shutil
import subprocess
import sys
import tempfile
import unittest
from unittest.mock import patch


ROOT = Path(__file__).resolve().parents[2]
sys.path.insert(0, str(ROOT / "scripts"))
import stage_package_releases as packages


class StageAPTReleaseTest(unittest.TestCase):
    tag = "v0.0.1-rc.5"
    asset = "oie_v0.0.1-rc.5_linux_amd64.deb"
    version = "0.0.1~rc.5-1"

    def test_finds_github_safe_filename_and_legacy_stable_package(self):
        releases = [
            {"draft": False, "tag_name": self.tag, "assets": [{"name": self.asset}]},
            {"draft": False, "tag_name": "v0.0.1", "assets": [{"name": "oie_0.0.1-1_amd64.deb"}]},
        ]
        with patch.object(packages, "gh", return_value=json.dumps([releases])):
            self.assertEqual([item[1:] for item in packages.apt_packages(packages.published_releases())], [
                (self.tag, self.asset, self.version),
                ("v0.0.1", "oie_0.0.1-1_amd64.deb", "0.0.1-1"),
            ])

    @unittest.skipUnless(shutil.which("dpkg-deb"), "dpkg-deb is required")
    def test_preserves_debian_version_in_apt_pool(self):
        def download(*args):
            directory = Path(args[args.index("--dir") + 1])
            control = directory / "package/DEBIAN/control"
            control.parent.mkdir(parents=True)
            control.write_text(f"Package: oie\nVersion: {self.version}\nArchitecture: amd64\n"
                               "Maintainer: Oiê <test@example.com>\nDescription: Fixture\n")
            package = directory / self.asset
            subprocess.run(["dpkg-deb", "--build", str(control.parent.parent), str(package)],
                           check=True, capture_output=True)
            (directory / "SHA256SUMS").write_text(f"{packages.digest(package)}  {self.asset}\n")

        with tempfile.TemporaryDirectory() as temporary, patch.object(packages, "gh", side_effect=download):
            directory = Path(temporary)
            packages.stage_deb(self.tag, self.asset, self.version, directory)
            self.assertTrue((directory / f"oie_{self.version}_amd64.deb").is_file())
            with self.assertRaisesRegex(ValueError, "unexpected version"):
                packages.stage_deb(self.tag, self.asset, "0.0.1-1", directory)

    def test_native_selection_uses_semver_and_requires_all_formats_from_one_release(self):
        def assets(tag, version):
            return {f"oie-{version}-1.x86_64.rpm", f"oie-{version}-1-x86_64.pkg.tar.zst",
                    f"oie-{tag}-x86_64.flatpak"}

        candidate = "v0.0.1-rc.28"
        latest_assets = assets(candidate, "0.0.1.rc.28")
        releases = [
            (packages.version_key("v0.0.1-rc.9"), "v0.0.1-rc.9", assets("v0.0.1-rc.9", "0.0.1.rc.9")),
            (packages.version_key("v0.0.1"), "v0.0.1", {"oie-0.0.1-1.x86_64.rpm"}),
            (packages.version_key(candidate), candidate, latest_assets),
        ]
        tag, staged = packages.linux_packages(releases)
        self.assertEqual(tag, candidate)
        self.assertEqual(set(staged), latest_assets)

    def test_download_rejects_a_tampered_package(self):
        def download(*args):
            directory = Path(args[args.index("--dir") + 1])
            (directory / "oie.rpm").write_bytes(b"altered")
            (directory / "SHA256SUMS").write_text(f"{'0' * 64}  oie.rpm\n")

        with tempfile.TemporaryDirectory() as temporary, patch.object(packages, "gh", side_effect=download):
            with self.assertRaisesRegex(ValueError, "release checksum mismatch for oie.rpm"):
                packages.download_release(self.tag, ("oie.rpm",), Path(temporary))
