import argparse
import os
import platform
import shutil
import subprocess
import uuid
from pathlib import Path

from app_build import BuildVariant, add_variant_argument, write_app_version
from license_build import load_license_build, license_linker_flags, file_sha256
import json

root = Path(__file__).resolve().parents[1]
parser = argparse.ArgumentParser()
parser.add_argument('--platform', choices=['android', 'windows', 'darwin'], required=True)
parser.add_argument('--abi', action='append', choices=['arm64-v8a', 'armeabi-v7a', 'x86_64'])
parser.add_argument('--license-build-config', type=Path)
parser.add_argument('--license-build-receipt', type=Path)
add_variant_argument(parser)
options = parser.parse_args()
variant = BuildVariant(options.all_sources)
license_config = load_license_build(options.license_build_config) if options.platform == 'windows' else None
app_version = write_app_version(root)
if options.platform == 'windows' and options.license_build_receipt is None:
    raise SystemExit('Windows 授权构建必须指定独立 SDK 空间的 --license-build-receipt。')
catalog_epoch = os.environ.get('CATALOG_CACHE_EPOCH') or uuid.uuid4().hex
if not catalog_epoch.isascii() or not catalog_epoch.replace('-', '').replace('_', '').isalnum() or len(catalog_epoch) > 128:
    raise SystemExit('CATALOG_CACHE_EPOCH 必须为不超过 128 个字符的 ASCII 字母、数字、连字符或下划线。')

environment = os.environ.copy()
environment.setdefault('GOPROXY', 'https://goproxy.cn,direct')
environment.setdefault('GOSUMDB', 'off')
environment['CGO_ENABLED'] = '1'
go = shutil.which('go')
if not go:
    raise SystemExit('请先安装 Go 1.24.1 或更新版本。')
bootstrap_env = environment.copy()
bootstrap_env['GOSUMDB'] = os.environ.get('GOSUMDB', 'sum.golang.org')
if bootstrap_env['GOSUMDB'] == 'off':
    bootstrap_env['GOSUMDB'] = 'sum.golang.org'
toolchain_root = subprocess.check_output([go, 'env', 'GOROOT'], cwd=root / 'native',
    env=bootstrap_env, text=True).strip()
go = str(Path(toolchain_root) / 'bin' / ('go.exe' if platform.system() == 'Windows' else 'go'))

def build(goos, architecture, compiler, output, extra=None):
    output.parent.mkdir(parents=True, exist_ok=True)
    build_env = environment.copy()
    build_env.update(GOOS=goos, GOARCH=architecture, CC=str(compiler))
    if extra:
        build_env.update(extra)
    print('Building ' + str(output.relative_to(root)), flush=True)
    flags = variant.linker_flags + ' -X duanjuapp/native/core.buildCatalogEpoch=' + catalog_epoch
    if license_config is not None:
        flags += ' ' + license_linker_flags(license_config, app_version)
    subprocess.run([go, 'build', '-trimpath', '-buildmode=c-shared',
                    '-ldflags=' + flags,
                    '-o', str(output), './bridge'],
                   cwd=root / 'native', env=build_env, check=True)
    if license_config is not None:
        receipt = options.license_build_receipt.resolve()
        if receipt.is_relative_to(root):
            raise SystemExit('授权构建收据必须位于源码目录之外。')
        receipt.parent.mkdir(parents=True, exist_ok=True)
        receipt.write_text(json.dumps({'edition': variant.slug, 'version': app_version,
            'licenseRequired': True, 'licenseConfig': license_config,
            'nativeSHA256': file_sha256(output)}, ensure_ascii=False, indent=2), 'utf-8')

if options.platform == 'android':
    sdk = os.environ.get('ANDROID_HOME') or os.environ.get('ANDROID_SDK_ROOT')
    if not sdk:
        raise SystemExit('请设置 ANDROID_HOME 为 Android SDK 目录。')
    ndk = Path(os.environ.get('ANDROID_NDK_HOME', Path(sdk) / 'ndk' / '28.2.13676358'))
    host = {'Darwin': 'darwin-x86_64', 'Linux': 'linux-x86_64', 'Windows': 'windows-x86_64'}[platform.system()]
    compilers = ndk / 'toolchains' / 'llvm' / 'prebuilt' / host / 'bin'
    mappings = {
        'arm64-v8a': ('arm64', 'aarch64-linux-android26-clang'),
        'armeabi-v7a': ('arm', 'armv7a-linux-androideabi26-clang'),
        'x86_64': ('amd64', 'x86_64-linux-android26-clang'),
    }
    for abi in options.abi or list(mappings):
        architecture, name = mappings[abi]
        compiler = compilers / (name + ('.cmd' if platform.system() == 'Windows' else ''))
        if not compiler.exists():
            raise SystemExit('缺少 Android NDK 编译器：' + str(compiler))
        output = root / 'android' / 'app' / 'src' / 'main' / 'jniLibs' / abi / 'libduanju_core.so'
        extra = {'CGO_LDFLAGS': '-Wl,-z,max-page-size=16384'}
        if architecture == 'arm':
            extra['GOARM'] = '7'
        build('android', architecture, compiler, output, extra)
elif options.platform == 'windows':
    compiler = shutil.which('x86_64-w64-mingw32-gcc') or (shutil.which('gcc') if platform.system() == 'Windows' else None)
    if not compiler:
        raise SystemExit('请安装 MinGW-w64，并将其 bin 目录加入 PATH。')
    build('windows', 'amd64', compiler, root / 'windows' / 'runner' / 'duanju_core.dll',
          {'CGO_LDFLAGS': '-static-libgcc'})
else:
    compiler = shutil.which('clang')
    if not compiler:
        raise SystemExit('需要安装 Xcode Command Line Tools。')
    build('darwin', 'arm64' if platform.machine() == 'arm64' else 'amd64', compiler,
          root / 'native' / 'build' / 'darwin' / 'libduanju_core.dylib')
