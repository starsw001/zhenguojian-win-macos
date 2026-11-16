import argparse
import json
import re
import subprocess
import sys
from pathlib import Path

root = Path(__file__).resolve().parents[1]
admin = root.parent / 'license-cloudflare/license-server'
editions = ('hongguoshang', 'zhenguoshang')
pattern = r'(0|[1-9]\d{0,8})\.(0|[1-9]\d{0,8})\.(0|[1-9]\d{0,8})\+([1-9]\d{0,8})'


def next_version(previous):
    match = re.fullmatch(pattern, previous)
    if not match:
        raise ValueError('请输入完整的当前版本，例如 1.1.29+160。')
    major, minor, patch, build = map(int, match.groups())
    v = f'{major}.{minor}.{patch + 1}+{build + 1}'
    if not re.fullmatch(pattern, v):
        raise ValueError('版本超出支持范围。')
    return v


def release_tag(version):
    if not re.fullmatch(pattern, version):
        raise ValueError('版本号无效。')
    return 'tag' + version.split('+')[0]


def main():
    parser = argparse.ArgumentParser(description='构建或从干净的 Windows 签名目录生成四个更新资产，不自动上传。')
    parser.add_argument('--signing-key', type=Path, default=admin/'distribution-signing.key')
    parser.add_argument('--public-config', type=Path, default=admin/'config/production-public.json')
    parser.add_argument('--distribution', type=Path, default=admin/'config/universal-distribution.json')
    parser.add_argument('--bundle-root', type=Path, default=root/'dist/windows')
    parser.add_argument('--edition', choices=[*editions, 'both'], default='both')
    parser.add_argument('--next-from', metavar='VERSION', help='仅当源码与指定版本相同，将修订号和构建号各加一，构建并打包两款软件。')
    parser.add_argument('--cn-mirrors', action='store_true')
    args = parser.parse_args()
    pubspec = root/'pubspec.yaml'
    raw = pubspec.read_bytes()
    m = re.search(rb'(?m)^version:[ \t]*(\d+\.\d+\.\d+\+\d+)[ \t]*\r?$', raw)
    if not m:
        raise SystemExit('无法读取版本号。')
    version = m[1].decode('ascii')
    bundles = args.bundle_root.resolve()
    public = json.loads(args.public_config.read_text('utf8'))['distributionPublicKey']
    distribution = json.loads(args.distribution.read_text('utf8'))['distributionId']
    if not args.signing_key.is_file() or args.signing_key.resolve().is_relative_to(root):
        raise SystemExit('缺少独立保存的分发私钥。')
    if args.next_from:
        if args.edition != 'both' or args.next_from != version:
            raise SystemExit('原版本或两版构建范围不匹配；未递增版本。')
        version = next_version(version)
        if (root/'dist/windows/releases'/release_tag(version)).exists() or any(
            (bundles/(ed+'-'+version+'-windows-x64')).exists() for ed in editions
        ):
            raise SystemExit('新版本目录已经存在，不覆盖、不再递增。')
        pubspec.write_bytes(raw[:m.start(1)] + version.encode('ascii') + raw[m.end(1):])
        print(f'仅递增一次至 {version}，失败后请按此版本续做，不要再次递增。', flush=True)
        subprocess.run([sys.executable, '-B', root/'scripts/refresh_source_publications.py'], cwd=root, check=True)
        for ed in editions:
            cmd = [sys.executable, '-B', root/'scripts/build_windows.py', '--keep-publication-addresses',
                   '--license-build-config', args.public_config.resolve(), '--customer-file', args.distribution.resolve(),
                   '--signing-key', args.signing_key.resolve(), '--output-dir', bundles]
            if args.cn_mirrors:
                cmd.append('--cn-mirrors')
            if ed == 'zhenguoshang':
                cmd.append('--all-sources')
            subprocess.run(cmd, cwd=root, check=True)
    out = root/'dist/windows/releases'/release_tag(version)
    for ed in editions if args.edition == 'both' else [args.edition]:
        subprocess.run(['go','run','./cmd/update-publish','--bundle',bundles/(ed+'-'+version+'-windows-x64'),
                        '--out',out,'--key-file',args.signing_key.resolve(),'--public-key',public,
                        '--edition',ed,'--distribution-id',distribution,'--version',version], cwd=root/'native',check=True)
    print(out)


if __name__ == '__main__':
    main()
