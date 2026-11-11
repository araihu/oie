"""Negative controls for the actual pinned compatibility checker."""
import copy
import json
from pathlib import Path
import subprocess
import tempfile
import unittest

ROOT = Path(__file__).resolve().parents[2]
BASELINE = ROOT / "api/compatibility/engine-0.27.0.json"

class CompatibilityGate(unittest.TestCase):
    def test_breaking_and_additive_changes(self):
        baseline = json.loads(BASELINE.read_text())
        cases = []
        removed = copy.deepcopy(baseline)
        del removed["paths"]["/api/v1/engine"]
        cases.append(("removed operation", removed, False))
        removed = copy.deepcopy(baseline)
        engine = removed["components"]["schemas"]["Engine"]
        del engine["properties"]["id"]
        engine["required"].remove("id")
        cases.append(("removed response field", removed, False))
        added = copy.deepcopy(baseline)
        added["components"]["schemas"]["Engine"]["properties"]["optionalHint"] = {"type": "string"}
        cases.append(("additive optional field", added, True))
        with tempfile.TemporaryDirectory() as directory:
            for name, contract, compatible in cases:
                with self.subTest(name=name):
                    revision = Path(directory) / "revision.json"
                    revision.write_text(json.dumps(contract))
                    result = subprocess.run(
                        ["go", "tool", "-modfile=tools/go.mod", "oasdiff", "breaking",
                         str(BASELINE), str(revision), "--fail-on", "ERR"],
                        cwd=ROOT, capture_output=True, text=True, timeout=90)
                    self.assertEqual(result.returncode == 0, compatible, result.stdout + result.stderr)

if __name__ == "__main__":
    unittest.main()
