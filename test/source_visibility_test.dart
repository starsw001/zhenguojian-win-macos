import 'dart:convert';

import 'package:duanju_app/app_build.dart';
import 'package:duanju_app/local_profiles.dart';
import 'package:duanju_app/local_snapshot.dart';
import 'package:duanju_app/local_store.dart';
import 'package:duanju_app/models.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:shared_preferences/shared_preferences.dart';
import 'package:shared_preferences_platform_interface/shared_preferences_platform_interface.dart';

import 'fixtures.dart';
import 'local_persistence_test.dart' show FailingPreferences;

void main() {
  TestWidgetsFlutterBinding.ensureInitialized();

  Future<LocalStore> create([Map<String, Object> initial = const {}]) async {
    SharedPreferences.setMockInitialValues(initial);
    final store = testStore(await SharedPreferences.getInstance());
    addTearDown(store.dispose);
    return store;
  }

  test(
    'new profiles keep the full compiled source list without writing settings',
    () async {
      final store = await create();
      expect(store.hiddenSources, isEmpty);
      expect(store.visibleSources.length, allSourcesEnabled ? 23 : 1);
      expect(store.visibleSource, 'hongguo');
      expect(store.preferences.getKeys(), isEmpty);
    },
  );

  test(
    'hiding persists without deleting source access, favorites or history',
    () async {
      final store = await create();
      await store.toggleFavorite(FixtureRepository.free);
      await store.saveWatch(
        WatchEntry(
          drama: FixtureRepository.free,
          episode: 2,
          position: 12,
          duration: 90,
          updatedAt: DateTime(2026, 10, 26),
        ),
      );
      final before = jsonDecode(await store.exportBackup()) as Map;
      await store.setSourceHidden('hongguo', true);
      expect(store.isSourceHidden('hongguo'), isTrue);
      expect(store.isSourceVisible('hongguo'), isFalse);
      expect(
        store.visibleSources.any((source) => source.id == 'hongguo'),
        isFalse,
      );
      expect(store.sources.any((source) => source.id == 'hongguo'), isTrue);
      expect(store.allowsSource('hongguo'), isTrue);
      expect(store.canDownload, isTrue);
      expect(store.favorites.single.id, FixtureRepository.free.id);
      expect(store.history.single.position, 12);
      final reopened = testStore(store.preferences);
      addTearDown(reopened.dispose);
      expect(reopened.isSourceHidden('hongguo'), isTrue);
      final after = jsonDecode(await reopened.exportBackup()) as Map;
      for (final field in [
        'history',
        'favorites',
        'followStates',
        'mediaHistory',
        'playback',
        'downloadPreferences',
      ]) {
        expect(
          after['libraries']['default'][field],
          before['libraries']['default'][field],
          reason: field,
        );
      }
      await reopened.setSourceHidden('hongguo', false);
      expect(reopened.isSourceVisible('hongguo'), isTrue);
      expect(reopened.favorites.single.id, FixtureRepository.free.id);
    },
  );

  test(
    'hiding every source is recoverable and never silently unhides one',
    () async {
      final store = await create();
      for (final source in SourceSite.values) {
        await store.setSourceHidden(source.id, true);
      }
      expect(store.visibleSources, isEmpty);
      expect(store.visibleSource, isEmpty);
      expect(store.sources.length, SourceSite.values.length);
      await store.disableSourceGate();
      final reopened = testStore(store.preferences);
      addTearDown(reopened.dispose);
      expect(reopened.visibleSources, isEmpty);
      await reopened.setSourceHidden('hongguo', false);
      expect(reopened.visibleSources.map((source) => source.id), ['hongguo']);
    },
  );

  test(
    'source selection falls back only for display without rewriting its saved value',
    () async {
      final store = await create({'source': 'hongguo'});
      await store.setSourceHidden('hongguo', true);
      expect(store.source, 'hongguo');
      expect(store.visibleSource, allSourcesEnabled ? 'hanxiaoquan' : '');
      await store.setSourceHidden('hongguo', false);
      expect(store.visibleSource, 'hongguo');
    },
  );

  test(
    'backup round trips hidden sources including sources unavailable in red edition',
    () async {
      final store = await create({'hiddenSources': '["sorani"]'});
      await store.setSourceHidden('hongguo', true);
      final backup = await store.exportBackup();
      await store.setSourceHidden('hongguo', false);
      await store.importBackup(backup);
      expect(store.hiddenSources, {'hongguo', 'sorani'});
      expect(store.isSourceVisible('hongguo'), isFalse);
      expect(store.sources.any((source) => source.id == 'hongguo'), isTrue);
    },
  );

  test(
    'old backups without visibility preferences remain importable',
    () async {
      final store = await create();
      final backup =
          jsonDecode(await store.exportBackup()) as Map<String, dynamic>;
      for (final library in (backup['libraries'] as Map).values) {
        (library as Map).remove('hiddenSources');
      }
      await store.setSourceHidden('hongguo', true);
      await store.importBackup(jsonEncode(backup));
      expect(store.hiddenSources, isEmpty);
      expect(store.visibleSources.length, SourceSite.values.length);
    },
  );

  test(
    'invalid backup visibility settings are rejected before replacing data',
    () async {
      final store = await create();
      await store.setSourceHidden('hongguo', true);
      final before = store.preferences.getString(LocalSnapshot.storageKey);
      final backup =
          jsonDecode(await store.exportBackup()) as Map<String, dynamic>;
      backup['libraries']['default']['hiddenSources'] = ['unknown'];
      await expectLater(
        store.importBackup(jsonEncode(backup)),
        throwsFormatException,
      );
      expect(store.preferences.getString(LocalSnapshot.storageKey), before);
      expect(store.isSourceHidden('hongguo'), isTrue);
    },
  );

  test(
    'hidden settings are independent per profile and do not grant permissions',
    () async {
      final store = await create({
        'forceLogin': false,
        'profiles': jsonEncode([
          LocalProfile(
            id: 'default',
            name: '管理员',
            admin: true,
            salt: gateSalt,
            pinHash: await hashProfilePin(gatePin, gateSalt),
          ).toJson(),
          const LocalProfile(
            id: 'visitor',
            name: '访客',
            sources: ['hongguo'],
          ).toJson(),
        ]),
      });
      await store.setSourceHidden('hongguo', true);
      await store.switchProfile('visitor');
      expect(store.isSourceHidden('hongguo'), isFalse);
      expect(store.isSourceVisible('hongguo'), isTrue);
      await store.setSourceHidden('hongguo', true);
      if (allSourcesEnabled) {
        await expectLater(
          store.setSourceHidden('sorani', false),
          throwsStateError,
        );
        expect(store.allowsSource('sorani'), isFalse);
      }
      await store.switchProfile('default', pin: gatePin);
      await store.setSourceHidden('hongguo', false);
      await store.switchProfile('visitor');
      expect(store.isSourceHidden('hongguo'), isTrue);
      final reopened = testStore(store.preferences);
      addTearDown(reopened.dispose);
      expect(reopened.isSourceHidden('hongguo'), isTrue);
    },
  );

  test(
    'unknown sources are rejected and repeated hiding does not rewrite preferences',
    () async {
      final store = await create();
      await expectLater(
        store.setSourceHidden('unknown', true),
        throwsStateError,
      );
      expect(store.preferences.getKeys(), isEmpty);
      await store.setSourceHidden('hongguo', true);
      final first = store.preferences.getString(LocalSnapshot.storageKey);
      await store.setSourceHidden('hongguo', true);
      expect(store.preferences.getString(LocalSnapshot.storageKey), first);
    },
  );

  test(
    'damaged visibility preferences keep sources hidden until manually restored',
    () async {
      final store = await create({'hiddenSources': '{"bad":true}'});
      expect(store.visibleSources, isEmpty);
      expect(store.configurationError, isNull);
      await store.setSourceHidden('hongguo', false);
      expect(store.visibleSources.map((source) => source.id), ['hongguo']);
    },
  );

  for (final failure in ['false', 'throw', 'after']) {
    test(
      'failed $failure visibility write preserves the previous snapshot',
      () async {
        SharedPreferences.resetStatic();
        final platform = FailingPreferences({});
        SharedPreferencesStorePlatform.instance = platform;
        final store = testStore(await SharedPreferences.getInstance());
        addTearDown(store.dispose);
        await store.toggleFavorite(FixtureRepository.free);
        final before = store.preferences.getString(LocalSnapshot.storageKey);
        platform.failure = failure;
        await expectLater(
          store.setSourceHidden('hongguo', true),
          throwsStateError,
        );
        expect(store.isSourceHidden('hongguo'), isFalse);
        expect(store.preferences.getString(LocalSnapshot.storageKey), before);
        SharedPreferences.resetStatic();
        SharedPreferencesStorePlatform.instance = platform;
        final reopened = testStore(await SharedPreferences.getInstance());
        addTearDown(reopened.dispose);
        expect(reopened.isSourceVisible('hongguo'), isTrue);
        expect(reopened.favorites.single.id, FixtureRepository.free.id);
      },
    );
  }
}
