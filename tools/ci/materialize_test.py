import subprocess
import tempfile
import unittest
from pathlib import Path

from materialize import materialize


class SourceBoundaryTest(unittest.TestCase):
    def test_current_edits_are_included_and_runtime_data_is_excluded(self):
        with tempfile.TemporaryDirectory() as tmp:
            root, target = Path(tmp) / 'repo', Path(tmp) / 'input'
            root.mkdir()
            subprocess.run(['git', 'init', '--quiet', str(root)], check=True)
            (root / '.gitignore').write_text('data/\nbin/\n.env\n')
            (root / 'main.go').write_text('before')
            subprocess.run(['git', 'add', '.'], cwd=root, check=True)
            (root / 'main.go').write_text('current edit')
            (root / 'new file.go').write_text('untracked source')
            for name in ['data/private/connection', 'bin/oie', '.env', 'tests/browser/artifacts/report.json', 'tests/browser/node_modules/module/file']:
                path = root / name
                path.parent.mkdir(parents=True, exist_ok=True)
                path.write_text('must not leave the host')
            subprocess.run(['git', 'add', '--force', 'data/private/connection'], cwd=root, check=True)
            materialize(root, target)
            self.assertEqual((target / 'main.go').read_text(), 'current edit')
            self.assertEqual((target / 'new file.go').read_text(), 'untracked source')
            self.assertFalse((target / '.git').exists())
            self.assertFalse((target / 'data').exists())
            self.assertFalse((target / 'bin').exists())
            self.assertFalse((target / '.env').exists())
            self.assertFalse((target / 'tests/browser/artifacts').exists())
            self.assertFalse((target / 'tests/browser/node_modules').exists())


if __name__ == '__main__':
    unittest.main()
