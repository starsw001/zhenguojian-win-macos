import argparse
import hashlib
import re
import shutil
import zipfile
import subprocess
import os
import uuid
from pathlib import Path

from launcher_build import build_launcher
from app_build import BuildVariant, add_variant_argument
from license_build import load_license_build, read_build_receipt, customer_distribution_id
from portable_bundle import initialize_portable_data

root = Path(__file__).resolve().parents[1]
parser = argparse.ArgumentParser()
parser.add_argument('--platform', choices=['android', 'windows'], required=True)
parser.add_argument('--abi', action='append', choices=['arm64-v8a', 'armeabi-v7a', 'x86_64'])
parser.add_argument('--output-dir', type=Path)
parser.add_argument('--archive', action='store_true', help='额外生成 Windows ZIP，默认只导出可运行目录')
parser.add_argument('--license-build-config', type=Path)
parser.add_argument('--license-build-receipt', type=Path)
parser.add_argument('--customer-file', type=Path)
parser.add_argument('--signing-key', type=Path)
add_variant_argument(parser)
options = parser.parse_args()
variant = BuildVariant(options.all_sources)
match = re.search(r'^version:\s*([\w.+-]+)\s*$', (root / 'pubspec.yaml').read_text(encoding='utf-8'), re.MULTILINE)
if not match:
    raise SystemExit('pubspec.yaml 缺少合法版本号。')
version = match.group(1)
output = options.output_dir.resolve() if options.output_dir else root / 'dist' / options.platform
output.mkdir(parents=True, exist_ok=True)
artifacts = []

if options.platform == 'android':
    for abi in options.abi or ['arm64-v8a', 'armeabi-v7a', 'x86_64']:
        source = root / 'build' / 'app' / 'outputs' / 'flutter-apk' / f'app-{abi}-release.apk'
        if not source.is_file():
            raise SystemExit('缺少 APK：' + str(source))
        with zipfile.ZipFile(source) as archive:
            names = set(archive.namelist())
            required = [f'lib/{abi}/{library}' for library in
                        ['libduanju_core.so', 'libflutter.so', 'libapp.so', 'libmpv.so', 'libffmpegkit.so']]
            missing = set(required) - names
            if missing:
                raise SystemExit('APK 缺少原生库：' + ', '.join(sorted(missing)))
        target = output / f'{variant.slug}-{version}-{abi}.apk'
        shutil.copy2(source, target)
        artifacts.append(target)
else:
    config = load_license_build(options.license_build_config)
    if options.customer_file is None or options.signing_key is None:
        raise SystemExit('Windows 授权包必须指定专属资料和管理员签名私钥。')
    for private in (options.customer_file, options.signing_key):
        if not private.is_file() or private.resolve().is_relative_to(root):
            raise SystemExit('管理员专属资料和私钥必须为源码目录之外的文件。')
    customer_distribution_id(options.customer_file)
    bundle = root / 'build' / 'windows' / 'x64' / 'runner' / 'Release'
    required = ['guo_window.dll', 'duanju_core.dll', 'flutter_windows.dll', 'libffmpegkit.dll',
                'libmpv-2.dll', 'msvcp140.dll', 'vcruntime140.dll',
                'data/icudtl.dat', 'data/app.so']
    missing = [name for name in required if not (bundle / name).is_file()]
    if missing:
        raise SystemExit('Windows 安装包缺少文件：' + ', '.join(missing))
    read_build_receipt(options.license_build_receipt, bundle / 'duanju_core.dll', variant.slug, version, config)
    target = output / f'{variant.slug}-{version}-windows-x64'
    if target.exists() or target.with_name(target.name + '.zip').exists():
        raise SystemExit('交付版本已存在，不覆盖可能含用户资料的目录；请先明确版本递增。')
    staging = output / ('.license-staging-' + uuid.uuid4().hex)
    staging.mkdir()
    for source in sorted(bundle.rglob('*')):
        if source.is_file():
            relative = source.relative_to(bundle)
            if relative.suffix.lower() == '.exe':
                continue
            if relative.parts[0] != 'data' and not (relative.parent == Path('.') and
                    (relative.suffix.lower() == '.dll' or relative.name == 'native_assets.json')):
                continue
            destination = staging / relative
            destination.parent.mkdir(parents=True, exist_ok=True)
            shutil.copy2(source, destination)
    build_launcher(root, staging / (variant.slug + '.exe'), variant, version, customer_distribution_id(options.customer_file), config['distributionPublicKey'])
    subprocess.run([shutil.which('go') or 'go', 'run', './cmd/license-admin', 'seal',
                    '--bundle', str(staging.resolve()), '--customer', str(options.customer_file.resolve()),
                    '--signing-key', str(options.signing_key.resolve()), '--edition', variant.slug,
                    '--version', version, '--expected-public-key', config['distributionPublicKey']],
                   cwd=root / 'native', env=os.environ.copy(), check=True)
    initialize_portable_data(staging)
    staging.rename(target)
    artifacts.append(target)
    if options.archive:
        published = output / 'releases' / ('tag' + version.split('+', 1)[0])
        subprocess.run(['go','run','./cmd/update-publish','--bundle',str(target),'--out',str(published),'--key-file',str(options.signing_key.resolve()),'--edition',variant.slug,'--public-key',config['distributionPublicKey'],'--distribution-id',customer_distribution_id(options.customer_file),'--version',version], cwd=root/'native',check=True)
        artifacts.extend([published/(target.name.replace('+','-build')+'.zip'),published/(variant.slug+'-windows-x64.update.json')])

checksums = []
for artifact in sorted(output.glob(f'*-{version}-*')):
    paths = sorted(path for path in artifact.rglob('*') if path.is_file()) if artifact.is_dir() else [artifact]
    for path in paths:
        digest = hashlib.sha256()
        with path.open('rb') as stream:
            for chunk in iter(lambda: stream.read(1024 * 1024), b''):
                digest.update(chunk)
        checksums.append(f'{digest.hexdigest()}  {path.relative_to(output).as_posix()}')
    print(artifact)
(output / 'SHA256SUMS.txt').write_text('\n'.join(checksums) + '\n', encoding='ascii')
