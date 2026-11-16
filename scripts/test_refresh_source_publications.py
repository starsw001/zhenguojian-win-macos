import json
import re
import tempfile
import unittest
from pathlib import Path

from refresh_source_publications import (DISCOVERY_ADDRESSES, PERMANENT_ADDRESSES,
                                         PUBLICATIONS, PublicationRefreshError,
                                         configured_proxy, normalize_origin,
                                         parse_publication, refresh)


class PublicationRefreshTests(unittest.TestCase):
    def test_three_publications_are_the_only_authoritative_build_sources(self):
        self.assertEqual(PERMANENT_ADDRESSES, ('https://huangguoai.com',))
        self.assertEqual(PUBLICATIONS, ('https://huangguoai.pages.dev', 'https://huang-guo.pages.dev', 'https://huangguoai.ai'))
        self.assertEqual(DISCOVERY_ADDRESSES, PUBLICATIONS)

    def test_normalization_excludes_credentials_downloads_and_nonpublic_origins(self):
        self.assertEqual(normalize_origin('https://Mirror.Example.org/'), 'https://mirror.example.org')
        for value in ['http://mirror.example.org', 'https://127.0.0.1', 'https://user:token@example.org',
                      'https://mirror.example.org/video/1', 'https://mirror.example.org/?token=x',
                      'https://huangguoai.pages.dev', 'https://huangguoai.com', 'https://x.com/user',
                      'https://host.local']:
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

    def test_refresh_replaces_stale_candidates_in_publication_order(self):
        with tempfile.TemporaryDirectory() as temporary:
            file = Path(temporary) / 'candidates.json'
            file.write_text(json.dumps([{'origin': 'https://old.example.org', 'addedOn': '2026-10-23', 'addedFrom': 'old'}]), encoding='utf-8')
            calls = []

            def fetch(url):
                calls.append(url)
                if url == 'https://one.release/publish.js':
                    return '<a href="https://two.example.org">线路</a>'
                if url == 'https://one.release/':
                    return '<a href="https://one.example.org">线路</a><script src="/publish.js"></script><script src="https://outside.example.org/evil.js"></script>'
                if url == 'https://two.release/':
                    return '<a href="https://one.example.org">重复线路</a>'
                return '<a class="use-btn" href="https://three.example.org">使用线路</a><a href="https://ignored.example.org">其他</a>'

            result = refresh(file, fetch, ['https://one.release', 'https://two.release', 'https://three.release'], date='2026-10-26')
            records = json.loads(file.read_text(encoding='utf-8'))
            self.assertEqual([row['origin'] for row in records], ['https://one.example.org', 'https://two.example.org', 'https://three.example.org'])
            self.assertEqual(result['staleCandidatesReplaced'], 1)
            self.assertFalse(result['oldRecordsRetained'])
            self.assertTrue(result['catalogFallbackEnabled'])
            self.assertFalse(any('outside' in call for call in calls))

    def test_any_failed_publication_retains_candidates_and_seed_bytes(self):
        with tempfile.TemporaryDirectory() as temporary:
            directory = Path(temporary)
            candidates = directory / 'candidates.json'
            seeds = directory / 'seeds.go'
            candidates.write_text('[{"origin":"https://old.example.org","addedOn":"2026-10-23","addedFrom":"old"}]', encoding='utf-8')
            seeds.write_text('package core\n\nconst huangguoMirrorSeedJSON = `[]`\n\nconst huangguoPublicationSeedJSON = `[]`\n', encoding='utf-8')
            before_candidates, before_seeds = candidates.read_bytes(), seeds.read_bytes()

            def fetch(url):
                if url.startswith('https://failed.release'):
                    raise OSError('fixture unavailable')
                return '<a href="https://one.example.org">线路</a>'

            with self.assertRaises(PublicationRefreshError):
                refresh(candidates, fetch, ['https://one.release', 'https://failed.release', 'https://three.release'],
                        date='2026-10-26', mirror_seed_path=seeds)
            self.assertEqual(candidates.read_bytes(), before_candidates)
            self.assertEqual(seeds.read_bytes(), before_seeds)

    def test_declared_script_failure_does_not_publish_partial_addresses(self):
        with tempfile.TemporaryDirectory() as temporary:
            file = Path(temporary) / 'candidates.json'
            file.write_text('[]', encoding='utf-8')

            def fetch(url):
                if url.endswith('.js'):
                    raise OSError('optional script unavailable')
                return '<a href="https://one.example.org">线路</a><script src="/publish.js"></script>'

            with self.assertRaises(PublicationRefreshError):
                refresh(file, fetch, ['https://one.release', 'https://two.release', 'https://three.release'], date='2026-10-26')
            self.assertEqual(file.read_text(encoding='utf-8'), '[]')

    def test_refresh_rebuilds_mirror_origins_and_effective_origins(self):
        with tempfile.TemporaryDirectory() as temporary:
            directory = Path(temporary)
            candidates = directory / 'candidates.json'
            seeds = directory / 'seeds.go'

            def fetch(url):
                host = url.split('/')[2].split('.')[0]
                return f'<a href="https://{host}.example.org">线路</a>'

            refresh(candidates, fetch, ['https://one.release', 'https://two.release', 'https://three.release'],
                    date='2026-10-26', mirror_seed_path=seeds)
            source = seeds.read_text(encoding='utf-8')
            mirror_json = re.search(r'(?s)huangguoMirrorSeedJSON = `(\[.*?\])`', source)[1]
            rows = json.loads(mirror_json)
            self.assertEqual([row['origin'] for row in rows], ['https://one.example.org', 'https://two.example.org', 'https://three.example.org'])
            self.assertTrue(all(row['origin'] == row['effectiveOrigin'] and row['state'] == 'pending' for row in rows))
            self.assertTrue(all(not row['poster'] and not row['playback'] for row in rows))

    def test_proxy_configuration_uses_http_proxy_for_https_fallback(self):
        self.assertEqual(configured_proxy({'HTTP_PROXY': 'http://127.0.0.1:10808'}), 'http://127.0.0.1:10808')
        self.assertEqual(configured_proxy({'HTTPS_PROXY': 'https://proxy.example:443', 'HTTP_PROXY': 'http://other'}), 'https://proxy.example:443')
        self.assertEqual(configured_proxy({'HTTPS_PROXY': 'socks5://unsupported'}), '')

    def test_corrupt_candidate_file_is_retained(self):
        with tempfile.TemporaryDirectory() as temporary:
            file = Path(temporary) / 'candidates.json'
            file.write_text('{broken', encoding='utf-8')
            with self.assertRaises(ValueError):
                refresh(file, lambda _: '', ['https://one.release', 'https://two.release', 'https://three.release'])
            self.assertEqual(file.read_text(encoding='utf-8'), '{broken')


if __name__ == '__main__':
    unittest.main()
