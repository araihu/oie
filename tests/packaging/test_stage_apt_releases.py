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
import stage_apt_releases as apt


class StageAPTReleaseTest(unittest.TestCase):
    tag = "v0.0.1-rc.5"
    asset = "oie_v0.0.1-rc.5_linux_amd64.deb"
    version = "0.0.1~rc.5-1"

    def test_finds_github_safe_filename_and_legacy_stable_package(self):
        releases = [
            {"draft": False, "tag_name": self.tag, "assets": [{"name": self.asset}]},
            {"draft": False, "tag_name": "v0.0.1", "assets": [{"name": "oie_0.0.1-1_amd64.deb"}]},
        ]
        with patch.object(apt, "gh", return_value=json.dumps([releases])):
            self.assertEqual([item[1:] for item in apt.published_packages()], [
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
            (directory / "SHA256SUMS").write_text(f"{apt.digest(package)}  {self.asset}\n")

        with tempfile.TemporaryDirectory() as temporary, patch.object(apt, "gh", side_effect=download):
            directory = Path(temporary)
            apt.stage_release(self.tag, self.asset, self.version, directory)
            self.assertTrue((directory / f"oie_{self.version}_amd64.deb").is_file())
            with self.assertRaisesRegex(ValueError, "unexpected version"):
                apt.stage_release(self.tag, self.asset, "0.0.1-1", directory)
