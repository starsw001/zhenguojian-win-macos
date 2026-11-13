import argparse
import datetime
import hashlib
import json
from pathlib import Path
import sqlite3
import time

from audit_catalog_integrity import NativeAPI, ORDINARY_SOURCES, atomic_json, encoded, workspace_lock


def now():
    return datetime.datetime.now().astimezone().isoformat()


def signature(chapter):
    canonical = json.dumps(chapter, ensure_ascii=False, sort_keys=True, separators=(',', ':'))
    return hashlib.sha256(canonical.encode('utf-8')).hexdigest()


class LinkAudit:
    def __init__(self, directory, api, sources=ORDINARY_SOURCES, delay_ms=500, seconds=0):
        self.directory, self.api, self.sources = directory, api, tuple(sources)
        self.delay_ms, self.seconds, self.started = delay_ms, seconds, time.monotonic()
        self.db = sqlite3.connect(directory / 'integrity.sqlite')
        self.db.execute('CREATE TABLE IF NOT EXISTS connection_blocks(source TEXT PRIMARY KEY, reason TEXT NOT NULL, checked_at TEXT NOT NULL)')
        self.db.execute('CREATE INDEX IF NOT EXISTS works_connection_queue ON works(source,state)')
        self.db.commit()

    def stopped(self):
        return self.seconds > 0 and time.monotonic() - self.started >= self.seconds

    def totals(self, source, identifier, detail):
        ok, failed = 0, 0
        for chapter in detail['chapters']:
            row = self.db.execute('SELECT result FROM connections WHERE source=? AND id=? AND number=? AND signature=?', (source, identifier, int(chapter['currentEpisode']), signature(chapter))).fetchone()
            if row:
                if json.loads(row[0]).get('connected'):
                    ok += 1
                else:
                    failed += 1
        self.db.execute('UPDATE works SET expected=?,conn_ok=?,conn_failed=? WHERE source=? AND id=?', (len(detail['chapters']), ok, failed, source, identifier))
        return ok, failed

    def one_source(self, source, limit=8):
        if self.db.execute('SELECT 1 FROM connection_blocks WHERE source=?', (source,)).fetchone():
            return 0
        attempted = 0
        while not self.stopped() and attempted < limit:
            row = self.db.execute("SELECT id,result FROM works WHERE source=? AND origin='current_source' AND state IN ('ok','incomplete','connections_pending') AND expected>conn_ok+conn_failed ORDER BY rowid LIMIT 1", (source,)).fetchone()
            if not row:
                break
            identifier, body = row
            detail = json.loads(body)
            ok, failed = self.totals(source, identifier, detail)
            self.db.commit()
            for chapter in detail['chapters']:
                if self.stopped() or attempted >= limit:
                    return attempted
                number, digest = int(chapter['currentEpisode']), signature(chapter)
                if self.db.execute('SELECT 1 FROM connections WHERE source=? AND id=? AND number=? AND signature=?', (source, identifier, number, digest)).fetchone():
                    continue
                from audit_checkpoint_evidence import saved_connection
                saved = saved_connection(self.db, source, identifier, chapter)
                if saved is not None:
                    result = saved
                elif chapter.get('vip'):
                    result = {'connected': False, 'actualPlayback': False, 'state': 'authorization_required', 'error': '该分集需要合法授权；未自动支付或绕过'}
                else:
                    try:
                        result = self.api.call('checkEpisodeConnection', drama=detail['drama'], chapter=chapter, index=number)
                        if result.get('actualPlayback') is not False or not 0 < result.get('bytesRead', 0) <= 1024:
                            raise ValueError('连接结果缺少不实播及限量读取证据')
                    except (RuntimeError, ValueError) as error:
                        result = {'connected': False, 'actualPlayback': False, 'error': str(error)}
                result['checkedAt'] = now()
                self.db.execute('INSERT INTO connections VALUES(?,?,?,?,?)', (source, identifier, number, digest, encoded(result)))
                if result.get('connected'):
                    ok += 1
                else:
                    failed += 1
                self.db.execute('UPDATE works SET conn_ok=?,conn_failed=? WHERE source=? AND id=?', (ok, failed, source, identifier))
                message = result.get('error', '').lower()
                blocked = not result.get('connected') and any(word in message for word in ('403', '429', '验证码', 'cloudflare', '浏览器验证', '限流'))
                if blocked:
                    self.db.execute('INSERT OR REPLACE INTO connection_blocks VALUES(?,?,?)', (source, result['error'], now()))
                self.db.commit()
                attempted += 1
                self.write_report()
                if blocked:
                    return attempted
                time.sleep(self.delay_ms / 1000)
        return attempted

    def run(self, per_source=8):
        while not self.stopped():
            attempted = 0
            for source in self.sources:
                if self.stopped():
                    break
                attempted += self.one_source(source, per_source)
            if not attempted:
                break
        return self.write_report()

    def write_report(self):
        rows = []
        for source in self.sources:
            expected, ok, failed = self.db.execute("SELECT coalesce(sum(expected),0),coalesce(sum(conn_ok),0),coalesce(sum(conn_failed),0) FROM works WHERE source=? AND origin='current_source' AND state IN ('ok','incomplete','connections_pending')", (source,)).fetchone()
            block = self.db.execute('SELECT reason,checked_at FROM connection_blocks WHERE source=?', (source,)).fetchone()
            rows.append({'source': source, 'verifiedDetailEpisodes': expected, 'connected': ok, 'failedOrAuthorizationRequired': failed, 'pending': max(0, expected-ok-failed), 'blockedReason': block[0] if block else '', 'blockedAt': block[1] if block else None})
        report = {'updatedAt': now(), 'actualPlayback': False, 'metadataRefetched': False, 'workScope': 'current_source_only', 'scope': list(self.sources), 'sources': rows}
        atomic_json(self.directory / 'LINK_REPORT.json', report)
        return report


def main(argv=None):
    parser = argparse.ArgumentParser(description='从已核对详情继续逐集连接，跳过已完成链接，不重采目录或剧集')
    parser.add_argument('--library', type=Path, required=True)
    parser.add_argument('--workspace', type=Path, required=True)
    parser.add_argument('--sources', nargs='+', choices=ORDINARY_SOURCES, default=ORDINARY_SOURCES)
    parser.add_argument('--seconds', type=int, default=180)
    parser.add_argument('--per-source', type=int, default=8)
    parser.add_argument('--delay-ms', type=int, default=500)
    options = parser.parse_args(argv)
    options.library, options.workspace = options.library.resolve(strict=True), options.workspace.resolve(strict=True)
    if options.seconds < 0 or not 1 <= options.per_source <= 100 or options.delay_ms < 200:
        parser.error('时间预算非负，单轮每源1–100条，串行间隔至少200ms')
    with workspace_lock(options.workspace):
        db = sqlite3.connect(options.workspace / 'integrity.sqlite')
        row = db.execute("SELECT value FROM meta WHERE key='librarySHA256'").fetchone()
        db.close()
        with options.library.open('rb') as handle:
            digest = hashlib.file_digest(handle, 'sha256').hexdigest()
        if not row or row[0] != digest:
            raise ValueError('原生核心与已保存断点不一致，不能自动替换核对输入')
        api = NativeAPI(options.library)
        api.call('initialize', directory=str(options.workspace / 'connection-native'), stopStartupCatalog=True)
        audit = LinkAudit(options.workspace, api, options.sources, options.delay_ms, options.seconds)
        try:
            report = audit.run(options.per_source)
        finally:
            audit.db.close()
        print(encoded(report), flush=True)
        return 2 if any(item['pending'] or item['failedOrAuthorizationRequired'] for item in report['sources']) else 0


if __name__ == '__main__':
    raise SystemExit(main())
