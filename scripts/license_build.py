import base64
import hashlib
import json
import re
from pathlib import Path


SERVER = 'https://license-server.star-andy-jack.workers.dev'
FIELDS = {'protocol', 'serverBaseURL', 'distributionPublicKey', 'serverPublicKey',
          'activatePath', 'verifyPath', 'eventPath'}


def load_license_build(path):
    if path is None:
        raise ValueError('Windows 授权构建需要 --license-build-config；未配置公钥和真实接口契约时不输出分发成品。')
    path = Path(path).resolve()
    value = json.loads(path.read_text('utf-8'))
    if set(value) != FIELDS or value['protocol'] != 'guoapp-license-v1' or value['serverBaseURL'].rstrip('/') != SERVER:
        raise ValueError('授权构建配置字段、协议或服务地址无效；配置中不能包含私钥、口令或管理员令牌。')
    for name in ('distributionPublicKey', 'serverPublicKey'):
        key = value[name]
        if not isinstance(key, str) or not re.fullmatch(r'[A-Za-z0-9_-]{43}', key):
            raise ValueError('授权公钥必须为 32 字节 Ed25519 公钥的无填充 base64url。')
        if len(base64.urlsafe_b64decode(key + '=')) != 32:
            raise ValueError('授权公钥长度无效。')
    for name in ('activatePath', 'verifyPath', 'eventPath'):
        if not isinstance(value[name], str) or not re.fullmatch(r'/api/[A-Za-z0-9/_-]+', value[name]) or '//' in value[name]:
            raise ValueError('必须配置已经确认的许可证 API 相对路径。')
    return value


def license_linker_flags(value, version):
    names = {'DistributionKey': 'distributionPublicKey', 'ServerKey': 'serverPublicKey',
             'ActivatePath': 'activatePath', 'VerifyPath': 'verifyPath', 'EventPath': 'eventPath'}
    flags = ['-X duanjuapp/native/core.buildLicenseRequired=true',
             '-X duanjuapp/native/core.buildAppVersion=' + version]
    flags += ['-X duanjuapp/native/core.buildLicense' + name + '=' + value[field]
              for name, field in names.items()]
    return ' '.join(flags)


def file_sha256(path):
    digest = hashlib.sha256()
    with Path(path).open('rb') as stream:
        for block in iter(lambda: stream.read(1024 * 1024), b''):
            digest.update(block)
    return digest.hexdigest()


def customer_distribution_id(path):
    value = json.loads(Path(path).read_text('utf-8')).get('distributionId')
    if not isinstance(value, str) or not re.fullmatch(r'[A-Za-z0-9_-]{8,128}', value):
        raise ValueError('管理员专属分发编号无效。')
    return value


def read_build_receipt(path, native, edition, version, config):
    if path is None:
        raise ValueError('缺少本次授权原生构建收据，不能把旧核心封装为授权版。')
    value = json.loads(Path(path).read_text('utf-8'))
    if (value.get('edition') != edition or value.get('version') != version or
            value.get('licenseRequired') is not True or value.get('licenseConfig') != config or
            value.get('nativeSHA256') != file_sha256(native)):
        raise ValueError('授权构建收据与版本、站源类型、公钥或实际原生核心不匹配。')
    return value
