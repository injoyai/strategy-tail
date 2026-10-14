"""使用替身 Docker 检查部署流程，不操作真实容器。"""
import json
import os
from pathlib import Path
import subprocess
import tempfile
import unittest

ROOT = Path(__file__).resolve().parents[1]
DOCKER = r'''#!/usr/bin/env python3
import json,os,sys
from pathlib import Path
root=Path(os.environ['DEPLOY_TEST_ROOT'])
args=sys.argv[1:]
with (root/'calls').open('a') as f: f.write(json.dumps(args)+'\n')
scenario=os.environ['DEPLOY_TEST_CASE']
if args[:2]==['container','inspect']: sys.exit(0)
if args[0]=='inspect': print('true')
elif args[0]=='build' and scenario=='build-fail': sys.exit(1)
elif args[0]=='exec':
 if args[-1].endswith('/api/status'):
  count=int((root/'checks').read_text()) if (root/'checks').exists() else 0
  (root/'checks').write_text(str(count+1))
  print(json.dumps({'state':'running' if scenario=='busy' or (scenario=='busy-after-build' and count==1) else 'idle'}))
 elif scenario=='health-fail': sys.exit(1)
elif args[0]=='run' and scenario=='run-fail': sys.exit(1)
elif args[0]=='rename' and scenario=='rename-fail': sys.exit(1)
'''

class DeployTest(unittest.TestCase):
    def scenario(self, case):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            (root / 'docker').write_text(DOCKER)
            (root / 'sleep').write_text('#!/bin/sh\nexit 0\n')
            for name in ('docker', 'sleep'):
                (root / name).chmod(0o755)
            env = dict(os.environ, PATH=directory + os.pathsep + os.environ['PATH'],
                       DEPLOY_TEST_ROOT=directory, DEPLOY_TEST_CASE=case)
            result = subprocess.run(['bash', str(ROOT / 'deploy.sh')], env=env,
                                    capture_output=True, text=True, timeout=10)
            calls = [json.loads(line) for line in (root / 'calls').read_text().splitlines()]
            return result.returncode, calls

    def test_busy_or_build_failure_keeps_old_container(self):
        for case in ('busy', 'busy-after-build', 'build-fail'):
            with self.subTest(case=case):
                code, calls = self.scenario(case)
                self.assertNotEqual(code, 0)
                self.assertFalse(any(call[0] in ('stop', 'rename', 'run') for call in calls))

    def test_success_keeps_backup(self):
        code, calls = self.scenario('success')
        self.assertEqual(code, 0)
        self.assertEqual(sum(call[0] == 'rename' for call in calls), 1)
        self.assertFalse(any(call[0] == 'rm' for call in calls))
        run = next(call for call in calls if call[0] == 'run')
        self.assertIn(str(ROOT / 'data') + ':/app/data', run)

    def test_rename_failure_restarts_original_container(self):
        code, calls = self.scenario('rename-fail')
        self.assertNotEqual(code, 0)
        self.assertIn(['start', 'strategy-lab'], calls)
        self.assertFalse(any(call[0] == 'run' for call in calls))

    def test_start_or_health_failure_restores_old_container(self):
        for case in ('run-fail', 'health-fail'):
            with self.subTest(case=case):
                code, calls = self.scenario(case)
                self.assertNotEqual(code, 0)
                self.assertIn(['start', 'strategy-lab'], calls)
                self.assertTrue(any(call[0] == 'rename' and call[1].startswith('strategy-lab-previous-') and call[2] == 'strategy-lab' for call in calls))
                self.assertFalse(any(call[0] == 'rm' for call in calls))

if __name__ == '__main__':
    unittest.main()
