import 'dart:convert';
import 'dart:io';

import 'package:duanju_app/app_storage.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:path/path.dart' as p;

void main() {
  late Directory root;
  late Directory legacy;
  late Directory target;

  setUp(() async {
    root = await Directory.systemTemp.createTemp('zhenguoshang-storage-');
    legacy = Directory(p.join(root.path, '旧配置'));
    target = Directory(p.join(root.path, '新配置'));
    await legacy.create();
  });
  tearDown(() => root.delete(recursive: true));

  Future<File> original(String name, String contents) async {
    final file = File(p.join(legacy.path, name));
    await file.parent.create(recursive: true);
    return file.writeAsString(contents);
  }

  test(
    'migration copies exact records and leaves old bytes untouched',
    () async {
      final data = {
        'shared_preferences.json': '{"history":["keep"]}',
        'sources.json': '{"hongguo":{"enabled":true}}',
        'download-location.json': '{"directory":"D:/download"}',
        'lan-identity.json': '{"id":"same-device"}',
        'nested/record.bin': 'unchanged',
      };
      for (final entry in data.entries) {
        await original(entry.key, entry.value);
      }
      await target.create();
      expect(
        await migrateLegacyAppStorage(legacy: legacy, target: target),
        isTrue,
      );
      for (final entry in data.entries) {
        expect(
          await File(p.join(legacy.path, entry.key)).readAsString(),
          entry.value,
        );
        expect(
          await File(p.join(target.path, entry.key)).readAsString(),
          entry.value,
        );
      }
      expect(
        await migrateLegacyAppStorage(legacy: legacy, target: target),
        isFalse,
      );
    },
  );

  test('an existing target is never overwritten or merged', () async {
    await original('sources.json', 'old');
    await target.create();
    final current = File(p.join(target.path, 'shared_preferences.json'));
    await current.writeAsString('new');
    expect(
      await migrateLegacyAppStorage(legacy: legacy, target: target),
      isFalse,
    );
    expect(await current.readAsString(), 'new');
    expect(await File(p.join(target.path, 'sources.json')).exists(), isFalse);
  });

  test(
    'default videos stay in place with their original download index',
    () async {
      await original('sources.json', '{}');
      final video = await original('downloads/show/S01E001.mp4', 'video-bytes');
      await original('downloads/index.json', '[{"id":"same-job"}]');
      expect(
        await migrateLegacyAppStorage(legacy: legacy, target: target),
        isTrue,
      );
      expect(await video.readAsString(), 'video-bytes');
      expect(
        await Directory(p.join(target.path, 'downloads')).exists(),
        isFalse,
      );
      final location =
          jsonDecode(
                await File(
                  p.join(target.path, 'download-location.json'),
                ).readAsString(),
              )
              as Map<String, dynamic>;
      expect(location['directory'], p.join(legacy.path, 'downloads'));
      expect(
        await File(p.join(legacy.path, 'downloads/index.json')).readAsString(),
        '[{"id":"same-job"}]',
      );
    },
  );

  test(
    'missing or empty legacy state does not create replacement records',
    () async {
      expect(
        await migrateLegacyAppStorage(legacy: legacy, target: target),
        isFalse,
      );
      expect(await target.exists(), isFalse);
      await legacy.delete();
      expect(
        await migrateLegacyAppStorage(legacy: legacy, target: target),
        isFalse,
      );
      expect(await target.exists(), isFalse);
    },
  );

  test('a target file collision fails without overwriting any data', () async {
    await original('sources.json', '{"preserved":true}');
    final collision = File(target.path);
    await collision.writeAsString('existing-user-file');
    await expectLater(
      migrateLegacyAppStorage(legacy: legacy, target: target),
      throwsA(isA<FileSystemException>()),
    );
    expect(await collision.readAsString(), 'existing-user-file');
    expect(
      await File(p.join(legacy.path, 'sources.json')).readAsString(),
      '{"preserved":true}',
    );
  });
}
