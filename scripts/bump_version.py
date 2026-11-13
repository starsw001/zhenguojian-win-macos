import argparse
import re
from pathlib import Path

from app_build import write_app_version


def next_version(value):
    match = re.fullmatch(r'(\d+)\.(\d+)\.(\d+)\+(\d+)', value)
    if not match:
        raise ValueError('版本必须为 major.minor.patch+build。')
    major, minor, patch, build = map(int, match.groups())
    return f'{major}.{minor}.{patch + 1}+{build + 1}'


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument('--apply', action='store_true')
    options = parser.parse_args()
    root = Path(__file__).resolve().parents[1]
    manifest = root / 'pubspec.yaml'
    text = manifest.read_text('utf-8')
    match = re.search(r'(?m)^version:\s*([^\s]+)\s*$', text)
    if not match:
        raise SystemExit('pubspec.yaml 缺少版本号。')
    version = next_version(match.group(1))
    if options.apply:
        manifest.write_text(text[:match.start(1)] + version + text[match.end(1):], 'utf-8')
        write_app_version(root)
    print(version)


if __name__ == '__main__':
    main()
