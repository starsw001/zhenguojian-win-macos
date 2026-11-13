import base64
import json
import os
import re
from pathlib import Path
import plistlib
import runpy
import shutil
import subprocess
import sys
import tempfile
import unittest
from unittest import mock

from app_build import BuildVariant, required_flutter_version, verify_flutter_version, write_app_version
from configure_ios_branding import configure


def dart_defines(*values):
    return ','.join(base64.b64encode(value.encode()).decode() for value in values)


class AppBuildTests(unittest.TestCase):
    def setUp(self):
        temporary = tempfile.TemporaryDirectory()
        self.addCleanup(temporary.cleanup)
        private = Path(temporary.name)
        public = base64.urlsafe_b64encode(bytes(range(32))).decode().rstrip('=')
        config = dict(protocol='guoapp-license-v1',
                      serverBaseURL='https://license-server.star-andy-jack.workers.dev',
                      distributionPublicKey=public, serverPublicKey=public,
                      activatePath='/api/guoapp/v1/activate-account',
                      verifyPath='/api/guoapp/v1/verify-account', eventPath='/api/guoapp/v1/events')
        (private / 'public.json').write_text(json.dumps(config), 'utf-8')
        (private / 'customer.json').write_text(json.dumps({'distributionId': 'fixture-customer'}), 'utf-8')
        (private / 'signing.key').write_text('isolated-test-tool-input-not-a-real-key', 'utf-8')
        self.license_arguments = ['--license-build-config', str(private / 'public.json'),
                                  '--customer-file', str(private / 'customer.json'),
                                  '--signing-key', str(private / 'signing.key')]

    def test_local_build_version_replaces_stale_generated_constants(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            (root / 'pubspec.yaml').write_text('version: 0.2.122+130\n', encoding='utf-8')
            (root / 'lib').mkdir()
            target = root / 'lib/app_version.g.dart'
            target.write_text("const appVersion = '0.2.100+108';\nconst appBuild = 108;\n", encoding='utf-8')
            self.assertEqual(write_app_version(root), '0.2.122+130')
            self.assertIn("const appVersion = '0.2.122+130';", target.read_text(encoding='utf-8'))
            self.assertIn('const appBuild = 130;', target.read_text(encoding='utf-8'))
            before = target.stat().st_mtime_ns
            write_app_version(root)
            self.assertEqual(target.stat().st_mtime_ns, before)
            (root / 'pubspec.yaml').write_text('version: invalid\n', encoding='utf-8')
            with self.assertRaises(ValueError):
                write_app_version(root)
            self.assertIn('0.2.122+130', target.read_text(encoding='utf-8'))

    def test_all_local_app_builders_generate_version_before_flutter_build(self):
        scripts = Path(__file__).resolve().parent
        for name in ['build_windows.py', 'build_android.py', 'build_macos.py', 'build_ios.py']:
            text = (scripts / name).read_text(encoding='utf-8')
            self.assertIn('write_app_version(root)', text)
            self.assertLess(text.index('write_app_version(root)'), text.index("[flutter, 'build'"))

    def test_omitted_or_false_flag_keeps_hongguo_only(self):
        for encoded in ['', dart_defines('ALL_SOURCES=false'), dart_defines('OTHER=true')]:
            variant = BuildVariant.from_dart_defines(encoded)
            self.assertFalse(variant.all_sources)
            self.assertEqual(variant.name, '红果赏')
            self.assertEqual(variant.slug, 'hongguoshang')

    def test_full_edition_decodes_among_other_flutter_defines(self):
        variant = BuildVariant.from_dart_defines(dart_defines(
            'OTHER=中文', 'ALL_SOURCES=true', 'VALUE=a=b'))
        self.assertTrue(variant.all_sources)
        self.assertEqual(variant.name, '真果赏')
        self.assertEqual(variant.slug, 'zhenguoshang')

    def test_ios_branding_can_switch_editions_without_replacing_bundle_identity(self):
        with tempfile.TemporaryDirectory() as temporary:
            path = Path(temporary) / 'Info.plist'
            original = {'CFBundleIdentifier': 'com.duanju.duanjuApp', 'CFBundleVersion': '9'}
            path.write_bytes(plistlib.dumps(original))
            for enabled in [True, False]:
                configure(path, dart_defines('ALL_SOURCES=' + str(enabled).lower()))
                actual = plistlib.loads(path.read_bytes())
                self.assertEqual(actual['CFBundleDisplayName'], '真果赏' if enabled else '红果赏')
                self.assertEqual(actual['CFBundleName'], 'zhenguoshang' if enabled else 'hongguoshang')
                for key, value in original.items():
                    self.assertEqual(actual[key], value)

    def test_windows_package_excludes_stale_executables_after_rename(self):
        scripts = Path(__file__).resolve().parent
        for enabled in [False, True]:
            with self.subTest(all_sources=enabled), tempfile.TemporaryDirectory() as temporary:
                root = Path(temporary)
                script = root / 'scripts/package_release.py'
                script.parent.mkdir()
                shutil.copy2(scripts / script.name, script)
                (root / 'pubspec.yaml').write_text('version: 0.2.87+95\n', encoding='utf-8')
                bundle = root / 'build/windows/x64/runner/Release'
                for name in ['zhenguoshang.exe', 'stale-brand.exe', 'stale.runtime.exe', 'dev.lib', 'data/extra.exe', 'guo_window.dll', 'duanju_core.dll',
                             'flutter_windows.dll', 'libffmpegkit.dll', 'libmpv-2.dll',
                             'msvcp140.dll', 'vcruntime140.dll', 'data/icudtl.dat', 'data/app.so']:
                    path = bundle / name
                    path.parent.mkdir(parents=True, exist_ok=True)
                    path.write_bytes(name.encode())
                args = [str(script), '--platform', 'windows', *self.license_arguments] + (['--all-sources'] if enabled else [])
                with mock.patch.object(sys, 'argv', args), \
                        mock.patch('launcher_build.build_launcher', side_effect=lambda root,target,*args: target.write_bytes(b'fixture-launcher')), \
                        mock.patch('license_build.read_build_receipt'), \
                        mock.patch('subprocess.run') as sealed:
                    runpy.run_path(str(script), run_name='__main__')
                variant = BuildVariant(enabled)
                result = root / f'dist/windows/{variant.slug}-0.2.87+95-windows-x64'
                self.assertEqual({p.name for p in result.rglob('*.exe')}, {variant.slug + '.exe'})
                self.assertEqual((result / 'guo_window.dll').read_bytes(), b'guo_window.dll')
                self.assertFalse((result / 'dev.lib').exists())
                self.assertEqual((result / (variant.slug + '.exe')).read_bytes(), b'fixture-launcher')
                self.assertFalse(list((root / 'dist/windows').glob('*.zip')))
                self.assertFalse((result / 'github-update-url.txt').exists())
                self.assertIn('seal', sealed.call_args.args[0])
                self.assertFalse((root / 'dist/windows/fixture-customer').exists())

    @unittest.skipUnless(shutil.which('cmake'), 'CMake is unavailable')
    def test_windows_reads_defines_from_flutter_tool_environment(self):
        branding = Path(__file__).resolve().parents[1] / 'windows/runner/app_branding.cmake'
        for flags, expected in [
            ([], '红果赏'),
            (['ALL_SOURCES=true'], '真果赏'),
            (['OTHER=true', 'ALL_SOURCES=false'], '红果赏'),
            (['ALL_SOURCES=true', 'ALL_SOURCES=false'], '红果赏'),
        ]:
            with self.subTest(flags=flags), tempfile.TemporaryDirectory() as temporary:
                script = Path(temporary) / 'check.cmake'
                result = Path(temporary) / 'name.txt'
                script.write_text(
                    'list(APPEND FLUTTER_TOOL_ENVIRONMENT "OTHER=1" "DART_DEFINES=' + dart_defines(*flags) + '")\n'
                    'include("' + branding.as_posix() + '")\n'
                    'file(WRITE "' + result.as_posix() + '" "${APP_DISPLAY_NAME}")\n',
                    encoding='utf-8',
                )
                subprocess.run(['cmake', '-P', str(script)], check=True, capture_output=True)
                self.assertEqual(result.read_text(encoding='utf-8'), expected)

    def test_android_and_windows_propagate_one_edition_to_core_flutter_and_package(self):
        root = Path(__file__).resolve().parent
        for target in ['android', 'windows']:
            for enabled in [False, True]:
                with self.subTest(target=target, all_sources=enabled):
                    script = root / f'build_{target}.py'
                    arguments = [str(script)] + (['--all-sources'] if enabled else [])
                    if target == 'windows':
                        arguments += self.license_arguments
                    with mock.patch.object(sys, 'argv', arguments), \
                            mock.patch.dict(os.environ, {'PATH': '/tools'}, clear=True), \
                            mock.patch('platform.system', return_value='Windows'), \
                            mock.patch('shutil.which', return_value='/tools/flutter'), \
                            mock.patch('app_build.verify_flutter_version'), \
                            mock.patch('subprocess.run') as run:
                        runpy.run_path(str(script), run_name='__main__')
                    calls = [call.args[0] for call in run.call_args_list]
                    native = next(call for call in calls if any(str(arg).endswith('build_native.py') for arg in call))
                    flutter = next(call for call in calls if 'build' in call)
                    package = next(call for call in calls if any(str(arg).endswith('package_release.py') for arg in call))
                    self.assertEqual('--all-sources' in native, enabled)
                    self.assertEqual('--all-sources' in package, enabled)
                    self.assertIn('--dart-define=ALL_SOURCES=' + str(enabled).lower(), flutter)
                    self.assertIn('core.buildAllSources=' + str(enabled).lower(), BuildVariant(enabled).linker_flags)

    def test_sdk_pin_accepts_plain_and_quoted_versions(self):
        for value in ['3.47.4', "'3.47.4'", '"3.47.4"']:
            with self.subTest(value=value), tempfile.TemporaryDirectory() as temporary:
                root = Path(temporary)
                (root / 'pubspec.yaml').write_text(
                    'environment:\n  sdk: ">=3.12.0 <4.0.0"\n  flutter: ' + value + '\n',
                    encoding='utf-8',
                )
                self.assertEqual(required_flutter_version(root), '3.47.4')
                result = subprocess.CompletedProcess([], 0,
                                                     json.dumps({'frameworkVersion': '3.47.4'}), '')
                with mock.patch('subprocess.run', return_value=result) as run:
                    verify_flutter_version(root, '/tools/flutter', {'PATH': '/tools'})
                self.assertEqual(run.call_count, 1)
                self.assertIn('--no-version-check', run.call_args.args[0])

    def test_sdk_mismatch_stops_without_installing_or_upgrading(self):
        root = Path(__file__).resolve().parents[1]
        result = subprocess.CompletedProcess([], 0,
                                             json.dumps({'frameworkVersion': '3.50.0'}), '')
        with mock.patch('subprocess.run', return_value=result) as run:
            with self.assertRaisesRegex(SystemExit, '请手动切换 SDK'):
                verify_flutter_version(root, '/tools/flutter', {})
        self.assertEqual(run.call_count, 1)
        self.assertNotIn('upgrade', run.call_args.args[0])

    def test_windows_run_is_after_packaging_and_supports_image_free_verification(self):
        script = Path(__file__).resolve().parent / 'build_windows.py'
        order = []

        def execute(command, **kwargs):
            self.assertEqual(kwargs['env']['GOTOOLCHAIN'], 'local')
            self.assertEqual(kwargs['env']['HTTPS_PROXY'], 'http://proxy.example:8080')
            order.append(command)

        def launch(command, **kwargs):
            order.append(command)
            return mock.Mock(pid=12345)

        with mock.patch.object(sys, 'argv', [str(script), '--run', '--disable-remote-images', *self.license_arguments]), \
                mock.patch.dict(os.environ, {'PATH': '/tools', 'HTTP_PROXY': 'http://proxy.example:8080'}, clear=True), \
                mock.patch('shutil.which', return_value='/tools/flutter'), \
                mock.patch('app_build.verify_flutter_version'), \
                mock.patch('subprocess.run', side_effect=execute), \
                mock.patch('subprocess.Popen', side_effect=launch):
            runpy.run_path(str(script), run_name='__main__')
        self.assertTrue(any(str(arg).endswith('package_release.py') for arg in order[-2]))
        manifest = (script.parents[1] / 'pubspec.yaml').read_text(encoding='utf-8')
        version = re.search(r'^version:\s*(\S+)', manifest, re.MULTILINE).group(1)
        self.assertEqual(order[-1], [str(script.parents[1] / f'dist/windows/hongguoshang-{version}-windows-x64/hongguoshang.exe')])
        build = next(command for command in order if 'build' in command)
        self.assertIn('--dart-define=DISABLE_REMOTE_IMAGES=true', build)


if __name__ == '__main__':
    unittest.main()
