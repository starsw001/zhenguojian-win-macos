import json
from pathlib import Path
import tempfile
from types import SimpleNamespace
import unittest

from audit_catalog_integrity import Audit
from audit_media_connections import LinkAudit, signature


class FakeAPI:
    def __init__(self):
        self.calls = []
        self.error = None

    def call(self, action, **parameters):
        self.calls.append((action, parameters))
        if action != 'checkEpisodeConnection':
            raise AssertionError('completed metadata was fetched again')
        if self.error:
            raise RuntimeError(self.error)
        return {'connected': True, 'actualPlayback': False, 'bytesRead': 1024}


class LinkAuditTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        library = self.root / 'fixture.dll'
        library.write_bytes(b'unit-test-only')
        options = SimpleNamespace(workspace=self.root, library=library)
        db = Audit(options, None)
        self.detail = {'drama': {'id': 'guipian:123', 'source': 'guipian', 'title': '普通作品'}, 'chapters': [{'id': 'guipian:123:' + str(number), 'source': 'guipian', 'currentEpisode': number} for number in (1, 2, 3)]}
        db.db.execute("INSERT INTO works(source,id,drama,origin,state,result,expected) VALUES(?,?,?,'current_source','ok',?,3)", ('guipian', 'guipian:123', json.dumps(self.detail['drama']), json.dumps(self.detail)))
        db.db.commit()
        db.db.close()
        self.api = FakeAPI()
        self.audit = LinkAudit(self.root, self.api, ['guipian'], delay_ms=0)
        self.addCleanup(self.audit.db.close)

    def test_resume_skips_completed_links_and_never_fetches_detail(self):
        self.assertEqual(self.audit.one_source('guipian', 1), 1)
        self.assertEqual(self.audit.one_source('guipian', 10), 2)
        self.assertEqual(self.audit.one_source('guipian', 10), 0)
        self.assertEqual([p['index'] for _, p in self.api.calls], [1, 2, 3])
        self.assertEqual(self.audit.write_report()['sources'][0]['connected'], 3)

    def test_changed_signature_does_not_reuse_other_episode(self):
        first = self.detail['chapters'][0]
        self.audit.db.execute('INSERT INTO connections VALUES(?,?,?,?,?)', ('guipian', 'guipian:123', 1, signature(dict(first, videoUrl='old-link')), json.dumps({'connected': True})))
        self.audit.db.commit()
        self.assertEqual(self.audit.one_source('guipian', 1), 1)
        self.assertEqual(self.api.calls[0][1]['index'], 1)

    def test_signature_ignores_json_field_order(self):
        chapter = self.detail['chapters'][0]
        self.assertEqual(signature(chapter), signature(dict(reversed(list(chapter.items())))))

    def test_historical_only_detail_never_requests_connections_or_inflates_current_totals(self):
        self.audit.db.execute("UPDATE works SET origin='historical_seed_not_current'")
        self.audit.db.commit()
        self.assertEqual(self.audit.one_source('guipian',10),0)
        self.assertEqual(self.api.calls,[])
        self.assertEqual(self.audit.write_report()['sources'][0]['verifiedDetailEpisodes'],0)

    def test_existing_records_repair_counters_without_network(self):
        for chapter in self.detail['chapters']:
            self.audit.db.execute('INSERT INTO connections VALUES(?,?,?,?,?)', ('guipian', 'guipian:123', chapter['currentEpisode'], signature(chapter), json.dumps({'connected': True})))
        self.audit.db.commit()
        self.assertEqual(self.audit.one_source('guipian', 10), 0)
        self.assertEqual(self.api.calls, [])
        self.assertEqual(self.audit.write_report()['sources'][0]['connected'], 3)

    def test_404_is_a_single_link_failure_not_whole_source_block(self):
        self.api.error = 'CDN HTTP 404'
        self.assertEqual(self.audit.one_source('guipian', 2), 2)
        row = self.audit.write_report()['sources'][0]
        self.assertEqual(row['failedOrAuthorizationRequired'], 2)
        self.assertEqual(row['pending'], 1)
        self.assertEqual(row['blockedReason'], '')

    def test_403_stops_source_without_retry_or_bypass(self):
        self.api.error = 'HTTP 403 browser verification required'
        self.assertEqual(self.audit.one_source('guipian', 10), 1)
        self.assertEqual(self.audit.one_source('guipian', 10), 0)
        self.assertEqual(len(self.api.calls), 1)
        self.assertIn('403', self.audit.write_report()['sources'][0]['blockedReason'])

    def test_paid_episode_is_recorded_without_request(self):
        self.detail['chapters'][0]['vip'] = True
        self.audit.db.execute('UPDATE works SET result=?', (json.dumps(self.detail),))
        self.audit.db.commit()
        self.assertEqual(self.audit.one_source('guipian', 1), 1)
        self.assertEqual(self.api.calls, [])
        self.assertEqual(self.audit.write_report()['sources'][0]['failedOrAuthorizationRequired'], 1)


if __name__ == '__main__':
    unittest.main()
