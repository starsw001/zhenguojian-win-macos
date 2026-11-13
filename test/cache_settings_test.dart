import 'package:duanju_app/cache_settings.dart';
import 'package:duanju_app/resource_settings.dart';
import 'package:duanju_app/resource_settings_screen.dart';
import 'package:duanju_app/local_store.dart';
import 'package:duanju_app/player_runtime_options.dart';
import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:shared_preferences/shared_preferences.dart';

import 'fixtures.dart';

class CacheSettingsRepository extends FixtureRepository {
  ResourceSettings settings = const ResourceSettings(
    proxyMode: 'direct',
    downloadConcurrency: 6,
    cacheUsage: {'catalogMiB': CacheUsage(value: 100 << 20, available: true)},
  );
  ResourceSettings? saved;
  int reads = 0;
  @override
  Future<ResourceSettings> resourceSettings() async {
    reads++;
    return settings;
  }

  @override
  Future<ResourceSettings> saveResourceSettings(ResourceSettings value) async {
    saved = value;
    return settings = ResourceSettings.fromJson({
      ...value.toJson(),
      'cacheUsage': {
        'catalogMiB': {'value': 100 << 20, 'available': true},
      },
    });
  }
}

void main() {
  tearDown(() => CacheRuntime.apply(const CacheLimits()));

  test('all ten defaults and bounds match the requested multipliers', () {
    expect(cacheSpecs.length, 10);
    for (final spec in cacheSpecs) {
      expect(spec.defaultValue, spec.minimum * 2);
      expect(spec.maximum, spec.minimum * 8);
      expect(const CacheLimits().value(spec.key), spec.defaultValue);
    }
    final legacy = ResourceSettings.fromJson({'downloadConcurrency': 6});
    expect(legacy.downloadConcurrency, 6);
    expect(legacy.cacheLimits.value('catalogMiB'), 256);
    expect(legacy.toJson()['cacheUsage'], isNull);
  });

  test(
    'input cache limits trim actual registered caches without leaking owners',
    () async {
      final owner = Object();
      final cache = {
        for (var i = 0; i < 100; i++) '$i': ['$i'],
      };
      CacheRuntime.registerInput(owner, cache);
      CacheRuntime.apply(const CacheLimits({'inputEntries': 32}));
      expect(cache.length, 32);
      expect((await CacheRuntime.sampleUsage())['inputEntries']!.value, 32);
      CacheRuntime.unregisterInput(owner);
      expect((await CacheRuntime.sampleUsage())['inputEntries']!.value, 0);
    },
  );

  test(
    'player usage reports bytes and does not fabricate unavailable samples',
    () async {
      final owner = Object();
      CacheRuntime.registerPlayer(owner, 64, () async => 1234);
      expect((await CacheRuntime.sampleUsage())['playerMiB']!.value, 1234);
      CacheRuntime.registerPlayer(owner, 64, () async => null);
      expect(
        (await CacheRuntime.sampleUsage())['playerMiB']!.available,
        isFalse,
      );
      CacheRuntime.unregisterPlayer(owner);
      expect((await CacheRuntime.sampleUsage())['playerMiB']!.value, 0);
      expect(CacheRuntime.playerBytes('{"total-bytes":99}'), 99);
      expect(CacheRuntime.playerBytes('{"fw-bytes":40,"bw-bytes":10}'), 50);
      expect(CacheRuntime.playerBytes('unknown'), isNull);
      expect(await CacheRuntime.readPlayerBytes((_) async => '42'), 42);
    },
  );

  test(
    'player forward and backward budgets sum to the selected total',
    () async {
      for (final mib in [32, 64, 256]) {
        final applied = <String, String>{};
        await configurePortablePlaybackCache(
          (key, value) async {
            applied[key] = value;
          },
          windows: true,
          totalMiB: mib,
        );
        expect(
          int.parse(applied['demuxer-max-bytes']!) +
              int.parse(applied['demuxer-max-back-bytes']!),
          mib << 20,
        );
        expect(applied['cache-on-disk'], 'no');
      }
      await expectLater(
        configurePortablePlaybackCache((_, _) async {}, totalMiB: 257),
        throwsArgumentError,
      );
    },
  );

  testWidgets(
    'network resources displays real usage, validates all limits and preserves network values',
    (tester) async {
      tester.view.physicalSize = const Size(1000, 1400);
      tester.view.devicePixelRatio = 1;
      addTearDown(tester.view.resetPhysicalSize);
      addTearDown(tester.view.resetDevicePixelRatio);
      SharedPreferences.setMockInitialValues({});
      final store = LocalStore(await SharedPreferences.getInstance());
      addTearDown(store.dispose);
      final repository = CacheSettingsRepository();
      await tester.pumpWidget(
        MaterialApp(
          home: ResourceSettingsScreen(repository: repository, store: store),
        ),
      );
      await tester.pumpAndSettle();
      final field = find.byKey(const ValueKey('cache-limit-catalogMiB'));
      await tester.scrollUntilVisible(
        field,
        450,
        scrollable: find.byType(Scrollable).first,
      );
      await tester.pumpAndSettle();
      expect(find.textContaining('当前占用：100.00 MiB'), findsOneWidget);
      expect(tester.widget<TextFormField>(field).controller!.text, '256');
      await tester.enterText(field, '1025');
      await tester.tap(find.text('保存'));
      await tester.pumpAndSettle();
      expect(repository.saved, isNull);
      await tester.enterText(field, '512');
      await tester.tap(find.text('保存'));
      await tester.pumpAndSettle();
      expect(repository.saved!.cacheLimits.value('catalogMiB'), 512);
      expect(repository.saved!.proxyMode, 'direct');
      expect(repository.saved!.downloadConcurrency, 6);
      await tester.tap(find.byTooltip('刷新缓存占用'));
      await tester.pumpAndSettle();
      expect(repository.reads, 2);
      for (final spec in cacheSpecs) {
        await tester.scrollUntilVisible(
          find.byKey(ValueKey('cache-limit-${spec.key}')),
          400,
          scrollable: find.byType(Scrollable).first,
        );
        await tester.pumpAndSettle();
      }
      await tester.pumpWidget(const SizedBox.shrink());
    },
  );
}
