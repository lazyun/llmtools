"""Exercise platform packaging and failure behavior without foreign toolchains."""
import json
import os
import pathlib
import shutil
import subprocess
import tempfile
import unittest

ROOT = pathlib.Path(__file__).resolve().parents[1]
STUB = r'''#!/usr/bin/env python3
import json,os,pathlib,shutil,sys
name=pathlib.Path(sys.argv[0]).name
a=sys.argv[1:]
root=pathlib.Path(os.environ['BUILD_FIXTURE_ROOT'])
host=os.environ['BUILD_FIXTURE_HOST']
with open(os.environ['BUILD_FIXTURE_LOG'],'a') as f:
 f.write(json.dumps({'name':name,'args':a,'cgo':os.getenv('CGO_ENABLED'),'goos':os.getenv('GOOS'),'goarch':os.getenv('GOARCH'),'cwd':os.getcwd()})+'\n')
if name=='go':
 if a[0]=='env':
  print({'GOHOSTOS':host,'GOHOSTARCH':'amd64','GOVERSION':'go1.26.2','GOPATH':str(root/'go home')}[a[1]])
 elif a[0]=='build':
  if os.getenv('CGO_ENABLED')!='1':sys.exit('SQLite CGO was disabled')
  p=pathlib.Path(a[a.index('-o')+1]);p.parent.mkdir(parents=True,exist_ok=True);p.write_text('MCP with SQLite');p.chmod(0o755)
elif name=='wails':
 if a==['version']:print(os.getenv('BUILD_FIXTURE_WAILS_VERSION','v2.10.2')+'\nSponsor message')
 elif a[0]=='build':
  d=root/'desktop/build/bin'
  if d.exists():shutil.rmtree(d)
  p=d/'AI Worker.app/Contents/MacOS/AI Worker' if host=='darwin' else d/('AI Worker.exe' if host=='windows' else 'AI Worker')
  p.parent.mkdir(parents=True,exist_ok=True);p.write_text('Desktop');p.chmod(0o755)
elif name=='npm':
 if a[-1]=='ci':(root/'desktop/frontend/node_modules').mkdir(parents=True,exist_ok=True)
 if a[-2:]==['run','build'] and os.getenv('BUILD_FIXTURE_FAIL')=='frontend':sys.exit(9)
elif name=='pkg-config':
 if a[-1]=='gtk+-3.0':sys.exit(0 if os.getenv('BUILD_FIXTURE_GTK','1')=='1' else 1)
 if a[-1]=='webkit2gtk-4.1':sys.exit(0 if os.getenv('BUILD_FIXTURE_WEBKIT','4.1')=='4.1' else 1)
 if a[-1]=='webkit2gtk-4.0':sys.exit(0 if os.getenv('BUILD_FIXTURE_WEBKIT','4.1')=='4.0' else 1)
elif name=='ditto':shutil.copytree(a[0],a[1],dirs_exist_ok=True)
'''


class BuildScripts(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory(prefix='llmtools-build-tests-')
        self.addCleanup(self.temp.cleanup)
        self.root = pathlib.Path(self.temp.name) / 'repo with spaces'
        self.bin = pathlib.Path(self.temp.name) / 'tools'
        self.bin.mkdir()
        (self.root / 'scripts').mkdir(parents=True)
        for name in ['common', 'macos', 'linux', 'windows']:
            shutil.copy2(ROOT / f'scripts/build-{name}.sh', self.root / f'scripts/build-{name}.sh')
        (self.root / 'desktop/frontend').mkdir(parents=True)
        (self.root / 'desktop/go.mod').write_text('module fixture\nrequire github.com/wailsapp/wails/v2 v2.10.2\n')
        for name in ['go', 'node', 'npm', 'wails', 'pkg-config', 'codesign', 'ditto', 'xcode-select']:
            path = self.bin / name
            path.write_text(STUB)
            path.chmod(0o755)
        self.log = pathlib.Path(self.temp.name) / 'calls.jsonl'

    def run_build(self, platform, *args, **extra):
        env = os.environ.copy()
        env.pop('WAILS', None)
        env.update(PATH=str(self.bin) + os.pathsep + env['PATH'],
                   BUILD_FIXTURE_ROOT=str(self.root), BUILD_FIXTURE_LOG=str(self.log),
                   BUILD_FIXTURE_HOST={'macos': 'darwin'}.get(platform, platform), CGO_ENABLED='0')
        env.update(extra)
        return subprocess.run(['bash', str(self.root / f'scripts/build-{platform}.sh'), *args],
                              cwd=self.temp.name, env=env, text=True, capture_output=True, timeout=20)

    def calls(self):
        return [json.loads(line) for line in self.log.read_text().splitlines()] if self.log.exists() else []

    def test_full_build_packages_desktop_and_cgo_mcp_on_every_platform(self):
        for platform in ['macos', 'linux', 'windows']:
            with self.subTest(platform=platform):
                result = self.run_build(platform)
                self.assertEqual(0, result.returncode, result.stdout + result.stderr)
                out = self.root / f'dist/{platform}-amd64'
                if platform == 'macos':
                    self.assertEqual('MCP with SQLite', (out / 'AI Worker.app/Contents/MacOS/ai-worker-mcp').read_text())
                    self.assertTrue((out / 'AI Worker.app/Contents/MacOS/AI Worker').is_file())
                else:
                    suffix = '.exe' if platform == 'windows' else ''
                    self.assertEqual('MCP with SQLite', (out / f'ai-worker-mcp{suffix}').read_text())
                    self.assertTrue((out / f'AI Worker{suffix}').is_file())
                builds = [c for c in self.calls() if c['name'] == 'go' and c['args'][0] == 'build']
                self.assertTrue(all(c['cgo'] == '1' for c in builds))

    def test_mcp_only_needs_no_frontend_or_wails(self):
        result = self.run_build('windows', '--mcp-only')
        self.assertEqual(0, result.returncode, result.stderr)
        self.assertEqual('MCP with SQLite', (self.root / 'dist/windows-amd64/ai-worker-mcp.exe').read_text())
        self.assertFalse(any(c['name'] in ['npm', 'node', 'wails', 'pkg-config'] for c in self.calls()))

    def test_wrong_native_go_host_fails_before_building(self):
        result = self.run_build('windows', BUILD_FIXTURE_HOST='linux')
        self.assertNotEqual(0, result.returncode)
        self.assertIn('native windows Go', result.stderr)
        self.assertFalse((self.root / 'dist').exists())

    def test_inherited_cross_compile_environment_cannot_mismatch_packaged_targets(self):
        result = self.run_build('macos', GOOS='windows', GOARCH='arm64')
        self.assertEqual(0, result.returncode, result.stderr)
        compiler_calls = [c for c in self.calls() if (c['name'] == 'go' and c['args'][0] == 'build') or (c['name'] == 'wails' and c['args'][0] != 'version')]
        self.assertTrue(compiler_calls)
        self.assertTrue(all((c['goos'], c['goarch'], c['cgo']) == ('darwin', 'amd64', '1') for c in compiler_calls))

    def test_linux_detects_webkit_41_and_40_and_rejects_missing_gtk(self):
        for version in ['4.1', '4.0']:
            with self.subTest(version=version):
                self.log.unlink(missing_ok=True)
                result = self.run_build('linux', BUILD_FIXTURE_WEBKIT=version)
                self.assertEqual(0, result.returncode, result.stderr)
                wails = [c for c in self.calls() if c['name'] == 'wails' and c['args'][0] != 'version']
                self.assertTrue(all(('webkit2_41' in c['args']) == (version == '4.1') for c in wails))
        self.log.unlink(missing_ok=True)
        result = self.run_build('linux', BUILD_FIXTURE_GTK='0')
        self.assertNotEqual(0, result.returncode)
        self.assertIn('GTK 3', result.stderr)
        self.assertFalse(any(c['args'][0] == 'build' for c in self.calls()))

    def test_frontend_failure_cannot_report_a_successful_package(self):
        result = self.run_build('macos', BUILD_FIXTURE_FAIL='frontend')
        self.assertEqual(9, result.returncode)
        self.assertIn('frontend', result.stderr)
        self.assertNotIn('Build complete', result.stdout)
        self.assertFalse(any(c['name'] == 'go' and c['args'][0] == 'build' for c in self.calls()))

    def test_wails_version_must_match_the_desktop_module(self):
        result = self.run_build('macos', BUILD_FIXTURE_WAILS_VERSION='v2.9.0')
        self.assertNotEqual(0, result.returncode)
        self.assertIn('v2.10.2 required', result.stderr)

    def test_skip_install_requires_and_preserves_cached_modules(self):
        first = self.run_build('macos', '--skip-install')
        self.assertNotEqual(0, first.returncode)
        modules = self.root / 'desktop/frontend/node_modules'
        modules.mkdir()
        second = self.run_build('macos', '--skip-install')
        self.assertEqual(0, second.returncode, second.stderr)
        self.assertFalse(any(c['name'] == 'npm' and c['args'][-1] == 'ci' for c in self.calls()))

    def test_help_and_unknown_options(self):
        for platform in ['macos', 'linux', 'windows']:
            result = self.run_build(platform, '--help')
            self.assertEqual(0, result.returncode)
            self.assertIn('--mcp-only', result.stdout)
        self.assertEqual([], self.calls())
        self.assertNotEqual(0, self.run_build('macos', '--unknown').returncode)


if __name__ == '__main__':
    unittest.main()
