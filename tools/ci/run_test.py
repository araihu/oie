"""Exercise CI exit status and owned-resource cleanup without a real engine."""
import json
import os
from pathlib import Path
import signal
import subprocess
import tempfile
import time
import unittest


DRIVER = '''#!/usr/bin/env python3
import json, os, signal, sys, time
from pathlib import Path
args = sys.argv[1:]
source = Path(args[args.index('-source')+1])
assert (source / 'current.go').read_text() == 'current edit'
assert not (source / 'data').exists()
if os.environ.get('CI_TEST_RESIST_TERM'):
    signal.signal(signal.SIGTERM, signal.SIG_IGN)
if os.environ.get('CI_TEST_WRITE_ARTIFACTS'):
    artifacts = Path(args[args.index('-artifacts')+1])
    artifacts.mkdir(parents=True, exist_ok=True)
    (artifacts / 'fixture.log').write_text('retained failure evidence')
# Publish readiness only after initialization, as one complete filesystem update.
capture = Path(os.environ['CI_TEST_CAPTURE'])
pending = capture.with_suffix('.tmp')
pending.write_text(json.dumps({'args': args, 'source': str(source), 'pid': os.getpid(), 'runner': os.environ.get('_EXPERIMENTAL_DAGGER_RUNNER_HOST'), 'artifacts_env': os.environ.get('OIE_CI_ARTIFACTS')}))
pending.replace(capture)
if os.environ.get('CI_TEST_SLEEP'):
    time.sleep(30)
sys.exit(int(os.environ.get('CI_TEST_EXIT', '0')))
'''


class RunnerLifecycleTest(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.addCleanup(self.tmp.cleanup)
        self.base = Path(self.tmp.name)
        self.root = self.base / 'repo'
        (self.root / 'tools/ci').mkdir(parents=True)
        original = Path(__file__).parent
        for name in ['run.sh', 'materialize.py']:
            (self.root / 'tools/ci' / name).write_bytes((original / name).read_bytes())
        (self.root / 'tools/ci/preflight_test.py').write_text('import unittest\nclass Preflight(unittest.TestCase):\n def test_ready(self): self.assertTrue(True)\n')
        (self.root / 'current.go').write_text('current edit')
        (self.root / 'data').mkdir()
        (self.root / 'data/private').write_text('excluded')
        (self.root / '.gitignore').write_text('data/\n')
        subprocess.run(['git', 'init', '--quiet', str(self.root)], check=True)
        subprocess.run(['git', 'add', '.'], cwd=self.root, check=True)
        self.bin = self.base / 'bin'
        self.bin.mkdir()
        go = self.bin / 'go'
        go.write_text('#!/usr/bin/env python3\nimport os, sys\nfrom pathlib import Path\n'
                      'if "build" in sys.argv:\n'
                      ' p = Path(sys.argv[sys.argv.index("-o")+1]); p.write_text('+repr(DRIVER)+'); p.chmod(0o700)\n')
        go.chmod(0o700)
        self.capture = self.base / 'capture.json'
        self.env = {**os.environ, 'PATH': str(self.bin)+os.pathsep+os.environ['PATH'],
                    'CI_TEST_CAPTURE': str(self.capture), 'TMPDIR': str(self.base)}
        self.env.pop('OIE_CI_SSH_HOST', None)
        self.env.pop('OIE_CI_ARTIFACTS', None)
        self.env.pop('CI_TEST_WRITE_ARTIFACTS', None)
        self.env.pop('CI_TEST_SLEEP', None)
        self.env.pop('CI_TEST_EXIT', None)
        self.env.pop('CI_TEST_RESIST_TERM', None)
        self.env.pop('OIE_CI_CLEANUP_GRACE_SECONDS', None)
        self.command = ['sh', str(self.root / 'tools/ci/run.sh'), 'checks']

    def test_success_and_failure_keep_status_and_remove_snapshot(self):
        for code in [0, 23]:
            result = subprocess.run(self.command, env={**self.env, 'CI_TEST_EXIT': str(code)}, capture_output=True, text=True, timeout=10)
            self.assertEqual(result.returncode, code, result.stderr)
            record = json.loads(self.capture.read_text())
            self.assertFalse(Path(record['source']).parent.exists())
            self.assertEqual(record['args'][record['args'].index('-mode')+1], 'checks')

    def test_artifact_destination_survives_failure_and_snapshot_cleanup(self):
        destinations = [
            (None, self.root / 'tests/browser/artifacts/ci'),
            (str(self.base / 'separate evidence'), self.base / 'separate evidence'),
            ('proof/named run', self.root / 'proof/named run'),
        ]
        for configured, expected in destinations:
            with self.subTest(configured=configured):
                env = {**self.env, 'CI_TEST_EXIT': '23', 'CI_TEST_WRITE_ARTIFACTS': '1'}
                if configured is not None:
                    env['OIE_CI_ARTIFACTS'] = configured
                result = subprocess.run(self.command, env=env, capture_output=True, text=True, timeout=10)
                self.assertEqual(result.returncode, 23, result.stderr)
                record = json.loads(self.capture.read_text())
                actual = record['args'][record['args'].index('-artifacts')+1]
                self.assertEqual(actual, str(expected))
                self.assertEqual(record['artifacts_env'], actual)
                self.assertEqual((expected / 'fixture.log').read_text(), 'retained failure evidence')
                self.assertFalse(Path(record['source']).parent.exists())

    def test_independent_tunnel_is_closed_even_when_checks_fail(self):
        ssh = self.bin / 'ssh'
        ssh.write_text('''#!/usr/bin/env python3
import os, socket, sys, time
from pathlib import Path
address = sys.argv[sys.argv.index('-L')+1].split(':', 1)[0]
sock = socket.socket(socket.AF_UNIX); sock.bind(address); sock.listen()
Path(os.environ['CI_TEST_SSH_PID']).write_text(str(os.getpid()))
time.sleep(30)
''')
        ssh.chmod(0o700)
        pidfile = self.base / 'ssh.pid'
        result = subprocess.run(self.command, env={**self.env, 'OIE_CI_SSH_HOST': 'fixture-host', 'CI_TEST_EXIT': '23', 'CI_TEST_SSH_PID': str(pidfile)}, capture_output=True, text=True, timeout=10)
        self.assertEqual(result.returncode, 23, result.stderr)
        record = json.loads(self.capture.read_text())
        self.assertTrue(record['runner'].endswith('/engine.sock'))
        self.assertFalse(Path(record['source']).parent.exists())
        with self.assertRaises(ProcessLookupError):
            os.kill(int(pidfile.read_text()), 0)

    def test_sigterm_is_failure_and_cleans_owned_snapshot(self):
        self.check_termination(whole_group=True)

    def test_shell_only_sigterm_stops_owned_runner_and_cleans_snapshot(self):
        self.check_termination(whole_group=False)

    def test_term_resistant_runner_is_killed_and_snapshot_removed(self):
        self.check_termination(whole_group=False, resistant=True)

    def check_termination(self, whole_group, resistant=False):
        env = {**self.env, 'CI_TEST_SLEEP': '1'}
        if resistant:
            env['CI_TEST_RESIST_TERM'] = '1'
            env['OIE_CI_CLEANUP_GRACE_SECONDS'] = '.15'
        child = subprocess.Popen(self.command, env=env, stdout=subprocess.DEVNULL, stderr=subprocess.PIPE, text=True, start_new_session=True)
        try:
            for _ in range(100):
                if self.capture.exists():
                    break
                time.sleep(.02)
            self.assertTrue(self.capture.exists())
            record = json.loads(self.capture.read_text())
            started = time.monotonic()
            if whole_group:
                os.killpg(child.pid, signal.SIGTERM)
            else:
                os.kill(child.pid, signal.SIGTERM)
            _, error = child.communicate(timeout=5)
            self.assertEqual(child.returncode, 143, error)
            self.assertLess(time.monotonic()-started, 3, 'owned runner cleanup exceeded its grace')
            self.assertFalse(Path(record['source']).parent.exists())
            with self.assertRaises(ProcessLookupError):
                os.kill(record['pid'], 0)
        finally:
            if child.poll() is None:
                os.killpg(child.pid, signal.SIGKILL)
                child.communicate()
            else:
                child.communicate()
