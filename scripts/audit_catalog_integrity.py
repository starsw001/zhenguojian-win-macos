import argparse
import contextlib
import ctypes
import datetime
import hashlib
import json
import os
from pathlib import Path
import sqlite3
import sys
import time


ORDINARY_SOURCES = (
    'hongguo', 'hanxiaoquan', 'guipian', 'sorani', 'yaguo', 'maoguo',
    'fanguo', 'guanguo', 'heguo', 'xingguo', 'huaguo', 'niuguo',
    'wangguo', 'faguo', 'piguo', 'wuguo',
)
LIMITED_CATALOGS = {'fanguo', 'maoguo', 'xingguo', 'heguo'}


def now():
    return datetime.datetime.now().astimezone().isoformat()


def encoded(value):
    return json.dumps(value, ensure_ascii=False, separators=(',', ':'))


def verified_page_progress(result, page):
    proof = result.get('pagination') or {}
    current, total = proof.get('current'), proof.get('totalPages')
    return (proof.get('verified') is True and type(current) is int and current == page
            and type(total) is int and page < total <= 1000000
            and bool(result.get('items')) and result.get('hasMore') is True)


def source_detail_block(error):
    message = str(error).lower()
    return any(marker in message for marker in ('403', '429', '验证码', '授权', '登录', '限流', 'cloudflare', '浏览器验证', 'token', '令牌'))


def atomic_json(path, value):
    temporary = path.with_suffix(path.suffix + '.tmp')
    temporary.write_text(json.dumps(value, ensure_ascii=False, indent=2), encoding='utf-8')
    temporary.replace(path)


class NativeAPI:
    def __init__(self, library):
        self.lib = ctypes.CDLL(str(library))
        self.lib.DuanjuRequest.argtypes = [ctypes.c_char_p]
        self.lib.DuanjuRequest.restype = ctypes.c_void_p
        self.lib.DuanjuFree.argtypes = [ctypes.c_void_p]

    def call(self, action, **parameters):
        pointer = self.lib.DuanjuRequest(encoded(dict(action=action, **parameters)).encode('utf-8'))
        if not pointer:
            raise RuntimeError('原生核心未返回响应')
        try:
            result = json.loads(ctypes.string_at(pointer).decode('utf-8'))
        finally:
            self.lib.DuanjuFree(pointer)
        if not result.get('ok'):
            raise RuntimeError(str(result.get('error', '原生操作失败')))
        return result.get('data')


@contextlib.contextmanager
def workspace_lock(directory):
    marker = directory / '.catalog-integrity-workspace'
    if directory.exists() and any(directory.iterdir()) and not marker.is_file():
        raise ValueError('只能使用本工具创建的隔离目录；不能指向现有客户端配置或下载目录')
    directory.mkdir(parents=True, exist_ok=True)
    with marker.open('a+b') as handle:
        handle.seek(0)
        if not handle.read(1):
            handle.write(b'1')
            handle.flush()
        handle.seek(0)
        if os.name == 'nt':
            import msvcrt
            msvcrt.locking(handle.fileno(), msvcrt.LK_NBLCK, 1)
        else:
            import fcntl
            fcntl.flock(handle, fcntl.LOCK_EX | fcntl.LOCK_NB)
        try:
            yield
        finally:
            handle.seek(0)
            if os.name == 'nt':
                msvcrt.locking(handle.fileno(), msvcrt.LK_UNLCK, 1)
            else:
                fcntl.flock(handle, fcntl.LOCK_UN)


class Audit:
    def __init__(self, options, api):
        self.options, self.api = options, api
        self.started = time.monotonic()
        self.db = sqlite3.connect(options.workspace / 'integrity.sqlite')
        self.db.executescript('''
            CREATE TABLE IF NOT EXISTS meta (key TEXT PRIMARY KEY, value TEXT NOT NULL);
            CREATE TABLE IF NOT EXISTS sources (source TEXT PRIMARY KEY, discovered INTEGER NOT NULL DEFAULT 0, error TEXT NOT NULL DEFAULT '', detail_error TEXT NOT NULL DEFAULT '');
            CREATE TABLE IF NOT EXISTS categories (source TEXT, category TEXT, name TEXT NOT NULL, next_page INTEGER NOT NULL DEFAULT 1, complete INTEGER NOT NULL DEFAULT 0, error TEXT NOT NULL DEFAULT '', PRIMARY KEY(source,category));
            CREATE TABLE IF NOT EXISTS pages (source TEXT, category TEXT, page INTEGER, next_page INTEGER NOT NULL, terminal INTEGER NOT NULL, ids TEXT NOT NULL, sha256 TEXT NOT NULL, checked_at TEXT NOT NULL, PRIMARY KEY(source,category,page));
            CREATE TABLE IF NOT EXISTS membership (source TEXT, category TEXT, id TEXT, PRIMARY KEY(source,category,id));
            CREATE TABLE IF NOT EXISTS works (source TEXT, id TEXT, drama TEXT NOT NULL, origin TEXT NOT NULL, state TEXT NOT NULL DEFAULT 'pending', result TEXT, checked_at TEXT, expected INTEGER NOT NULL DEFAULT 0, conn_ok INTEGER NOT NULL DEFAULT 0, conn_failed INTEGER NOT NULL DEFAULT 0, PRIMARY KEY(source,id));
            CREATE TABLE IF NOT EXISTS connections (source TEXT, id TEXT, number INTEGER, signature TEXT, result TEXT NOT NULL, PRIMARY KEY(source,id,number,signature));
            CREATE INDEX IF NOT EXISTS works_state_counts ON works(source,state);
            CREATE INDEX IF NOT EXISTS works_origin_counts ON works(source,origin);
            CREATE INDEX IF NOT EXISTS works_link_summary ON works(source,state,expected,conn_ok,conn_failed);
        ''')
        with options.library.open('rb') as handle:
            library_hash = hashlib.file_digest(handle, 'sha256').hexdigest()
        old = self.db.execute("SELECT value FROM meta WHERE key='librarySHA256'").fetchone()
        if old and old[0] != library_hash:
            raise ValueError('断点绑定了其他原生核心版本，请使用新的隔离目录；旧证据不覆盖')
        self.db.execute("INSERT OR IGNORE INTO meta VALUES('librarySHA256',?)", (library_hash,))
        self.db.commit()

    def stopped(self):
        return self.options.seconds > 0 and time.monotonic() - self.started >= self.options.seconds

    def wait(self):
        time.sleep(self.options.delay_ms / 1000)

    def work(self, source, row, origin):
        identifier = row.get('id', '')
        if row.get('source') != source or not identifier.startswith(source + ':') or not row.get('title', '').strip():
            raise ValueError('目录作品缺少名称或站源标识不一致：' + identifier)
        self.db.execute('INSERT OR IGNORE INTO works(source,id,drama,origin) VALUES(?,?,?,?)', (source, identifier, encoded(row), origin))
        if origin == 'current_source':
            self.db.execute("UPDATE works SET drama=?,origin=? WHERE source=? AND id=?", (encoded(row), origin, source, identifier))

    def seed(self):
        self.db.execute("INSERT OR REPLACE INTO meta VALUES('workScope','current_source_only')")
        self.db.commit()

    def discover(self, source):
        self.db.execute('INSERT OR IGNORE INTO sources(source) VALUES(?)', (source,))
        if self.db.execute('SELECT discovered FROM sources WHERE source=?', (source,)).fetchone()[0]:
            return
        if not self.options.retry_failures and self.db.execute('SELECT error FROM sources WHERE source=?', (source,)).fetchone()[0]:
            return
        try:
            rows = self.api.call('categories', source=source, force=True)['items']
            categories = [r for r in rows if r.get('id') and r.get('kind') != 'tag'] or [{'id': '', 'name': '全部'}]
            for row in categories:
                self.db.execute('INSERT OR IGNORE INTO categories(source,category,name) VALUES(?,?,?)', (source, row['id'], row['name']))
            self.db.execute("UPDATE sources SET discovered=1,error='' WHERE source=?", (source,))
        except (RuntimeError, ValueError, KeyError) as error:
            self.db.execute('UPDATE sources SET error=? WHERE source=?', (str(error), source))
        self.db.commit()
        self.wait()

    def catalogs(self, source):
        count = 0
        for category, name, page in self.db.execute("SELECT category,name,next_page FROM categories WHERE source=? AND complete=0 AND (error='' OR ?) ORDER BY rowid", (source, int(self.options.retry_failures))).fetchall():
            while not self.stopped() and (not self.options.max_pages_per_source or count < self.options.max_pages_per_source):
                try:
                    from audit_checkpoint_evidence import saved_catalog_page
                    saved = saved_catalog_page(self.db, source, category, page)
                    if saved is not None:
                        next_page, terminal = saved
                        self.db.execute("UPDATE categories SET next_page=?,complete=?,error='' WHERE source=? AND category=?", (next_page, int(terminal), source, category))
                        self.db.commit()
                        page = next_page
                        self.write_report()
                        if terminal:
                            break
                        continue
                    result = self.api.call('catalog', source=source, category=category, page=page, force=True, pageOnly=True)
                    if not result.get('authoritative') or result.get('warning'):
                        raise ValueError(result.get('warning') or '核心没有确认本页为独立实时响应')
                    rows = result.get('items', [])
                    ids = [r['id'] for r in rows]
                    more = result.get('hasMore', False)
                    next_page = result.get('nextPage', 0) or page + 1
                    if more and next_page <= page:
                        raise ValueError('分页游标没有前进，不能记为末页')
                    new_count = 0
                    for row in rows:
                        self.work(source, row, 'current_source')
                        changed = self.db.execute('INSERT OR IGNORE INTO membership VALUES(?,?,?)', (source, category, row['id'])).rowcount
                        new_count += changed
                    if more and not new_count and not verified_page_progress(result, page):
                        raise ValueError('本站分类分页重复，不能记为全量完成')
                    digest = hashlib.sha256(encoded(result).encode('utf-8')).hexdigest()
                    self.db.execute('INSERT OR REPLACE INTO pages VALUES(?,?,?,?,?,?,?,?)', (source, category, page, next_page, int(not more), encoded(ids), digest, now()))
                    self.db.execute("UPDATE categories SET next_page=?,complete=?,error='' WHERE source=? AND category=?", (next_page, int(not more), source, category))
                    self.db.commit()
                    count += 1
                    page = next_page
                    self.write_report()
                    self.wait()
                    if not more:
                        break
                except (RuntimeError, ValueError, KeyError) as error:
                    self.db.commit()
                    self.db.execute('UPDATE categories SET error=? WHERE source=? AND category=?', (str(error), source, category))
                    self.db.commit()
                    break
            if self.stopped() or self.options.max_pages_per_source and count >= self.options.max_pages_per_source:
                break

    def details(self, source):
        self.db.execute('INSERT OR IGNORE INTO sources(source) VALUES(?)', (source,))
        blocked = self.db.execute('SELECT detail_error FROM sources WHERE source=?', (source,)).fetchone()[0]
        if blocked and not self.options.retry_failures:
            return
        self.db.execute("UPDATE sources SET detail_error='' WHERE source=?", (source,))
        states = ('pending', 'connections_pending', 'failed') if self.options.retry_failures else ('pending', 'connections_pending')
        selection = 'state IN (' + ','.join('?' for _ in states) + ')'
        if self.options.connections:
            selection += " OR (state IN ('ok','incomplete') AND expected>conn_ok+conn_failed)"
        query = "SELECT id,drama FROM works WHERE source=? AND origin='current_source' AND (" + selection + ") ORDER BY rowid"
        count = 0
        last_error = ''
        for identifier, body in self.db.execute(query, (source, *states)):
            if self.stopped() or self.options.max_details_per_source and count >= self.options.max_details_per_source:
                break
            drama = json.loads(body)
            expected, connected, failed = 0, 0, 0
            try:
                detail = self.api.call('detail', drama=drama, checkOnly=True)
                integrity = detail.get('integrity')
                if not integrity or detail['drama']['id'] != identifier or ' '.join(detail['drama']['title'].split()).casefold() != ' '.join(drama['title'].split()).casefold():
                    raise ValueError('详情没有通过独立作品和分集校验')
                chapters = detail['chapters']
                expected = len(chapters)
                if len(chapters) != integrity['actual'] or detail['drama']['episodes'] != len(chapters):
                    raise ValueError('详情显示集数与独立分集列表不一致')
                try:
                    cover = self.api.call('cover', drama=detail['drama'], force=True)
                    detail['poster'] = {'loaded': bool(cover.get('path')), 'result': cover}
                except RuntimeError as error:
                    detail['poster'] = {'loaded': False, 'error': str(error)}
                state = 'ok' if not detail.get('warning') and detail['poster']['loaded'] else 'incomplete'
                if self.options.connections:
                    for chapter in chapters:
                        number = int(chapter['currentEpisode'])
                        signature = hashlib.sha256(encoded(chapter).encode('utf-8')).hexdigest()
                        previous = self.db.execute('SELECT result FROM connections WHERE source=? AND id=? AND number=? AND signature=?', (source, identifier, number, signature)).fetchone()
                        if previous:
                            if json.loads(previous[0]).get('connected'):
                                connected += 1
                            else:
                                failed += 1
                            continue
                        if self.stopped():
                            state = 'connections_pending'
                            break
                        if chapter.get('vip'):
                            result = {'connected': False, 'actualPlayback': False, 'error': '需要用户合法授权；未自动支付或绕过'}
                        else:
                            try:
                                result = self.api.call('checkEpisodeConnection', drama=detail['drama'], chapter=chapter, index=number)
                            except RuntimeError as error:
                                result = {'connected': False, 'actualPlayback': False, 'error': str(error)}
                        if result.get('connected'):
                            connected += 1
                        else:
                            failed += 1
                        self.db.execute('INSERT INTO connections VALUES(?,?,?,?,?)', (source, identifier, number, signature, encoded(result)))
                        self.db.commit()
                        self.wait()
                result = detail
            except (RuntimeError, ValueError, KeyError) as error:
                state, result = 'failed', {'error': str(error)}
                last_error = str(error)
            self.db.execute('UPDATE works SET state=?,result=?,checked_at=?,expected=?,conn_ok=?,conn_failed=? WHERE source=? AND id=?', (state, encoded(result), now(), expected, connected, failed, source, identifier))
            self.db.commit()
            count += 1
            self.write_report()
            self.wait()
            if state == 'failed' and source_detail_block(last_error):
                self.db.execute('UPDATE sources SET detail_error=? WHERE source=?', (last_error, source))
                self.db.commit()
                break

    def write_report(self):
        sources = []
        for source in self.options.sources:
            categories = [dict(zip(('category', 'name', 'nextPage', 'complete', 'error'), row)) for row in self.db.execute('SELECT category,name,next_page,complete,error FROM categories WHERE source=?', (source,))]
            counts = dict(self.db.execute("SELECT state,count(*) FROM works WHERE source=? AND origin='current_source' GROUP BY state", (source,)))
            observed = self.db.execute("SELECT count(*) FROM works WHERE source=? AND origin='current_source'", (source,)).fetchone()[0]
            excluded = self.db.execute("SELECT count(*) FROM works WHERE source=? AND origin!='current_source'", (source,)).fetchone()[0]
            discovered = self.db.execute('SELECT discovered,error FROM sources WHERE source=?', (source,)).fetchone() or (0, '分类尚未读取')
            detail_error = self.db.execute('SELECT detail_error FROM sources WHERE source=?', (source,)).fetchone()
            catalog_complete = bool(discovered[0] and categories and all(r['complete'] for r in categories))
            detail_complete = bool(sum(counts.values()) and not any(counts.get(s, 0) for s in ('pending', 'failed', 'connections_pending')))
            expected, connected, failed = self.db.execute("SELECT coalesce(sum(expected),0),coalesce(sum(conn_ok),0),coalesce(sum(conn_failed),0) FROM works WHERE source=? AND origin='current_source'", (source,)).fetchone()
            connection_complete = self.options.connections and expected > 0 and connected == expected and failed == 0
            sources.append({'source': source, 'categoryDiscoveryError': discovered[1], 'detailBlockedReason': detail_error[0] if detail_error else '', 'categories': categories, 'ruleCatalogComplete': catalog_complete, 'wholeSiteEnumerable': source not in LIMITED_CATALOGS, 'works': sum(counts.values()), 'currentSourceWorks': observed, 'historicalOnlyWorks': excluded, 'workScope': 'current_source_only', 'detailStates': counts, 'allKnownDetailsChecked': detail_complete, 'allDetailsMatch': detail_complete and counts.get('incomplete', 0) == 0, 'episodeConnections': {'expected': expected, 'connected': connected, 'failed': failed, 'complete': connection_complete}, 'complete': catalog_complete and detail_complete and counts.get('incomplete', 0) == 0 and source not in LIMITED_CATALOGS and (not self.options.connections or connection_complete)})
        report = {'updatedAt': now(), 'actualPlayback': False, 'librarySHA256': self.db.execute("SELECT value FROM meta WHERE key='librarySHA256'").fetchone()[0], 'scope': list(self.options.sources), 'complete': bool(sources) and all(r['complete'] for r in sources), 'exhaustiveClaim': '仅所有分类到真实末页且所有作品详情通过时完成；推荐入口不代表全站', 'sources': sources}
        atomic_json(self.options.workspace / 'REPORT.json', report)
        return report


def main(argv=None):
    parser = argparse.ArgumentParser(description='普通影视源目录及逐部详情断点核对；只检查媒体连接，不播放')
    parser.add_argument('--library', type=Path, required=True)
    parser.add_argument('--workspace', type=Path, required=True)
    parser.add_argument('--sources', nargs='+', choices=ORDINARY_SOURCES, default=ORDINARY_SOURCES)
    parser.add_argument('--seed-catalog', type=Path, help='兼容旧调用；当前仅核对真实目录，历史种子不再导入')
    parser.add_argument('--phase', choices=('catalogs', 'details', 'both'), default='both')
    parser.add_argument('--max-pages-per-source', type=int, default=0)
    parser.add_argument('--max-details-per-source', type=int, default=0)
    parser.add_argument('--seconds', type=int, default=0)
    parser.add_argument('--delay-ms', type=int, default=500)
    parser.add_argument('--connections', action='store_true')
    parser.add_argument('--retry-failures', action='store_true')
    options = parser.parse_args(argv)
    options.library, options.workspace = options.library.resolve(strict=True), options.workspace.resolve()
    if min(options.max_pages_per_source, options.max_details_per_source, options.seconds) < 0 or options.delay_ms < 200:
        parser.error('预算不能为负数，串行请求间隔至少为 200ms；不得绕过限流')
    with workspace_lock(options.workspace):
        api = NativeAPI(options.library)
        api.call('initialize', directory=str(options.workspace / 'native'), stopStartupCatalog=True)
        audit = Audit(options, api)
        running = {'pid': os.getpid(), 'running': True, 'startedAt': now(), 'library': str(options.library), 'phase': options.phase, 'sources': list(options.sources)}
        atomic_json(options.workspace / 'RUNNING.json', running)
        try:
            audit.seed()
            if options.phase != 'details':
                for source in options.sources:
                    if audit.stopped():
                        break
                    audit.discover(source)
                    audit.catalogs(source)
                    print(encoded({'source': source, 'phase': 'catalogs', 'checkpoint': str(options.workspace / 'REPORT.json')}), flush=True)
            if options.phase != 'catalogs':
                for source in options.sources:
                    if audit.stopped():
                        break
                    audit.details(source)
                    print(encoded({'source': source, 'phase': 'details', 'checkpoint': str(options.workspace / 'REPORT.json')}), flush=True)
        except KeyboardInterrupt:
            print('已保留逐页、逐部及逐集断点；未执行播放或删除用户文件', file=sys.stderr)
        finally:
            report = audit.write_report()
            audit.db.close()
            running.update(running=False, finishedAt=now(), complete=report['complete'])
            atomic_json(options.workspace / 'RUNNING.json', running)
        print(encoded({'complete': report['complete'], 'report': str(options.workspace / 'REPORT.json')}), flush=True)
        return 0 if report['complete'] else 2


if __name__ == '__main__':
    raise SystemExit(main())
