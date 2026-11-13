import hashlib
import json
from pathlib import Path


def saved_catalog_page(db, source, category, page):
    row = db.execute('SELECT next_page,terminal,ids,sha256 FROM pages WHERE source=? AND category=? AND page=?', (source, category, page)).fetchone()
    if row is None:
        return None
    next_page, terminal, body, digest = row
    identifiers = json.loads(body)
    if not isinstance(identifiers, list) or len(set(identifiers)) != len(identifiers) or len(digest) != 64:
        raise ValueError('已存目录证据无效，未跳过该页')
    if terminal not in (0, 1) or next_page <= page:
        raise ValueError('已存目录游标无效，未跳过该页')
    for identifier in identifiers:
        if not isinstance(identifier, str) or not identifier.startswith(source + ':'):
            raise ValueError('已存目录作品归属不一致')
        if not db.execute('SELECT 1 FROM membership m JOIN works w ON w.source=m.source AND w.id=m.id WHERE m.source=? AND m.category=? AND m.id=?', (source, category, identifier)).fetchone():
            raise ValueError('已存目录缺少作品证据，未跳过该页')
    return next_page, bool(terminal)


def saved_connection(db, source, identifier, chapter):
    if not db.execute("SELECT 1 FROM sqlite_master WHERE type='table' AND name='saved_connection_evidence'").fetchone():
        return None
    row = db.execute('SELECT input,result,evidence_path,evidence_sha256,library_sha256 FROM saved_connection_evidence WHERE source=? AND id=? AND number=?', (source, identifier, int(chapter['currentEpisode']))).fetchone()
    if row is None:
        return None
    expected, result, path, digest, library = row
    binding = db.execute("SELECT value FROM meta WHERE key='librarySHA256'").fetchone()
    if not binding or binding[0] != library:
        return None
    if any(chapter.get(key) != value for key, value in json.loads(expected).items()):
        return None
    if any(chapter.get(key) for key in ('vip', 'headers', 'referer', 'pageUrl')):
        return None
    with Path(path).open('rb') as handle:
        actual = hashlib.file_digest(handle, 'sha256').hexdigest()
    if actual != digest:
        raise ValueError('已存连接证据哈希变化，未复用为通过')
    value = json.loads(result)
    if value.get('method') != 'HEAD' or value.get('httpStatus') != 200 or value.get('actualPlayback') is not False or value.get('bytesRead') != 0:
        raise ValueError('已存连接证据测量类型无效')
    return value
