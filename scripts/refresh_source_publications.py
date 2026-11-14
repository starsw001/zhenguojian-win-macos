import argparse
import datetime
import html.parser
import http.client
import ipaddress
import json
import re
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
DISCOVERY_ADDRESSES = (*PUBLICATIONS, *PERMANENT_ADDRESSES)
EXCLUDED = {'x.com', 'twitter.com', 't.me', 'telegram.org', 'github.com',
            'youtube.com', 'google.com', 'mc.yandex.ru',
            *(urllib.parse.urlsplit(url).hostname for url in DISCOVERY_ADDRESSES)}
MAX_BODY = 2 * 1024 * 1024


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
        values.extend(re.findall(r'''["'](https://[a-zA-Z0-9.-]+/?)['"]''', body))
    result = []
    for value in values:
        origin = normalize_origin(value)
        if origin and origin not in result and len(result) < 128:
            result.append(origin)
    return result, parser.scripts


def fetch_text(url):
    request = urllib.request.Request(url, headers={'User-Agent': 'Mozilla/5.0', 'Accept': 'text/html,application/javascript;q=0.9'})
    with urllib.request.urlopen(request, timeout=8) as response:
        body = response.read(MAX_BODY + 1)
        if len(body) > MAX_BODY:
            raise ValueError('publication exceeds 2 MiB')
        return body.decode('utf-8', errors='replace')


def refresh(path, fetcher=fetch_text, publications=DISCOVERY_ADDRESSES, date=None):
    records = json.loads(path.read_text(encoding='utf-8')) if path.exists() else []
    if not isinstance(records, list) or len(records) > 2048:
        raise ValueError('invalid candidate record file; original retained')
    known = {normalize_origin(row.get('origin', '')) for row in records}
    date = date or datetime.datetime.now(datetime.timezone(datetime.timedelta(hours=8))).date().isoformat()
    results = []
    for publication in publications:
        result = {'publication': publication, 'added': 0, 'date': date, 'state': 'failed'}
        try:
            body = fetcher(publication + '/')
            origins, scripts = parse_publication(body)
            script_errors = []
            for script in scripts[:2]:
                address = urllib.parse.urljoin(publication + '/', script)
                url = urllib.parse.urlsplit(address)
                if url.scheme + '://' + url.netloc == publication and url.path == '/publish.js':
                    try:
                        text = fetcher(address)
                        origins.extend(parse_publication(text)[0])
                    except (OSError, ValueError, urllib.error.URLError, http.client.HTTPException) as error:
                        script_errors.append(str(error))
            for origin in origins:
                if origin not in known and len(records) < 2048:
                    records.append({'origin': origin, 'addedOn': date, 'addedFrom': publication})
                    known.add(origin)
                    result['added'] += 1
            result.update(state='ok' if origins else 'empty', candidates=len(set(origins)))
            if script_errors:
                result.update(state='partial' if origins else 'failed', scriptErrors=script_errors)
        except (OSError, ValueError, urllib.error.URLError, http.client.HTTPException) as error:
            result['error'] = str(error)
        results.append(result)
    data = json.dumps(records, ensure_ascii=False, indent=2) + '\n'
    if not path.exists() or path.read_text(encoding='utf-8') != data:
        temporary = path.with_suffix(path.suffix + '.tmp')
        temporary.write_text(data, encoding='utf-8')
        temporary.replace(path)
    return {'publications': results, 'candidateCount': len(records), 'added': sum(item['added'] for item in results), 'oldRecordsRetained': True, 'candidatesNotAutomaticallyEnabled': True}


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument('--output', type=Path, default=Path(__file__).resolve().parents[1] / 'native/core/huangguo_publication_candidates.json')
    arguments = parser.parse_args()
    result = refresh(arguments.output)
    print(json.dumps(result, ensure_ascii=False, indent=2))
    return 0


if __name__ == '__main__':
    raise SystemExit(main())
