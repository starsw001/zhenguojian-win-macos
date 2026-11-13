import base64
import json
import re
import subprocess
from dataclasses import dataclass
from pathlib import Path


@dataclass(frozen=True)
class BuildVariant:
    all_sources: bool = False

    @property
    def name(self):
        return '真果赏' if self.all_sources else '红果赏'

    @property
    def slug(self):
        return 'zhenguoshang' if self.all_sources else 'hongguoshang'

    @property
    def arguments(self):
        return ['--all-sources'] if self.all_sources else []

    @property
    def flutter_arguments(self):
        return ['--dart-define=ALL_SOURCES=' + str(self.all_sources).lower()]

    @property
    def linker_flags(self):
        return '-s -w -X duanjuapp/native/core.buildAllSources=' + str(self.all_sources).lower()

    @classmethod
    def from_dart_defines(cls, encoded):
        values = {}
        for item in encoded.split(','):
            if not item:
                continue
            key, separator, value = base64.b64decode(item, validate=True).decode('utf-8').partition('=')
            if separator:
                values[key] = value
        return cls(values.get('ALL_SOURCES') == 'true')


def add_variant_argument(parser):
    parser.add_argument('--all-sources', action='store_true',
                        help='构建包含全部站源的真果赏；默认构建仅红果的红果赏')


def write_app_version(root):
    root = Path(root)
    manifest = (root / 'pubspec.yaml').read_text(encoding='utf-8')
    match = re.search(r'^version:\s*(\d+\.\d+\.\d+(?:-[\w.-]+)?\+(\d+))\s*$',
                      manifest, re.MULTILINE)
    if not match:
        raise ValueError('pubspec.yaml 的 version 必须包含版本号和数字构建号。')
    version, build = match.groups()
    output = root / 'lib/app_version.g.dart'
    output.parent.mkdir(parents=True, exist_ok=True)
    text = ("// 由构建脚本依据 pubspec.yaml 生成，请勿手工修改。\n"
            f"const appVersion = '{version}';\nconst appBuild = {int(build)};\n")
    if not output.is_file() or output.read_text(encoding='utf-8') != text:
        output.write_text(text, encoding='utf-8')
    return version


def required_flutter_version(root):
    manifest = (Path(root) / 'pubspec.yaml').read_text(encoding='utf-8')
    match = re.search(r'''(?m)^  flutter:[ \t]*["']?(\d+\.\d+\.\d+)["']?[ \t]*$''', manifest)
    if not match:
        raise ValueError('pubspec.yaml 的 environment.flutter 必须填写固定版本号。')
    return match.group(1)


def verify_flutter_version(root, flutter, environment):
    expected = required_flutter_version(root)
    result = subprocess.run(
        [flutter, '--no-version-check', '--suppress-analytics', '--version', '--machine'],
        env=environment, capture_output=True, text=True,
    )
    if result.returncode != 0:
        raise SystemExit('Flutter 环境检查失败，请手动检查 SDK：\n' +
                         (result.stderr or result.stdout))
    try:
        actual = json.loads(result.stdout)['frameworkVersion']
    except (json.JSONDecodeError, KeyError, TypeError):
        raise SystemExit('无法读取 Flutter 版本，请手动检查 SDK。') from None
    if actual != expected:
        raise SystemExit(f'本项目固定使用 Flutter {expected}，当前为 {actual}。'
                         '请手动切换 SDK；脚本不会安装或升级 Flutter。')
    print('Flutter SDK: ' + actual, flush=True)
