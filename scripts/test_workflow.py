#!/usr/bin/env python3
"""Exercise script arguments and deployment sequencing using fake CLIs only."""
import json
import os
from pathlib import Path
import subprocess
import tempfile
import unittest

ROOT = Path(__file__).resolve().parents[1]


class WorkflowTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.path = Path(self.temp.name)
        self.log = self.path / 'calls.jsonl'
        stub = '''#!/usr/bin/env python3
import json, os, sys
from pathlib import Path
name=Path(sys.argv[0]).name
args=sys.argv[1:]
with open(os.environ['CLI_LOG'],'a') as f: f.write(json.dumps([name,args])+'\\n')
if name=='git':
 if args[0]=='rev-parse': print('a'*40)
 elif args[0]=='status': print(os.environ.get('FAKE_DIRTY',''),end='')
 elif '--format=%cI' in args: print('2026-09-28T12:00:00+00:00')
 elif '--format=%ct' in args: print('1790596800')
elif name=='kubectl':
 if 'jsonpath={.data.UC_BASE_URL}' in args: print('https://uc.invalid/api/2.1/unity-catalog')
 elif 'jsonpath={.data.UC_CATALOG}' in args: print('fixture_catalog')
 elif '--local' in args: print('apiVersion: apps/v1\\nkind: Deployment')
 elif 'rollout' in args: sys.exit(int(os.environ.get('FAKE_ROLLOUT_EXIT','0')))
'''
        for name in ('git', 'docker', 'kubectl'):
            executable = self.path / name
            executable.write_text(stub)
            executable.chmod(0o755)
        self.env = dict(os.environ, PATH=str(self.path) + os.pathsep + os.environ['PATH'], CLI_LOG=str(self.log))
        self.env.pop('BUILD_TIMESTAMP', None)
        self.env.pop('PLATFORM', None)

    def run_script(self, name, *args):
        return subprocess.run(['bash', str(ROOT / 'scripts' / name), *args], env=self.env,
                              capture_output=True, text=True)

    def calls(self):
        return [json.loads(line) for line in self.log.read_text().splitlines()] if self.log.exists() else []

    def test_build_metadata_and_no_push(self):
        result = self.run_script('build-image.sh', 'registry.invalid/team/adapter', 'v1.2.3')
        self.assertEqual(result.returncode, 0, result.stderr)
        builds = [args for name, args in self.calls() if name == 'docker']
        self.assertEqual(len(builds), 1)
        self.assertEqual(builds[0][0], 'build')
        for arg in ('VERSION=v1.2.3', 'GIT_SHA=' + 'a'*40,
                    'BUILD_TIMESTAMP=2026-09-28T12:00:00+00:00', 'registry.invalid/team/adapter:v1.2.3'):
            self.assertIn(arg, builds[0])

    def test_dirty_checkout_never_builds(self):
        self.env['FAKE_DIRTY'] = ' M Dockerfile\n'
        self.assertNotEqual(self.run_script('build-image.sh', 'repo', 'tag').returncode, 0)
        self.assertFalse(any(name == 'docker' for name, _ in self.calls()))

    def test_invalid_arguments_never_call_clis(self):
        for script, args in [('build-image.sh', ['repo', 'bad tag']), ('deploy.sh', ['image', 'BAD_NAMESPACE'])]:
            self.assertNotEqual(self.run_script(script, *args).returncode, 0)
        self.assertEqual(self.calls(), [])

    def test_deployment_renders_before_apply(self):
        image = 'registry.invalid/adapter@sha256:' + 'b'*64
        result = self.run_script('deploy.sh', image, 'poc-test')
        self.assertEqual(result.returncode, 0, result.stderr)
        commands = [args for name, args in self.calls() if name == 'kubectl']
        rendered = next(i for i, args in enumerate(commands) if '--local' in args)
        applied = next(i for i, args in enumerate(commands) if 'apply' in args)
        self.assertLess(rendered, applied)
        self.assertIn('adapter=' + image, commands[rendered])
        self.assertNotIn('secret-example.yaml', ' '.join(commands[applied]))
        self.assertTrue(any('rollout' in args for args in commands))
        self.assertTrue(any('pods' in args for args in commands))
        self.assertTrue(any('service' in args for args in commands))

    def test_rollout_failure_is_not_success(self):
        self.env['FAKE_ROLLOUT_EXIT'] = '7'
        self.assertEqual(self.run_script('deploy.sh', 'repo:tag', 'poc-test').returncode, 7)
        self.assertTrue(any('pods' in args for _, args in self.calls()))


if __name__ == '__main__':
    unittest.main()
