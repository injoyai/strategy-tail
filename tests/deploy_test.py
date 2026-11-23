"""使用替身 Docker 检查部署流程，不操作真实容器。"""
import json
import os
from pathlib import Path
import shutil
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
elif args[0]=='build':
 if scenario=='build-fail': sys.exit(1)
 if scenario!='missing-image-id': Path(args[args.index('--iidfile')+1]).write_text('sha256:'+'a'*64)
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


def init_repo(path):
    """建立最小 Git 仓库并返回 HEAD，供 deploy.sh 读取提交号。"""
    path.mkdir(parents=True, exist_ok=True)
    (path / '.keep').write_text('')
    subprocess.run(['git', 'init', '-q', str(path)], check=True, capture_output=True)
    subprocess.run(['git', '-C', str(path), 'add', '-A'], check=True, capture_output=True)
    subprocess.run(
        ['git', '-C', str(path), '-c', 'user.email=test@example.com',
         '-c', 'user.name=test', 'commit', '-qm', 'init'],
        check=True, capture_output=True,
    )
    return subprocess.run(
        ['git', '-C', str(path), 'rev-parse', 'HEAD'],
        check=True, capture_output=True, text=True,
    ).stdout.strip()


class DeployTest(unittest.TestCase):
    def scenario(self, case, bind=None, port=None, sdk='ok'):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            app = root / 'app'
            app.mkdir()
            shutil.copy2(ROOT / 'deploy.sh', app / 'deploy.sh')
            if sdk == 'missing':
                sdk_rev = '0' * 40
            else:
                actual = init_repo(root / 'tdx')
                sdk_rev = 'a' * 40 if sdk == 'mismatch' else actual
            (app / 'tdx.rev').write_text(sdk_rev + '\n')
            init_repo(app)
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
            result = subprocess.run(['bash', str(app / 'deploy.sh')], env=env,
                                    capture_output=True, text=True, timeout=10)
            calls = []
            if (root / 'calls').exists():
                calls = [json.loads(line) for line in (root / 'calls').read_text().splitlines()]
            return result.returncode, calls, result.stderr, root

    def test_busy_or_build_failure_keeps_old_container(self):
        for case in ('busy', 'busy-after-build', 'build-fail', 'missing-image-id'):
            with self.subTest(case=case):
                code, calls, _, _ = self.scenario(case)
                self.assertNotEqual(code, 0)
                self.assertFalse(any(call[0] in ('stop', 'rename', 'run') for call in calls))

    def test_success_keeps_backup(self):
        code, calls, _, root = self.scenario('success')
        self.assertEqual(code, 0)
        self.assertEqual(sum(call[0] == 'rename' for call in calls), 1)
        build = next(call for call in calls if call[0] == 'build')
        self.assertIn('--build-context', build)
        self.assertTrue(any(arg.startswith('tdx=') and
                            os.path.normpath(arg[4:]) == os.path.normpath(str(root / 'tdx'))
                            for arg in build))
        self.assertTrue(any(arg.startswith('APP_REVISION=') for arg in build))
        self.assertTrue(any(arg.startswith('TDX_REVISION=') for arg in build))
        self.assertFalse(any(call[0] == 'rm' for call in calls))
        run = next(call for call in calls if call[0] == 'run')
        self.assertIn(str(root / 'app' / 'data') + ':/app/data', run)
        self.assertIn('0.0.0.0:80:8765', run)
        self.assertIn('pull.mysql', run)
        self.assertEqual(run[-1], 'sha256:' + 'a' * 64)

    def test_explicit_bind_is_preserved(self):
        code, calls, _, _ = self.scenario('success', bind='192.168.1.10')
        self.assertEqual(code, 0)
        run = next(call for call in calls if call[0] == 'run')
        self.assertIn('192.168.1.10:80:8765', run)

    def test_explicit_port_is_preserved(self):
        code, calls, _, _ = self.scenario('success', port='8765')
        self.assertEqual(code, 0)
        run = next(call for call in calls if call[0] == 'run')
        self.assertIn('0.0.0.0:8765:8765', run)

    def test_rename_failure_restarts_original_container(self):
        code, calls, _, _ = self.scenario('rename-fail')
        self.assertNotEqual(code, 0)
        self.assertIn(['start', 'strategy-lab'], calls)
        self.assertFalse(any(call[0] == 'run' for call in calls))

    def test_start_or_health_failure_restores_old_container(self):
        for case in ('run-fail', 'health-fail', 'wrong-storage', 'mysql-unavailable'):
            with self.subTest(case=case):
                code, calls, _, _ = self.scenario(case)
                self.assertNotEqual(code, 0)
                self.assertIn(['start', 'strategy-lab'], calls)
                self.assertTrue(any(call[0] == 'rename' and call[1].startswith('strategy-lab-previous-') and call[2] == 'strategy-lab' for call in calls))
                self.assertFalse(any(call[0] == 'rm' for call in calls))

    def test_missing_or_mismatched_sdk_stops_before_build(self):
        for sdk in ('missing', 'mismatch'):
            with self.subTest(sdk=sdk):
                code, calls, stderr, _ = self.scenario('success', sdk=sdk)
                self.assertNotEqual(code, 0)
                self.assertFalse(any(call[0] in ('build', 'stop', 'rename', 'run') for call in calls))
                self.assertIn('相邻 SDK', stderr)
                if sdk == 'mismatch':
                    self.assertIn('期望', stderr)
                    self.assertIn('实际', stderr)


if __name__ == '__main__':
    unittest.main()
