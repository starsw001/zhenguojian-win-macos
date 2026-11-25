import argparse
import json
import platform
import re
import subprocess
import tempfile
import zipfile
from pathlib import Path

from app_build import BuildVariant, add_variant_argument

root = Path(__file__).resolve().parents[1]


def main():
    parser = argparse.ArgumentParser()
    add_variant_argument(parser)
    variant = BuildVariant(parser.parse_args().all_sources)
    if platform.system() != 'Windows':
        raise SystemExit('此检查需要 Windows。')
    version = re.search(r'^version:\s*(\S+)', (root / 'pubspec.yaml').read_text(encoding='utf-8'), re.MULTILINE).group(1)
    package = root / 'dist' / 'windows' / f'{variant.slug}-{version}-windows-x64.zip'
    if not package.is_file():
        package = root / 'dist' / 'windows' / 'releases' / ('tag' + version.split('+')[0]) / f'{variant.slug}-{version.replace("+", "-build")}-windows-x64.zip'
    with tempfile.TemporaryDirectory(prefix='zhenguoshang-smoke-') as temporary:
        directory = Path(temporary)
        with zipfile.ZipFile(package) as archive:
            archive.extractall(directory)
        runtime = directory / package.stem
        if not runtime.is_dir():
            runtime = directory
        media = runtime / 'fixture.mp4'
        subprocess.run(['ffmpeg', '-v', 'error', '-y', '-f', 'lavfi',
                        '-i', 'testsrc2=size=160x90:rate=12', '-t', '3',
                        '-c:v', 'libx264', '-threads', '1', str(media)], check=True)
        report = runtime / 'result.json'
        subprocess.run([str(runtime / (variant.slug + '.exe')), '--package-smoke', str(report), str(media)],
                       cwd=runtime, check=True, timeout=90)
        evidence = json.loads(report.read_text(encoding='utf-8'))
        if evidence.get('ok') is not True:
            raise SystemExit('Windows 包启动验收未通过。')
        output = root / 'build' / 'windows-package-smoke.json'
        output.parent.mkdir(parents=True, exist_ok=True)
        output.write_text(json.dumps(evidence, indent=2) + '\n', encoding='utf-8')
        print(json.dumps(evidence))


if __name__ == '__main__':
    main()
