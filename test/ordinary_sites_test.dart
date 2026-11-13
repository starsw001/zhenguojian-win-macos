import 'dart:async';
import 'dart:convert';

import 'package:duanju_app/catalog_browser.dart';
import 'package:duanju_app/core_bridge.dart';
import 'package:duanju_app/detail_screen.dart';
import 'package:duanju_app/local_store.dart';
import 'package:duanju_app/models.dart';
import 'package:duanju_app/ordinary_catalog.dart';
import 'package:duanju_app/ordinary_site_selector.dart';
import 'package:duanju_app/playback_launch_screen.dart';
import 'package:duanju_app/player_screen.dart';
import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:shared_preferences/shared_preferences.dart';
import 'package:media_kit/media_kit.dart';

import 'fixtures.dart';
import 'player_fixtures.dart';

const firstSite = Drama(
  id: 'hongguo:100',
  source: 'hongguo',
  title: '普通影视 第一季',
  sourceId: '100',
  episodes: 27,
  category: '真人剧',
  onlineDate: '2026-01-01',
);
const secondSite = Drama(
  id: 'yaguo:200',
  source: 'yaguo',
  title: '普通影视 第一季',
  sourceId: '200',
  episodes: 10,
  category: '短剧',
  onlineDate: '2026-02-01',
);
Drama revised(Drama value, Map<String, dynamic> fields) =>
    Drama.fromJson({...value.toJson(), ...fields});
DramaDetail siteDetail(Drama site, int count) => DramaDetail(site, [
  for (var number = 1; number <= count; number++)
    Episode({
      'id': '${site.id}:$number',
      'source': site.source,
      'currentEpisode': number,
      'title': '第$number集',
    }, number),
]);

class OrdinaryRepository extends FixtureRepository {
  final selections = <Drama>[];
  final pending = <Completer<DramaDetail>>[];
  @override
  Future<Drama?> supplementMetadata(Drama drama) async =>
      revised(drama, {'episodes': 999});
  @override
  Future<DramaDetail> detail(Drama drama) {
    selections.add(drama);
    final result = Completer<DramaDetail>();
    pending.add(result);
    return result.future;
  }

  @override
  Future<CatalogPage> catalog(
    String source, {
    int page = 1,
    String query = '',
    String category = '',
    bool force = false,
  }) async {
    requests.add(source);
    forced.add(force);
    return CatalogPage([
      source == 'hongguo' ? firstSite : secondSite,
    ], fresh: true);
  }
}

void main() {
  test('ordinary card count is declared until selected detail is checked', () {
    expect(firstSite.episodeCountLabel, '源站标注 27 集');
    final checked = validateIsolatedSiteDetail(
      firstSite,
      siteDetail(firstSite, 10),
    );
    expect(checked.drama.episodes, 10);
    expect(checked.drama.episodeCountVerified, isTrue);
    expect(checked.drama.episodeCountLabel, '共 10 集');
    expect(Drama.fromJson(checked.drama.toJson()).episodeCountVerified, isTrue);
    final refreshed = checked.drama.merge(revised(firstSite, {'episodes': 12}));
    expect(refreshed.episodeCountVerified, isFalse);
    expect(refreshed.episodeCountLabel, '源站标注 12 集');
  });

  test('ordinary whitelist excludes other adapters', () {
    expect(SourceSite.ordinaryIds.length, 16);
    for (final id in [
      'huangdou',
      'huangju',
      'yeguo',
      'dsd',
      'huangguo-video',
      'huangguoai',
      'cloudfront',
    ]) {
      expect(SourceSite.ordinaryIds.contains(id), false);
    }
  });
  test('same full title has one card with independent site counts', () {
    final cards = groupOrdinaryTitles([firstSite, secondSite, firstSite]);
    expect(cards.length, 1);
    expect(cards.single.sites.map((site) => site.id), [
      firstSite.id,
      secondSite.id,
    ]);
    expect(cards.single.sites.map((site) => site.episodes), [27, 10]);
    expect(cards.single.episodes, 27);
  });
  test('season year and known media kind conflicts remain separate', () {
    final otherSeason = revised(secondSite, {'title': '普通影视 第二季'});
    final otherYear = revised(secondSite, {'onlineDate': '2025-01-01'});
    final movie = revised(secondSite, {'category': '电影'});
    for (final other in [otherSeason, otherYear, movie]) {
      expect(groupOrdinaryTitles([firstSite, other]).length, 2);
    }
    expect(ordinaryTitleKey('  Ordinary\n Series  '), 'ordinary series');
  });
  test('unknown year cannot join two conflicting known years', () {
    final unknown = revised(firstSite, {'id': 'hongguo:300', 'onlineDate': ''});
    final old = revised(secondSite, {'onlineDate': '2025-01-01'});
    expect(groupOrdinaryTitles([unknown, firstSite, old]).length, 2);
  });
  test('generic and excluded titles are not merged', () {
    final excluded = revised(secondSite, {
      'source': 'huangguoai',
      'id': 'huangguoai:1',
    });
    expect(groupOrdinaryTitles([firstSite, excluded]).length, 2);
    expect(
      groupOrdinaryTitles([
        revised(firstSite, {'title': '短剧'}),
        revised(secondSite, {'title': '短剧'}),
      ]).length,
      2,
    );
  });
  test(
    'site choices are transient and keep original IDs in stored records',
    () {
      final group = groupOrdinaryTitles([firstSite, secondSite]).single;
      expect(group.toJson().containsKey('sites'), false);
      final restored = Drama.fromJson(jsonDecode(jsonEncode(group.toJson())));
      expect(restored.id, firstSite.id);
      expect(restored.sites, isEmpty);
    },
  );
  test('same ID on distinct origins has separate metadata identity', () {
    final a = revised(firstSite, {'sourceId': 'https://one.test/video/100'});
    final b = revised(firstSite, {
      'sourceId': 'https://two.test/video/100',
      'episodes': 10,
    });
    final repo = OrdinaryRepository();
    repo.catalogUpdates.publish(b);
    expect(repo.catalogUpdates.current(a).episodes, 27);
    expect(repo.catalogUpdates.current(b).episodes, 10);
    expect(groupOrdinaryTitles([a, b]).single.sites.length, 2);
  });
  test(
    'selected actual episodes can decrease without leaking other site data',
    () {
      final repo = OrdinaryRepository();
      final detail = validateIsolatedSiteDetail(
        firstSite,
        siteDetail(firstSite, 10),
      );
      expect(detail.drama.episodes, 10);
      expect(detail.episodes.length, 10);
      repo.catalogUpdates.publish(detail.drama);
      expect(repo.catalogUpdates.current(firstSite).episodes, 10);
      expect(repo.catalogUpdates.current(secondSite).episodes, 10);
      expect(
        detail.episodes.every((ep) => ep.id.startsWith('${firstSite.id}:')),
        true,
      );
    },
  );
  test('wrong title ID origin source and foreign chapters are rejected', () {
    final repo = OrdinaryRepository();
    final variants = [
      revised(firstSite, {'title': '另一个作品'}),
      revised(firstSite, {'id': 'hongguo:999'}),
      revised(firstSite, {'source': 'yaguo'}),
      revised(firstSite, {'sourceId': 'https://wrong.test/video/100'}),
    ];
    for (final wrong in variants) {
      expect(
        () => validateIsolatedSiteDetail(firstSite, siteDetail(wrong, 1)),
        throwsA(isA<AppFailure>()),
      );
    }
    for (final fields in [
      {'source': 'yaguo', 'id': '${firstSite.id}:1'},
      {'source': 'hongguo', 'id': 'hongguo:999:1'},
    ]) {
      expect(
        () => validateIsolatedSiteDetail(
          firstSite,
          DramaDetail(firstSite, [Episode(fields, 1)]),
        ),
        throwsA(isA<AppFailure>()),
      );
    }
    expect(repo.catalogUpdates.latest, isNull);
  });
  test(
    'duplicate actual episode numbers count once and empty does not reuse stale list',
    () {
      final one = siteDetail(firstSite, 1).episodes.single;
      final detail = validateIsolatedSiteDetail(
        firstSite,
        DramaDetail(firstSite, [one, one]),
      );
      expect(detail.drama.episodes, 1);
      expect(
        () => validateIsolatedSiteDetail(firstSite, DramaDetail(firstSite, [])),
        throwsA(isA<AppFailure>()),
      );
    },
  );
  test(
    'ordinary catalog results group without adding remote requests',
    () async {
      final repo = OrdinaryRepository();
      final browser = CatalogBrowser(repo);
      final result = await browser.load(
        SourceGroup('all', '全部', [
          SourceSite.hongguo,
          SourceSite.byId('yaguo'),
        ]),
        force: true,
      );
      expect(result.items.length, 1);
      expect(result.items.single.sites.length, 2);
      expect(repo.requests, ['hongguo', 'yaguo']);
      expect(repo.catalogUpdates.knownOrdinarySites.length, 2);
    },
  );
  test(
    'known ordinary registry is bounded and does not retain excluded rows',
    () {
      final repo = OrdinaryRepository();
      repo.catalogUpdates.rememberOrdinary([
        for (var n = 0; n < 4200; n++) revised(firstSite, {'id': 'hongguo:$n'}),
        revised(firstSite, {'id': 'huangguoai:1', 'source': 'huangguoai'}),
      ]);
      expect(repo.catalogUpdates.knownOrdinarySites.length, 4096);
      expect(repo.catalogUpdates.knownOrdinarySites.first.id, 'hongguo:104');
    },
  );

  late LocalStore store;
  late OrdinaryRepository repo;
  Future<void> prepare() async {
    SharedPreferences.setMockInitialValues({});
    store = LocalStore(await SharedPreferences.getInstance());
    repo = OrdinaryRepository();
  }

  Future<void> mount(WidgetTester tester) async {
    await tester.pumpWidget(
      MaterialApp(
        home: PlaybackLaunchScreen(
          drama: firstSite.withSites([firstSite, secondSite]),
          repository: repo,
          store: store,
          playerBuilder: (detail, index, position, selector) => Scaffold(
            body: Column(
              children: [
                ?selector,
                Text(
                  'player:${detail.drama.source} count:${detail.episodes.length} index:$index position:$position',
                ),
              ],
            ),
          ),
        ),
      ),
    );
    await tester.pump();
  }

  Future<void> flush(WidgetTester tester) async {
    await tester.pump();
    await tester.pump(const Duration(milliseconds: 60));
  }

  Future<void> close(WidgetTester tester) async {
    await tester.pumpWidget(const SizedBox.shrink());
    for (final request in repo.pending) {
      if (!request.isCompleted) request.completeError(AppFailure('cancelled'));
    }
    await tester.pump();
    store.dispose();
    expect(tester.takeException(), isNull);
  }

  testWidgets(
    'switch site discards late response and does not append old episodes',
    (tester) async {
      await prepare();
      await mount(tester);
      expect(find.text('站点1'), findsOneWidget);
      expect(find.text('站点2'), findsOneWidget);
      await tester.tap(find.text('站点2'));
      await flush(tester);
      expect(repo.selections.map((site) => site.id), [
        firstSite.id,
        secondSite.id,
      ]);
      repo.pending[1].complete(siteDetail(secondSite, 10));
      await flush(tester);
      expect(find.textContaining('player:yaguo count:10'), findsOneWidget);
      repo.pending[0].complete(siteDetail(firstSite, 7));
      await flush(tester);
      expect(find.textContaining('player:hongguo'), findsNothing);
      expect(find.textContaining('player:yaguo count:10'), findsOneWidget);
      expect(repo.catalogUpdates.current(firstSite).episodes, 27);
      await close(tester);
    },
  );
  testWidgets('each site uses its own saved episode and position', (
    tester,
  ) async {
    await prepare();
    for (final entry in [(firstSite, 3, 19.0), (secondSite, 2, 8.0)]) {
      await store.saveWatch(
        WatchEntry(
          drama: entry.$1,
          episode: entry.$2,
          position: entry.$3,
          duration: 100,
          updatedAt: DateTime(2026, 10, 27),
        ),
      );
    }
    await mount(tester);
    repo.pending[0].complete(siteDetail(firstSite, 27));
    await flush(tester);
    expect(find.textContaining('index:2 position:19.0'), findsOneWidget);
    await tester.tap(find.text('站点2'));
    await flush(tester);
    expect(find.textContaining('player:hongguo'), findsNothing);
    repo.pending[1].complete(siteDetail(secondSite, 10));
    await flush(tester);
    expect(find.textContaining('index:1 position:8.0'), findsOneWidget);
    expect(store.watched(firstSite.id)!.episode, 3);
    expect(store.watched(secondSite.id)!.episode, 2);
    await close(tester);
  });
  testWidgets(
    'failure keeps site selector and retry requests only selected site',
    (tester) async {
      await prepare();
      await mount(tester);
      repo.pending[0].completeError(AppFailure('站点1暂不可用'));
      await flush(tester);
      expect(find.text('站点2'), findsOneWidget);
      await tester.tap(find.text('站点2'));
      await flush(tester);
      repo.pending[1].complete(siteDetail(secondSite, 10));
      await flush(tester);
      expect(find.textContaining('player:yaguo count:10'), findsOneWidget);
      await close(tester);
    },
  );
  test('hidden sites filtered while original site data is retained', () async {
    await prepare();
    repo.catalogUpdates.rememberOrdinary([firstSite, secondSite]);
    expect(visibleOrdinarySites(firstSite, repo, store).length, 2);
    await store.setSourceHidden(secondSite.source, true);
    expect(
      visibleOrdinarySites(firstSite, repo, store).map((site) => site.id),
      [firstSite.id],
    );
    expect(repo.catalogUpdates.knownOrdinarySites.length, 2);
    await store.setSourceHidden(secondSite.source, false);
    expect(visibleOrdinarySites(firstSite, repo, store).length, 2);
    store.dispose();
  });
  testWidgets(
    'detail site switch replaces episode count and metadata cannot inflate it',
    (tester) async {
      await prepare();
      await tester.pumpWidget(
        MaterialApp(
          home: DetailScreen(
            drama: firstSite.withSites([firstSite, secondSite]),
            repository: repo,
            store: store,
          ),
        ),
      );
      await flush(tester);
      repo.pending[0].complete(siteDetail(firstSite, 10));
      await flush(tester);
      expect(find.text('红果 · 共 10 集'), findsOneWidget);
      expect(find.textContaining('999'), findsNothing);
      await tester.tap(find.text('站点2'));
      await flush(tester);
      repo.pending[1].complete(siteDetail(secondSite, 2));
      await flush(tester);
      expect(find.text('芽果 · 共 2 集'), findsOneWidget);
      expect(repo.catalogUpdates.current(firstSite).episodes, 10);
      expect(repo.catalogUpdates.current(secondSite).episodes, 2);
      await close(tester);
    },
  );
  testWidgets('many site choices scroll within a bounded narrow panel', (
    tester,
  ) async {
    final sites = [
      for (var n = 1; n <= 40; n++) revised(firstSite, {'id': 'hongguo:$n'}),
    ];
    await tester.pumpWidget(
      MaterialApp(
        home: Scaffold(
          body: MediaQuery(
            data: const MediaQueryData(textScaler: TextScaler.linear(2)),
            child: SizedBox(
              width: 280,
              height: 180,
              child: OrdinarySiteSelector(
                sites: sites,
                selected: sites.first,
                onSelected: (_) {},
              ),
            ),
          ),
        ),
      ),
    );
    expect(tester.takeException(), isNull);
    await tester.ensureVisible(find.byKey(const ValueKey('ordinary-site-40')));
    await tester.pump();
    expect(tester.takeException(), isNull);
    expect(
      tester.getSize(find.byType(SingleChildScrollView)).height,
      lessThanOrEqualTo(120),
    );
  });
  for (final size in [const Size(390, 844), const Size(1280, 720)]) {
    testWidgets('site selector shares player episode panel at $size', (
      tester,
    ) async {
      await prepare();
      tester.view.devicePixelRatio = 1;
      tester.view.physicalSize = size;
      addTearDown(tester.view.resetPhysicalSize);
      addTearDown(tester.view.resetDevicePixelRatio);
      final player = ScriptedPlayer();
      await tester.pumpWidget(
        MaterialApp(
          home: PlayerScreen(
            detail: siteDetail(firstSite, 10),
            initialIndex: 0,
            repository: repo,
            store: store,
            playerFactory: () => Player(platformPlayer: player),
            videoBuilder: (controls) => controls,
            siteSelector: OrdinarySiteSelector(
              sites: const [firstSite, secondSite],
              selected: firstSite,
              onSelected: (_) {},
            ),
          ),
        ),
      );
      for (var n = 0; n < 12; n++) {
        await tester.pump(const Duration(milliseconds: 20));
      }
      expect(find.text('站点1'), findsOneWidget);
      expect(find.text('站点2'), findsOneWidget);
      expect(tester.takeException(), isNull);
      await tester.pumpWidget(const SizedBox.shrink());
      for (var n = 0; n < 12; n++) {
        await tester.pump(const Duration(milliseconds: 20));
      }
      store.dispose();
      expect(tester.takeException(), isNull);
    });
  }
}
