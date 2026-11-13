import argparse
import os
import re
import shutil
import subprocess
import sys
import uuid
from pathlib import Path

from build_mirrors import china_mirror_environment, mirrored_pub_lockfile
from app_build import BuildVariant, add_variant_argument, verify_flutter_version, write_app_version
from license_build import load_license_build, customer_distribution_id

root = Path(__file__).resolve().parents[1]
parser = argparse.ArgumentParser()
parser.add_argument('--cn-mirrors', action='store_true', help='使用 Flutter 中国镜像')
parser.add_argument('--run', action='store_true', help='构建并导出目录成功后启动 Windows 客户端')
parser.add_argument('--output-dir', type=Path)
parser.add_argument('--archive', action='store_true', help='额外生成 ZIP，默认仅导出可直接运行的目录')
parser.add_argument('--disable-remote-images', action='store_true', help='开发验证时禁用站源图片请求')
parser.add_argument('--keep-publication-addresses', action='store_true', help='保留仓库现有黄果地址，不请求黄果发布页')
parser.add_argument('--license-build-config', type=Path, required=True)
parser.add_argument('--customer-file', type=Path, required=True)
parser.add_argument('--signing-key', type=Path, required=True)
add_variant_argument(parser)
options = parser.parse_args()
variant = BuildVariant(options.all_sources)
load_license_build(options.license_build_config)
if not options.customer_file.is_file() or not options.signing_key.is_file():
    raise SystemExit('缺少管理员专属资料或分发签名私钥；不会构建无授权分发包。')
for private in (options.customer_file, options.signing_key):
    if private.resolve().is_relative_to(root):
        raise SystemExit('专属资料和签名私钥必须放在源码目录之外。')
distribution_id = customer_distribution_id(options.customer_file)
flutter_arguments = variant.flutter_arguments
if options.disable_remote_images:
    flutter_arguments.append('--dart-define=DISABLE_REMOTE_IMAGES=true')
environment = os.environ.copy()
environment['CATALOG_CACHE_EPOCH'] = uuid.uuid4().hex
license_work = Path(os.environ.get('GUO_LICENSE_BUILD_SPACE', r'D:\sdk\guoapp-license-builds')) / environment['CATALOG_CACHE_EPOCH']
if license_work.resolve().is_relative_to(root):
    raise SystemExit('授权构建收据和调试符号必须保存在源码目录之外。')
license_work.mkdir(parents=True, exist_ok=False)
license_receipt = license_work / 'native-build.json'
license_arguments = ['--license-build-config', str(options.license_build_config.resolve()),
                     '--license-build-receipt', str(license_receipt)]
flutter_arguments += ['--obfuscate', '--split-debug-info=' + str(license_work / 'symbols')]
environment['GOTOOLCHAIN'] = 'local'
if environment.get('HTTP_PROXY'):
    environment.setdefault('HTTPS_PROXY', environment['HTTP_PROXY'])
environment.setdefault('GOPROXY', 'https://goproxy.cn,direct')
environment.setdefault('GOSUMDB', 'off')
flutter = shutil.which('flutter')
if not flutter:
    raise SystemExit('请先将 Flutter SDK 的 bin 目录加入 PATH。')
with china_mirror_environment(environment, options.cn_mirrors, gradle=False) as env:
    verify_flutter_version(root, flutter, env)
    write_app_version(root)
    with mirrored_pub_lockfile(root, env):
        if options.all_sources and not options.keep_publication_addresses:
            subprocess.run([sys.executable, str(root / 'scripts' / 'refresh_source_publications.py')],
                           cwd=root, env=env, check=True)
        subprocess.run([sys.executable, str(root / 'scripts' / 'build_native.py'), '--platform', 'windows', *license_arguments, *variant.arguments],
                       cwd=root, env=env, check=True)
        subprocess.run([flutter, 'pub', 'get', '--enforce-lockfile'], cwd=root, env=env, check=True)
        subprocess.run([flutter, 'build', 'windows', '--release', '--no-pub', *flutter_arguments], cwd=root, env=env, check=True)
        package_arguments = ['--archive'] if options.archive else []
        if options.output_dir:
            package_arguments += ['--output-dir', str(options.output_dir.resolve())]
        subprocess.run([sys.executable, str(root / 'scripts' / 'package_release.py'), '--platform', 'windows',
                        *license_arguments, '--customer-file', str(options.customer_file.resolve()),
                        '--signing-key', str(options.signing_key.resolve()),
                        *package_arguments, *variant.arguments], cwd=root, env=env, check=True)

if options.run:
    version = re.search(r'^version:\s*([\w.+-]+)\s*$', (root / 'pubspec.yaml').read_text(encoding='utf-8'), re.MULTILINE).group(1)
    executable = (options.output_dir or root / 'dist' / 'windows').resolve() / f'{variant.slug}-{version}-windows-x64' / (variant.slug + '.exe')
    process = subprocess.Popen(
        [str(executable)], cwd=executable.parent, env=environment,
        stdin=subprocess.DEVNULL, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL,
    )
    print(f'Windows 客户端已启动（PID {process.pid}）：{executable}', flush=True)
