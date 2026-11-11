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
 elif args[-1].endswith('/api/market-data/status'):
  print(json.dumps({'storage':'other-store' if scenario=='wrong-storage' else 'mysql://test/market', 'readError':'database unavailable' if scenario=='mysql-unavailable' else ''}))
 elif scenario=='health-fail': sys.exit(1)
elif args[0]=='run' and scenario=='run-fail': sys.exit(1)
elif args[0]=='rename' and scenario=='rename-fail': sys.exit(1)
'''

class ScreenPushTest(unittest.TestCase):
    def test_uses_project_root_from_another_directory(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            stub = '''#!/usr/bin/env python3
import json, os, sys
from pathlib import Path
with Path(os.environ['SCREEN_PUSH_TEST_LOG']).open('a') as log:
    log.write(json.dumps([Path(sys.argv[0]).name, os.getcwd(), sys.argv[1:]]) + '\\n')
'''
            for name in ('go', 'docker', 'sleep'):
                (root / name).write_text(stub)
                (root / name).chmod(0o755)
            log = root / 'calls'
            env = dict(os.environ, PATH=directory + os.pathsep + os.environ['PATH'],
                       SCREEN_PUSH_TEST_LOG=str(log))
            result = subprocess.run(['sh', str(ROOT / 'cmd/screen/dockerpush.sh')],
                                    cwd=directory, env=env, capture_output=True,
                                    text=True, timeout=10)
            self.assertEqual(result.returncode, 0, result.stderr)
            calls = [json.loads(line) for line in log.read_text().splitlines()]
            self.assertTrue(all(call[1] == str(ROOT) for call in calls))
            build = next(call[2] for call in calls if call[0] == 'go')
            self.assertEqual(build[-3:], ['-o', './bin/screen-stock', './cmd/screen'])
            image = next(call[2] for call in calls
                         if call[0] == 'docker' and call[2][0] == 'build')
            self.assertEqual(image[-3:], ['-f', './cmd/screen/Dockerfile', '.'])
            self.assertTrue((ROOT / 'config/config.yaml').is_file())
            self.assertTrue((ROOT / 'cmd/screen/Dockerfile.dockerignore').is_file())


class DeployTest(unittest.TestCase):
    def scenario(self, case, bind=None, port=None):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            (root / 'docker').write_text(DOCKER)
            (root / 'sleep').write_text('#!/bin/sh\nexit 0\n')
            for name in ('docker', 'sleep'):
                (root / name).chmod(0o755)
            env = dict(os.environ, PATH=directory + os.pathsep + os.environ['PATH'],
                       DEPLOY_TEST_ROOT=directory, DEPLOY_TEST_CASE=case)
            env.pop('BIND', None)
            env.pop('PORT', None)
            if bind is not None:
                env['BIND'] = bind
            if port is not None:
                env['PORT'] = port
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
        build = next(call for call in calls if call[0] == 'build')
        self.assertIn('--build-context', build)
        self.assertIn('tdx=' + str(ROOT / '../tdx'), build)
        self.assertFalse(any(call[0] == 'rm' for call in calls))
        run = next(call for call in calls if call[0] == 'run')
        self.assertIn(str(ROOT / 'data') + ':/app/data', run)
        self.assertIn('0.0.0.0:80:8765', run)
        self.assertIn('pull.mysql', run)

    def test_explicit_bind_is_preserved(self):
        code, calls = self.scenario('success', bind='192.168.1.10')
        self.assertEqual(code, 0)
        run = next(call for call in calls if call[0] == 'run')
        self.assertIn('192.168.1.10:80:8765', run)

    def test_explicit_port_is_preserved(self):
        code, calls = self.scenario('success', port='8765')
        self.assertEqual(code, 0)
        run = next(call for call in calls if call[0] == 'run')
        self.assertIn('0.0.0.0:8765:8765', run)

    def test_rename_failure_restarts_original_container(self):
        code, calls = self.scenario('rename-fail')
        self.assertNotEqual(code, 0)
        self.assertIn(['start', 'strategy-lab'], calls)
        self.assertFalse(any(call[0] == 'run' for call in calls))

    def test_start_or_health_failure_restores_old_container(self):
        for case in ('run-fail', 'health-fail', 'wrong-storage', 'mysql-unavailable'):
            with self.subTest(case=case):
                code, calls = self.scenario(case)
                self.assertNotEqual(code, 0)
                self.assertIn(['start', 'strategy-lab'], calls)
                self.assertTrue(any(call[0] == 'rename' and call[1].startswith('strategy-lab-previous-') and call[2] == 'strategy-lab' for call in calls))
                self.assertFalse(any(call[0] == 'rm' for call in calls))

if __name__ == '__main__':
    unittest.main()
