import importlib.util
import json
from pathlib import Path
import unittest
from unittest.mock import patch


ROOT = Path(__file__).resolve().parents[2]
SPEC = importlib.util.spec_from_file_location("release_oiectl", ROOT / "scripts/release-oiectl.py")
RELEASE = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(RELEASE)


class CLIReleaseTest(unittest.TestCase):
    def test_cli_patch_advances_independently_in_each_series(self):
        tags = "cli/v0.30.2\ncli/v0.30.9\ncli/v0.30.10-rc.1\n"

        def git(command, *args):
            return tags if command == "tag" and args[-1] == "cli/v0.30.*" else ""

        with patch.object(RELEASE, "git", side_effect=git):
            tag, metadata, exists = RELEASE.plan("openapi/engine/v0.30.1", "revision", "spec", "revision")
            self.assertEqual(tag, "cli/v0.30.11")
            self.assertEqual(metadata["specTag"], "openapi/engine/v0.30.1")
            self.assertFalse(exists)
            tag, _, _ = RELEASE.plan("openapi/engine/v0.31.0-rc.1", "revision", "spec", "revision")
            self.assertEqual(tag, "cli/v0.31.0-rc.1")

    def test_retry_reuses_release_and_rejects_a_moved_spec(self):
        spec_tag = "openapi/engine/v0.30.0"
        previous = {"specTag": spec_tag, "specRevision": "original", "sourceRevision": "original",
                    "request": "spec", "version": "v0.30.3"}
        with patch.object(RELEASE, "git", side_effect=["cli/v0.30.3", "metadata", json.dumps(previous)]):
            self.assertEqual(RELEASE.plan(spec_tag, "original", "spec", "original"),
                             ("cli/v0.30.3", previous, True))
        with patch.object(RELEASE, "git", side_effect=["cli/v0.30.3", "metadata", json.dumps(previous)]):
            with self.assertRaisesRegex(ValueError, "no longer points"):
                RELEASE.plan(spec_tag, "changed", "spec", "changed")
