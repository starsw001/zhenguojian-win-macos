import argparse
import datetime
import html.parser
import http.client
import ipaddress
import json
import os
import re
import tempfile
import time
import urllib.error
import urllib.parse
import urllib.request
from pathlib import Path

PUBLICATIONS = (
    'https://huangguoai.pages.dev',
    'https://huang-guo.pages.dev',
    'https://huangguoai.ai',
)
PERMANENT_ADDRESSES = (
    'https://huangguoai.com',
)
DISCOVERY_ADDRESSES = PUBLICATIONS
EXCLUDED = {'x.com', 'twitter.com', 't.me', 'telegram.org', 'github.com',
            'youtube.com', 'google.com', 'mc.yandex.ru',
            *(urllib.parse.urlsplit(url).hostname for url in (*PUBLICATIONS, *PERMANENT_ADDRESSES))}
MAX_BODY = 2 * 1024 * 1024
DIRECT_TIMEOUT = 10
PROXY_TIMEOUT = 30
PROXY_DELAY = 0.8
ROOT = Path(__file__).resolve().parents[1]
DEFAULT_CANDIDATES = ROOT / 'native/core/huangguo_publication_candidates.json'
DEFAULT_MIRROR_SEEDS = ROOT / 'native/core/huangguo_mirror_seeds.go'
FETCH_ERRORS = (OSError, ValueError, urllib.error.URLError, http.client.HTTPException)


class PublicationRefreshError(RuntimeError):
    def __init__(self, result):
        self.result = result
        failed = [item['publication'] for item in result['publications'] if item['state'] != 'ok']
        super().__init__('发布页刷新未完成，原缓存未改：' + '、'.join(failed))


def normalize_origin(value):
    try:
        url = urllib.parse.urlsplit(value.strip())
        host = (url.hostname or '').lower()
        if url.scheme != 'https' or not host or '.' not in host or url.username or url.password:
            return None
        if url.path not in ('', '/') or url.query or url.fragment or url.port not in (None, 443):
            return None
        if not re.fullmatch(r'[a-z0-9.-]+', host) or any(host == item or host.endswith('.' + item) for item in EXCLUDED):
            return None
        if host.endswith(('.local', '.localhost', '.internal')):
            return None
        try:
            ipaddress.ip_address(host)
            return None
        except ValueError:
            return 'https://' + host
    except ValueError:
        return None


class PublicationParser(html.parser.HTMLParser):
    def __init__(self):
        super().__init__(convert_charrefs=True)
        self.links = []
        self.scripts = []
        self.current = None

    def handle_starttag(self, tag, attributes):
        attributes = dict(attributes)
        if tag == 'a':
            self.current = [attributes.get('href', ''), '', 'use-btn' in attributes.get('class', '').split()]
        elif tag == 'script' and attributes.get('src'):
            self.scripts.append(attributes['src'])

    def handle_data(self, value):
        if self.current is not None:
            self.current[1] += value

    def handle_endtag(self, tag):
        if tag == 'a' and self.current is not None:
            self.links.append(self.current)
            self.current = None


def parse_publication(body):
    parser = PublicationParser()
    parser.feed(body)
    values = []
    preferred = any(link[2] for link in parser.links)
    for address, label, route in parser.links:
        if preferred and not route:
            continue
        if not any(word in label.lower() for word in ('下载', 'app', '隐私', '协议')):
            values.append(address)
    arrays = {}
    for name, text in re.findall(r'(?s)\b(?:var|const|let)\s+(subdomains|urls)\s*=\s*\[(.*?)\]', body):
        arrays[name] = re.findall(r'''["']([a-zA-Z0-9./:_-]+)["']''', text)
    for prefix in arrays.get('subdomains', []):
        if re.fullmatch(r'[a-zA-Z0-9-]{1,63}', prefix):
            for domain in arrays.get('urls', []):
                root = normalize_origin(domain if domain.startswith('https://') else 'https://' + domain)
                if root:
                    values.append('https://' + prefix + '.' + urllib.parse.urlsplit(root).hostname)
    if arrays:
        values.extend(re.findall(r'''["'](https://[a-zA-Z0-9.-]+/?)["']''', body))
    result = []
    for value in values:
        origin = normalize_origin(value)
        if origin and origin not in result and len(result) < 128:
            result.append(origin)
    return result, parser.scripts


def configured_proxy(environment=None):
    environment = os.environ if environment is None else environment
    for name in ('HTTPS_PROXY', 'https_proxy', 'ALL_PROXY', 'all_proxy', 'HTTP_PROXY', 'http_proxy'):
        value = environment.get(name, '').strip()
        if value:
            parsed = urllib.parse.urlsplit(value)
            if parsed.scheme in ('http', 'https') and parsed.hostname:
                return value
    return ''


def read_text(opener, url, timeout):
    request = urllib.request.Request(url, headers={
        'User-Agent': 'Mozilla/5.0',
        'Accept': 'text/html,application/javascript;q=0.9',
        'Cache-Control': 'no-cache',
        'Pragma': 'no-cache',
    })
    with opener.open(request, timeout=timeout) as response:
        body = response.read(MAX_BODY + 1)
        if len(body) > MAX_BODY:
            raise ValueError('publication exceeds 2 MiB')
        return body.decode('utf-8', errors='replace')


def fetch_text(url):
    direct = urllib.request.build_opener(urllib.request.ProxyHandler({}))
    try:
        return read_text(direct, url, DIRECT_TIMEOUT)
    except FETCH_ERRORS as direct_error:
        proxy = configured_proxy()
        if not proxy:
            raise
        time.sleep(PROXY_DELAY)
        proxied = urllib.request.build_opener(urllib.request.ProxyHandler({'http': proxy, 'https': proxy}))
        try:
            return read_text(proxied, url, PROXY_TIMEOUT)
        except FETCH_ERRORS as proxy_error:
            raise OSError(f'direct failed: {direct_error}; proxy failed: {proxy_error}') from proxy_error


def load_candidate_records(path):
    if not path.exists():
        return []
    records = json.loads(path.read_text(encoding='utf-8'))
    if not isinstance(records, list) or len(records) > 2048:
        raise ValueError('invalid candidate record file; original retained')
    for record in records:
        if not isinstance(record, dict) or normalize_origin(record.get('origin', '')) is None:
            raise ValueError('invalid candidate record file; original retained')
    return records


def collect_publications(fetcher, publications, date, checked_at):
    records = []
    results = []
    known = set()
    for publication in publications:
        result = {'publication': publication, 'added': 0, 'date': date, 'state': 'failed'}
        origins = []
        errors = []
        try:
            body = fetcher(publication + '/')
            page_origins, scripts = parse_publication(body)
            origins.extend(page_origins)
            for script in scripts[:2]:
                address = urllib.parse.urljoin(publication + '/', script)
                url = urllib.parse.urlsplit(address)
                if url.scheme + '://' + url.netloc != publication or url.path != '/publish.js':
                    continue
                try:
                    origins.extend(parse_publication(fetcher(address))[0])
                except FETCH_ERRORS as error:
                    errors.append(str(error))
            page_seen = set()
            unique_origins = []
            for origin in origins:
                if origin not in page_seen:
                    page_seen.add(origin)
                    unique_origins.append(origin)
            origins = unique_origins
            if not origins:
                errors.append('发布页未提供可识别的站点根地址')
        except FETCH_ERRORS as error:
            errors.append(str(error))
        if errors:
            result['error'] = '; '.join(errors)
            result['state'] = 'partial' if origins else 'failed'
        else:
            result['state'] = 'ok'
            for origin in origins:
                if origin in known:
                    continue
                known.add(origin)
                records.append({'origin': origin, 'addedOn': date, 'addedFrom': publication})
                result['added'] += 1
        result['candidates'] = len(origins)
        result['checkedAt'] = checked_at
        results.append(result)
    return records, results


def mirror_seed_source(records, publications):
    mirrors = []
    for record in records:
        origin = record['origin']
        mirrors.append({
            'origin': origin,
            'kind': 'huangguoai',
            'addedOn': record['addedOn'],
            'addedFrom': record['addedFrom'],
            'state': 'pending',
            'message': '打包时从三发布页刷新；作为目录备用地址，使用前仍核对页面结构；未声明海报、选集或播放通过',
            'checkedAt': '0001-01-01T00:00:00Z',
            'retryAt': '0001-01-01T00:00:00Z',
            'effectiveOrigin': origin,
            'poster': False,
            'posterWidth': 0,
            'posterHeight': 0,
            'posterBytes': 0,
            'posterMessage': '',
            'browse': False,
            'selection': False,
            'playback': False,
            'catalogCount': 0,
            'episodeCount': 0,
            'width': 0,
            'height': 0,
            'positionMs': 0,
        })
    statuses = []
    for publication in publications:
        statuses.append({
            'url': publication['publication'],
            'state': publication['state'],
            'message': f"打包时读取{publication['candidates']}个最新地址，按三发布页顺序去重后新增{publication['added']}个；未执行远程脚本",
            'checkedAt': publication['checkedAt'],
            'added': publication['added'],
        })
    mirror_json = json.dumps(mirrors, ensure_ascii=False, indent=2)
    publication_json = json.dumps(statuses, ensure_ascii=False, indent=2)
    return f'package core\n\nconst huangguoMirrorSeedJSON = `{mirror_json}`\n\nconst huangguoPublicationSeedJSON = `{publication_json}`\n'


def stage_file(path, body):
    path.parent.mkdir(parents=True, exist_ok=True)
    descriptor, name = tempfile.mkstemp(prefix='.' + path.name + '-', suffix='.tmp', dir=path.parent)
    try:
        with os.fdopen(descriptor, 'wb') as file:
            file.write(body)
            file.flush()
            os.fsync(file.fileno())
    except Exception:
        Path(name).unlink(missing_ok=True)
        raise
    return Path(name)


def write_files_atomically(updates):
    originals = {path: path.read_bytes() if path.exists() else None for path in updates}
    staged = {}
    replaced = []
    try:
        for path, body in updates.items():
            staged[path] = stage_file(path, body)
        for path, temporary in staged.items():
            os.replace(temporary, path)
            replaced.append(path)
    except Exception:
        for path in reversed(replaced):
            original = originals[path]
            if original is None:
                path.unlink(missing_ok=True)
            else:
                rollback = stage_file(path, original)
                os.replace(rollback, path)
        raise
    finally:
        for temporary in staged.values():
            temporary.unlink(missing_ok=True)


def refresh(path, fetcher=fetch_text, publications=DISCOVERY_ADDRESSES, date=None, mirror_seed_path=None):
    old_records = load_candidate_records(path)
    now = datetime.datetime.now(datetime.timezone(datetime.timedelta(hours=8)))
    date = date or now.date().isoformat()
    checked_at = now.isoformat(timespec='seconds')
    records, publication_results = collect_publications(fetcher, publications, date, checked_at)
    current_origins = {row['origin'] for row in records}
    result = {
        'publications': publication_results,
        'candidateCount': len(records),
        'added': len(records),
        'staleCandidatesReplaced': sum(1 for record in old_records if record.get('origin') not in current_origins),
        'oldRecordsRetained': False,
        'candidatesNotAutomaticallyEnabledForMedia': True,
        'catalogFallbackEnabled': True,
    }
    if len(publications) != 3 or any(item['state'] != 'ok' for item in publication_results) or not records:
        raise PublicationRefreshError(result)
    candidate_body = (json.dumps(records, ensure_ascii=False, indent=2) + '\n').encode('utf-8')
    updates = {path: candidate_body}
    if mirror_seed_path is not None:
        updates[mirror_seed_path] = mirror_seed_source(records, publication_results).encode('utf-8')
    result['filesChanged'] = sum(1 for target, body in updates.items() if not target.exists() or target.read_bytes() != body)
    write_files_atomically(updates)
    return result


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument('--output', type=Path, default=DEFAULT_CANDIDATES)
    parser.add_argument('--mirror-seeds', type=Path, default=DEFAULT_MIRROR_SEEDS)
    arguments = parser.parse_args()
    try:
        result = refresh(arguments.output, mirror_seed_path=arguments.mirror_seeds)
    except PublicationRefreshError as error:
        print(json.dumps(error.result, ensure_ascii=False, indent=2))
        print(str(error))
        return 1
    print(json.dumps(result, ensure_ascii=False, indent=2))
    return 0


if __name__ == '__main__':
    raise SystemExit(main())
