import 'dart:io';

import 'package:duanju_app/local_media_catalog.dart';
import 'package:duanju_app/media_library.dart';
import 'package:duanju_app/models.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:path/path.dart' as path;

LocalMediaItem catalogItem(
  String file, {
  String source = 'hongguo',
  String kind = 'export',
}) => LocalMediaItem(
  id: file,
  drama: Drama(id: '$source:test', source: source, title: '测试短剧'),
  file: file,
  kind: kind,
  episodes: [1],
  duration: 1,
  bytes: 4,
  created: DateTime(2026, 10, 26),
);

void main() {
  late Directory directory;
  setUp(
    () async =>
        directory = await Directory.systemTemp.createTemp('media-catalog-'),
  );
  tearDown(() async => directory.delete(recursive: true));

  Future<File> write(String relative, {String text = 'mp4'}) async {
    final file = File(path.join(directory.path, relative));
    await file.parent.create(recursive: true);
    return file.writeAsString(text);
  }

  test(
    'actual exports folders are grouped and episodes naturally ordered',
    () async {
      final first = path.join('exports', '第一部 [hongguo-111111111111]');
      final second = path.join('exports', '第二部 [hongguo-222222222222]');
      final names = ['S01E10.mp4', 'S01E2.mp4', 'S01E1.MP4'];
      for (final name in names) {
        await write(path.join(first, name));
      }
      await write(path.join(second, 'S01E001.mp4'));
      final indexed = catalogItem(path.join(first, 'S01E2.mp4'));
      final result = await scanLocalMediaCatalog(
        directory.path,
        [indexed],
        {'hongguo'},
      );
      expect(result.folders, hasLength(2));
      final folder = result.folders.firstWhere(
        (folder) => folder.directory == first,
      );
      expect(folder.files.map((file) => file.name), [
        'S01E1.MP4',
        'S01E2.mp4',
        'S01E10.mp4',
      ]);
      expect(folder.files[1].item?.id, indexed.id);
      expect(folder.files.first.item, isNull);
      expect(folder.bytes, 9);
    },
  );

  test(
    'refresh reflects external changes without writing index or restoring files',
    () async {
      final relative = path.join('exports', '一部剧', 'S01E001.mp4');
      final file = await write(relative);
      final index = await write(
        'media-library.json',
        text: '{"keep":"original"}',
      );
      final item = catalogItem(relative);
      final before = await scanLocalMediaCatalog(
        directory.path,
        [item],
        {'hongguo'},
      );
      expect(before.folders.single.files, hasLength(1));
      await file.delete();
      await write(path.join('exports', '另一部剧', '第2集.mp4'));
      final after = await scanLocalMediaCatalog(
        directory.path,
        [item],
        {'hongguo'},
      );
      expect(
        after.folders.firstWhere((folder) => folder.name == '一部剧').files,
        isEmpty,
      );
      expect(
        after.folders
            .firstWhere((folder) => folder.name == '另一部剧')
            .files
            .single
            .title,
        '第2集.mp4',
      );
      expect(await file.exists(), isFalse);
      expect(await index.readAsString(), '{"keep":"original"}');
    },
  );

  test(
    'loose files, empty folders and legacy nested specials stay distinguishable',
    () async {
      await write(path.join('exports', 'loose.mp4'));
      await Directory(path.join(directory.path, 'exports', '空目录')).create();
      final special = path.join('exports', '特别篇', 'Season 00', 'S00E002.mkv');
      await write(special);
      final result = await scanLocalMediaCatalog(
        directory.path,
        [catalogItem(special, kind: 'special')],
        {'hongguo'},
      );
      expect(result.folders.first.directory, 'exports');
      expect(result.folders.first.files.single.name, 'loose.mp4');
      expect(
        result.folders.firstWhere((folder) => folder.name == '空目录').files,
        isEmpty,
      );
      expect(
        result.folders
            .firstWhere((folder) => folder.name == '特别篇')
            .files
            .single
            .item
            ?.special,
        isTrue,
      );
    },
  );

  test('temporary metadata empty and non-video files are excluded', () async {
    for (final name in [
      'tvshow.nfo',
      'S01E001.nfo',
      'poster.jpg',
      'S01E002.mp4.writing',
    ]) {
      await write(path.join('exports', '剧集', name));
    }
    await write(path.join('exports', '剧集', 'empty.mp4'), text: '');
    await write(path.join('exports', '.managed-cache', 'hidden.mp4'));
    await write(path.join('exports', '剧集', 'real.mp4'));
    final result = await scanLocalMediaCatalog(directory.path, [], {'hongguo'});
    expect(result.folders, hasLength(1));
    expect(result.folders.single.files.single.name, 'real.mp4');
  });

  test(
    'known source folder markers obey source permissions even without index',
    () async {
      await write(path.join('exports', '红果 [hongguo-111111111111]', 'one.mp4'));
      await write(
        path.join('exports', '黄果 [huangguoai-222222222222]', 'one.mp4'),
      );
      final result = await scanLocalMediaCatalog(directory.path, [], {
        'hongguo',
      });
      expect(result.folders.map((folder) => folder.name), [
        '红果 [hongguo-111111111111]',
      ]);
    },
  );

  test(
    'missing exports is not created and missing merged products are not shown',
    () async {
      final result = await scanLocalMediaCatalog(
        directory.path,
        [
          catalogItem(
            path.join('library', 'missing', 'full.mkv'),
            kind: 'merged',
          ),
        ],
        {'hongguo'},
      );
      expect(result.folders, isEmpty);
      expect(result.merged, isEmpty);
      expect(
        await Directory(path.join(directory.path, 'exports')).exists(),
        isFalse,
      );
    },
  );

  test(
    'existing merged output remains separate and source-restricted',
    () async {
      final relative = path.join('library', 'one', 'full.mkv');
      await write(relative);
      final item = catalogItem(relative, kind: 'merged');
      final result = await scanLocalMediaCatalog(
        directory.path,
        [item],
        {'hongguo'},
      );
      expect(result.merged.single.id, item.id);
      expect(result.folders, isEmpty);
      expect(
        (await scanLocalMediaCatalog(directory.path, [item], {})).merged,
        isEmpty,
      );
    },
  );

  test('natural filename comparisons stay deterministic for equal numbers', () {
    final files = ['S01E10.mp4', 'S01E2.mp4', 'S01E002.mp4', 'S01E1.mp4']
      ..sort(compareMediaNames);
    expect(files, ['S01E1.mp4', 'S01E002.mp4', 'S01E2.mp4', 'S01E10.mp4']);
  });
}
