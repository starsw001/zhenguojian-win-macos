import json
import os
from pathlib import Path
import shutil
import subprocess
import tempfile

def build_launcher(root, target, variant, version, distribution, public_key):
    with tempfile.TemporaryDirectory(prefix='guo-launch-build-') as temp:
        work = Path(temp)
        shutil.copy2(root / 'native/cmd/update-launcher/main_windows.go', work)
        shutil.copy2(root / 'native/go.sum', work)
        (work / 'go.mod').write_text('module guo-launch-build\n\ngo 1.24.1\nrequire duanjuapp/native v0.0.0\nreplace duanjuapp/native => ' + json.dumps((root / 'native').as_posix()) + '\n', 'utf8')
        shutil.copy2(root / 'windows/runner/resources/app_icon.ico', work / 'icon.ico')
        shutil.copy2(root / 'windows/runner/runner.exe.manifest', work / 'app.manifest')
        numbers = version.replace('.', ',').replace('+', ',')
        rc = '''#pragma code_page(65001)
101 ICON "icon.ico"
1 24 "app.manifest"
1 VERSIONINFO
FILEVERSION %(numbers)s
PRODUCTVERSION %(numbers)s
BEGIN
 BLOCK "StringFileInfo"
 BEGIN
  BLOCK "040904e4"
  BEGIN
   VALUE "FileDescription", "%(name)s\\0"
   VALUE "FileVersion", "%(version)s\\0"
   VALUE "CompanyName", "真果赏\\0"
   VALUE "InternalName", "%(name)s\\0"
   VALUE "OriginalFilename", "%(slug)s.exe\\0"
   VALUE "ProductName", "真果赏\\0"
   VALUE "ProductVersion", "%(version)s\\0"
  END
 END
 BLOCK "VarFileInfo"
 BEGIN
  VALUE "Translation", 0x0409, 1252
 END
END
''' % dict(numbers=numbers, name=variant.name, slug=variant.slug, version=version)
        (work / 'launcher.rc').write_text(rc, 'utf8')
        subprocess.run([shutil.which('windres') or 'windres', '-O', 'coff', '-i', 'launcher.rc', '-o', 'resource_windows_amd64.syso'], cwd=work, check=True)
        flags = '-s -w -H=windowsgui' + ''.join(' -X main.' + k + '=' + v for k,v in
            dict(edition=variant.slug, version=version, publicKey=public_key, distributionID=distribution).items())
        subprocess.run(['go', 'build', '-trimpath', '-mod=mod', '-ldflags', flags, '-o', str(target), '.'], cwd=work,
                       env={**os.environ, 'GOOS':'windows','GOARCH':'amd64','CGO_ENABLED':'0','GOWORK':'off','GOTOOLCHAIN':'local','GOPROXY':'off'}, check=True)
