import json
from pathlib import Path
import tempfile
from types import SimpleNamespace
import unittest

from audit_catalog_integrity import Audit, workspace_lock


class FakeNative:
    def __init__(self):
        self.calls = []
        self.repeat = False

    def call(self, action, **parameters):
        self.calls.append((action, parameters))
        if action == 'categories':
            return {'items': [{'id': '1', 'name': '普通分类'}]}
        if action == 'catalog':
            page = parameters['page']
            identifier = '1' if self.repeat else str(page)
            return {'items': [{'id': 'guipian:' + identifier, 'source': 'guipian', 'title': '普通作品' + identifier}], 'authoritative': True, 'hasMore': page < 2}
        if action == 'detail':
            drama = parameters['drama']
            return {'drama': dict(drama, episodes=2), 'chapters': [{'id': drama['id'] + ':' + str(n), 'currentEpisode': n} for n in [1, 2]], 'integrity': {'actual': 2}, 'warning': ''}
        if action == 'cover':
            return {'path': 'isolated-real-cover-path'}
        if action == 'checkEpisodeConnection':
            return {'connected': True, 'actualPlayback': False, 'bytesRead': 1024}
        raise AssertionError(action)


class AuditTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        library = self.root / 'fixture.dll'
        library.write_bytes(b'unit-test-not-an-actual-dll')
        self.options = SimpleNamespace(workspace=self.root, library=library, sources=['guipian'], seconds=0, delay_ms=0, max_pages_per_source=0, max_details_per_source=0, seed_catalog=None, retry_failures=False, connections=True)
        self.api = FakeNative()
        self.audit = Audit(self.options, self.api)
        self.addCleanup(self.audit.db.close)

    def test_terminal_pages_and_each_work_resume_without_repeating(self):
        self.audit.discover('guipian')
        self.audit.catalogs('guipian')
        self.audit.details('guipian')
        report = self.audit.write_report()
        self.assertTrue(report['complete'])
        self.assertEqual(report['sources'][0]['works'], 2)
        self.assertEqual(report['sources'][0]['episodeConnections']['connected'], 4)
        count = len(self.api.calls)
        self.audit.discover('guipian')
        self.audit.catalogs('guipian')
        self.audit.details('guipian')
        self.assertEqual(len(self.api.calls), count)

    def test_bounded_scan_cannot_be_reported_complete(self):
        self.options.max_pages_per_source = 1
        self.audit.discover('guipian')
        self.audit.catalogs('guipian')
        report = self.audit.write_report()
        self.assertFalse(report['complete'])
        self.assertEqual(report['sources'][0]['categories'][0]['nextPage'], 2)
        self.assertEqual(report['sources'][0]['detailStates']['pending'], 1)
        self.options.max_pages_per_source = 0
        self.audit.catalogs('guipian')
        pages = [parameters['page'] for action, parameters in self.api.calls if action == 'catalog']
        self.assertEqual(pages, [1, 2])

    def test_seeds_do_not_claim_current_catalog_or_request_excluded_sources(self):
        seed = self.root / 'seed.json'
        seed.write_text(json.dumps({'catalogs': {'guipian': [{'id': 'guipian:3', 'source': 'guipian', 'title': '历史作品'}], 'huangguoai': [{'id': 'huangguoai:1', 'source': 'huangguoai', 'title': 'excluded'}]}}), encoding='utf-8')
        self.options.seed_catalog = seed
        self.audit.seed()
        self.audit.seed()
        report = self.audit.write_report()
        self.assertFalse(report['complete'])
        self.assertEqual(report['sources'][0]['works'], 0)
        self.assertEqual(report['sources'][0]['currentSourceWorks'], 0)
        self.assertEqual(self.api.calls, [])

    def test_current_membership_promotes_checked_history_without_repeating_detail(self):
        row = {'id':'guipian:3', 'source':'guipian', 'title':'同一作品'}
        self.audit.work('guipian', row, 'historical_seed_not_current')
        self.audit.db.execute("UPDATE works SET state='ok' WHERE id='guipian:3'")
        self.audit.work('guipian', dict(row, title='当前目录名称'), 'current_source')
        result = self.audit.db.execute("SELECT origin,state,drama FROM works WHERE id='guipian:3'").fetchone()
        self.assertEqual(result[:2], ('current_source','ok'))
        self.assertEqual(json.loads(result[2])['title'], '当前目录名称')
        self.assertEqual(self.api.calls, [])

    def test_old_ids_never_enter_detail_queue_and_two_work_404s_do_not_block_source(self):
        self.options.connections = False
        self.audit.discover('guipian')
        for n in (1,2,3):
            self.audit.work('guipian', {'id':f'guipian:{n}','source':'guipian','title':f'普通作品{n}'}, 'current_source')
        self.audit.work('guipian', {'id':'guipian:99','source':'guipian','title':'历史旧ID'}, 'historical_seed_not_current')
        original = self.api.call
        def call(action, **p):
            if action == 'detail' and p['drama']['id'] in ('guipian:1','guipian:2'):
                raise RuntimeError('旧剧集标识无效 HTTP 404')
            return original(action, **p)
        self.api.call = call
        self.audit.details('guipian')
        self.assertEqual(self.audit.db.execute("SELECT state FROM works WHERE id='guipian:3'").fetchone()[0], 'ok')
        self.assertEqual(self.audit.db.execute("SELECT state FROM works WHERE id='guipian:99'").fetchone()[0], 'pending')
        self.assertEqual(self.audit.db.execute("SELECT detail_error FROM sources WHERE source='guipian'").fetchone()[0], '')
        self.assertEqual(self.audit.write_report()['sources'][0]['works'], 3)

    def test_overlapping_pages_require_verified_current_page_before_advancing(self):
        self.audit.discover('guipian')
        self.audit.work('guipian', {'id':'guipian:1','source':'guipian','title':'普通作品1'}, 'current_source')
        self.audit.db.execute("INSERT INTO membership VALUES('guipian','1','guipian:1')")
        self.options.max_pages_per_source=1
        original=self.api.call
        def call(action, **p):
            result=original(action, **p)
            if action=='catalog': result['pagination']={'verified':True,'current':p['page'],'totalPages':20}
            return result
        self.api.call=call
        self.audit.catalogs('guipian')
        self.assertEqual(self.audit.db.execute("SELECT next_page,complete,error FROM categories").fetchone(), (2,0,''))

    def test_unconfirmed_repeated_page_remains_blocked_not_terminal(self):
        self.audit.discover('guipian')
        self.audit.work('guipian', {'id':'guipian:1','source':'guipian','title':'普通作品1'}, 'current_source')
        self.audit.db.execute("INSERT INTO membership VALUES('guipian','1','guipian:1')")
        self.options.max_pages_per_source=1
        self.audit.catalogs('guipian')
        next_page,complete,error=self.audit.db.execute("SELECT next_page,complete,error FROM categories").fetchone()
        self.assertEqual((next_page,complete), (1,0))
        self.assertIn('分页重复',error)

    def test_403_is_still_a_source_block_not_automatically_retried(self):
        self.audit.discover('guipian');self.audit.catalogs('guipian')
        original=self.api.call
        def call(action, **p):
            if action=='detail': raise RuntimeError('HTTP 403 browser verification required')
            return original(action, **p)
        self.api.call=call;self.audit.details('guipian')
        self.assertIn('403',self.audit.db.execute("SELECT detail_error FROM sources").fetchone()[0])
        self.assertEqual(self.audit.db.execute("SELECT count(*) FROM works WHERE state='pending'").fetchone()[0],1)

    def test_existing_user_directory_is_refused(self):
        protected = self.root / 'user-profile'
        protected.mkdir()
        content = protected / 'settings.json'
        content.write_text('preserve', encoding='utf-8')
        with self.assertRaises(ValueError), workspace_lock(protected):
            self.fail('existing profile accepted')
        self.assertEqual(content.read_text(encoding='utf-8'), 'preserve')

    def test_connection_failures_are_not_actual_playback_or_complete(self):
        original = self.api.call

        def call(action, **parameters):
            if action == 'checkEpisodeConnection':
                raise RuntimeError('HTTP 403; authorization required')
            return original(action, **parameters)

        self.api.call = call
        self.audit.discover('guipian')
        self.audit.catalogs('guipian')
        self.audit.details('guipian')
        report = self.audit.write_report()
        self.assertFalse(report['complete'])
        self.assertFalse(report['actualPlayback'])
        self.assertEqual(report['sources'][0]['episodeConnections']['failed'], 4)


if __name__ == '__main__':
    unittest.main()
