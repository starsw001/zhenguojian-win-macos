import json
import tempfile
import unittest
from pathlib import Path

from refresh_source_publications import DISCOVERY_ADDRESSES, PERMANENT_ADDRESSES, PUBLICATIONS, normalize_origin, parse_publication, refresh


class PublicationRefreshTests(unittest.TestCase):
    def test_permanent_entries_and_publications_are_separate(self):
        self.assertEqual(PERMANENT_ADDRESSES, ('https://huangguoai.com',))
        self.assertEqual(PUBLICATIONS, ('https://huangguoai.pages.dev', 'https://huang-guo.pages.dev', 'https://huangguoai.ai'))
        self.assertEqual(len(set(DISCOVERY_ADDRESSES)), 4)

    def test_normalization_excludes_credentials_downloads_and_nonpublic_origins(self):
        self.assertEqual(normalize_origin('https://Mirror.Example.org/'), 'https://mirror.example.org')
        for value in ['http://mirror.example.org', 'https://127.0.0.1', 'https://user:token@example.org',
                      'https://mirror.example.org/video/1', 'https://mirror.example.org/?token=x',
                      'https://huangguoai.pages.dev', 'https://x.com/user', 'https://host.local']:
            self.assertIsNone(normalize_origin(value), value)

    def test_only_entry_links_and_data_are_parsed_without_executing_scripts(self):
        origins, scripts = parse_publication('''
          <a href="https://one.example.org/">线路一</a>
          <a href="https://one.example.org">备用</a>
          <a href="https://download.example.org/">下载 APP</a>
          <script src="/publish.js"></script>
          <script>window.runBadCode();</script>''')
        self.assertEqual(origins, ['https://one.example.org'])
        self.assertEqual(scripts, ['/publish.js'])

    def test_route_buttons_exclude_unrelated_footer_and_download_links(self):
        origins, _ = parse_publication('''
          <a class="use-btn" href="https://route-one.example.org">使用线路</a>
          <a href="https://unrelated.example.org">其他站点</a>
          <a class="use-btn" href="https://route-two.example.org">使用线路</a>
          <a href="https://download.example.org">下载 APP</a>''')
        self.assertEqual(origins, ['https://route-one.example.org', 'https://route-two.example.org'])

    def test_subdomain_data_arrays_are_expanded_and_deduplicated(self):
        origins, _ = parse_publication('''const subdomains = [
          "alpha",
          "beta",
        ]; const urls = [
          "example.org",
        ];''')
        self.assertEqual(origins, ['https://alpha.example.org', 'https://beta.example.org'])
        self.assertEqual(parse_publication("const subdomains=['one','two']; const urls=['https://example.org/'];")[0], ['https://one.example.org', 'https://two.example.org', 'https://example.org'])

    def test_refresh_appends_dates_keeps_failed_history_and_ignores_remote_scripts(self):
        with tempfile.TemporaryDirectory() as temporary:
            file = Path(temporary) / 'candidates.json'
            original = {'origin': 'https://old.example.org', 'addedOn': '2026-10-23', 'addedFrom': 'user', 'failure': 'keep'}
            file.write_text(json.dumps([original]), encoding='utf-8')
            calls = []

            def fetch(url):
                calls.append(url)
                if url.endswith('/publish.js'):
                    return '<a href="https://two.example.org">线路</a>'
                if 'failed' in url:
                    raise OSError('fixture unavailable')
                return '<a href="https://one.example.org">线路</a><script src="/publish.js"></script><script src="https://outside.example.org/evil.js"></script>'

            result = refresh(file, fetch, ['https://publication.example.org', 'https://failed.example.org'], date='2026-10-26')
            records = json.loads(file.read_text(encoding='utf-8'))
            self.assertEqual(records[0], original)
            self.assertEqual(result['added'], 2)
            self.assertEqual(records[1]['addedOn'], '2026-10-26')
            self.assertFalse(any('outside' in call for call in calls))
            self.assertEqual(result['publications'][1]['state'], 'failed')
            self.assertEqual(refresh(file, fetch, ['https://publication.example.org'], date='2026-10-27')['added'], 0)

    def test_failed_optional_script_keeps_successful_page_links(self):
        with tempfile.TemporaryDirectory() as temporary:
            file = Path(temporary) / 'candidates.json'

            def fetch(url):
                if url.endswith('.js'):
                    raise OSError('optional script unavailable')
                return '<a href="https://one.example.org">线路</a><script src="/publish.js"></script>'

            result = refresh(file, fetch, ['https://publication.example.org'], date='2026-10-26')
            self.assertEqual(result['added'], 1)
            self.assertEqual(result['publications'][0]['state'], 'partial')

    def test_corrupt_candidate_file_is_retained(self):
        with tempfile.TemporaryDirectory() as temporary:
            file = Path(temporary) / 'candidates.json'
            file.write_text('{broken', encoding='utf-8')
            with self.assertRaises(ValueError):
                refresh(file, lambda _: '', [])
            self.assertEqual(file.read_text(encoding='utf-8'), '{broken')


if __name__ == '__main__':
    unittest.main()
