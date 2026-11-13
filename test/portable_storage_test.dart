import 'dart:convert';
import 'dart:io';

import 'package:flutter_test/flutter_test.dart';
import 'package:path/path.dart' as p;
import 'package:shared_preferences_platform_interface/types.dart';

import 'package:duanju_app/portable_storage.dart';

void main() {
  late Directory fixture;
  late PortableStorageLayout first;
  late PortableStorageLayout next;

  setUp(() async {
    fixture = await Directory.systemTemp.createTemp('portable-storage-');
    first = PortableStorageLayout(
      p.join(fixture.path, 'version-167', 'app.exe'),
    );
    next = PortableStorageLayout(
      p.join(fixture.path, 'version-168', 'app.exe'),
    );
  });

  tearDown(() async => fixture.delete(recursive: true));

  test(
    'fresh versions do not import previous data and keep manual JSON bytes',
    () async {
      await first.prepare();
      final previous = File(p.join(first.data, 'sources.json'));
      const old = '{"ordinary":{"stage":"old-version"}}';
      await previous.writeAsString(old);
      await next.prepare();
      final current = File(p.join(next.data, 'sources.json'));
      expect(await current.readAsString(), '{}\n');
      const edited =
          '{\n  "ordinary": {"stage": "manual-current-version"}\n}\n';
      await current.writeAsString(edited);
      await next.prepare();
      expect(await current.readAsString(), edited);
      expect(await previous.readAsString(), old);
    },
  );

  test(
    'all path-provider directories belong to the selected version',
    () async {
      final provider = PortablePathProvider(next);
      final paths = await Future.wait([
        provider.getApplicationSupportPath(),
        provider.getApplicationDocumentsPath(),
        provider.getLibraryPath(),
        provider.getApplicationCachePath(),
        provider.getTemporaryPath(),
        provider.getDownloadsPath(),
      ]);
      expect(paths.every((path) => p.isWithin(next.root, path)), isTrue);
      expect(paths.first, next.data);
      expect(paths.last, p.join(next.root, 'download'));
      expect(await Directory(first.root).exists(), isFalse);
    },
  );

  test(
    'preferences survive restart but cannot cross version or edition folders',
    () async {
      final oldStore = PortablePreferencesStore(first);
      final store = PortablePreferencesStore(next);
      await oldStore.setValue('String', 'flutter.history', 'previous-version');
      expect(await store.getAll(), isEmpty);
      await Future.wait([
        store.setValue('String', 'flutter.history', 'current-version'),
        store.setValue('Bool', 'flutter.autoLogin', true),
        store.setValue('StringList', 'flutter.downloads', <String>[
          'episode-1',
        ]),
        store.setValue('Int', 'other.keep', 7),
      ]);
      final restarted = PortablePreferencesStore(next);
      expect(await restarted.getAll(), {
        'flutter.history': 'current-version',
        'flutter.autoLogin': true,
        'flutter.downloads': ['episode-1'],
      });
      await restarted.clearWithParameters(
        ClearParameters(
          filter: PreferencesFilter(
            prefix: 'flutter.',
            allowList: {'flutter.autoLogin'},
          ),
        ),
      );
      expect(
        (await restarted.getAll()).containsKey('flutter.autoLogin'),
        isFalse,
      );
      await restarted.remove('flutter.downloads');
      await restarted.clear();
      expect(await restarted.getAll(), isEmpty);
      expect(jsonDecode(await File(next.preferences).readAsString()), {
        'other.keep': 7,
      });
      expect((await oldStore.getAll())['flutter.history'], 'previous-version');
      final otherEdition = PortableStorageLayout(
        p.join(fixture.path, 'other-edition-168', 'app.exe'),
      );
      expect(await PortablePreferencesStore(otherEdition).getAll(), isEmpty);
    },
  );

  test(
    'manually edited settings are read without using a stale in-memory file',
    () async {
      await next.prepare();
      final file = File(next.preferences);
      await file.writeAsString('{"flutter.manual":"first"}');
      final store = PortablePreferencesStore(next);
      expect((await store.getAll())['flutter.manual'], 'first');
      await file.writeAsString('{"flutter.manual":"second"}');
      expect((await store.getAll())['flutter.manual'], 'second');
      await store.setValue('Int', 'flutter.count', 2);
      expect((await store.getAll())['flutter.manual'], 'second');
    },
  );

  test(
    'invalid sources and preferences fail without replacing the original',
    () async {
      await next.prepare();
      final sources = File(p.join(next.data, 'sources.json'));
      await sources.writeAsString('{broken');
      await expectLater(next.prepare(), throwsFormatException);
      expect(await sources.readAsString(), '{broken');
      final preferences = File(next.preferences);
      await preferences.writeAsString('{broken');
      await expectLater(
        PortablePreferencesStore(
          next,
        ).setValue('String', 'flutter.new', 'value'),
        throwsFormatException,
      );
      expect(await preferences.readAsString(), '{broken');
    },
  );

  test(
    'blocked storage fails locally without creating a shared fallback',
    () async {
      await Directory(next.root).create(recursive: true);
      final blocker = File(next.data);
      await blocker.writeAsString('keep');
      await expectLater(next.prepare(), throwsA(isA<FileSystemException>()));
      expect(await blocker.readAsString(), 'keep');
      expect(await Directory(first.root).exists(), isFalse);
      await expectLater(
        next.checkPath(first.data),
        throwsA(isA<FileSystemException>()),
      );
      expect(
        () => PortableStorageLayout('relative/app.exe'),
        throwsArgumentError,
      );
    },
  );
}
