import 'dart:async';

import 'package:duanju_app/app_build.dart';
import 'package:duanju_app/catalog_browser.dart';
import 'package:duanju_app/catalog_filters.dart';
import 'package:duanju_app/core_bridge.dart';
import 'package:duanju_app/local_store.dart';
import 'package:duanju_app/main.dart';
import 'package:duanju_app/models.dart';
import 'package:duanju_app/ranking_models.dart';
import 'package:duanju_app/widgets.dart';
import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:shared_preferences/shared_preferences.dart';

import 'fixtures.dart';

class CategoryRepository extends FixtureRepository {
  final categoryRequests = <String>[];
  Completer<CatalogPage>? pendingComic;
  bool failLegacy = false;
  bool paginate = false;
  final rankRequests = <String>[];

  @override
  Future<CatalogPage> cached(String source, {String category = ''}) async =>
      category.isEmpty
      ? cachedPages[source] ?? CatalogPage([])
      : CatalogPage([]);

  @override
  Future<List<CatalogCategory>> categories(
    String source, {
    bool force = false,
  }) async => switch (source) {
    'hongguo' => const [
      CatalogCategory.all,
      CatalogCategory('short_play', '真人剧'),
      CatalogCategory('comic_series', '漫剧'),
      CatalogCategory('ai_series', 'AI 剧'),
    ],
    'huangguo-video' => const [CatalogCategory.all, CatalogCategory('2', '短片')],
    'huangguoai' => const [
      CatalogCategory.all,
      CatalogCategory('original', '黄果原创'),
      CatalogCategory('ai-duanju', '短剧'),
      CatalogCategory('ai-manju', '漫剧'),
      CatalogCategory('topics', '专题'),
      CatalogCategory('ranks-hot', '排行榜'),
      CatalogCategory('original-manju', '原创漫剧'),
      CatalogCategory('original-duanju', '原创短剧'),
      CatalogCategory('ai-duanju-hot', '当前热播'),
      CatalogCategory('ai-duanju-random', '精选推荐'),
      CatalogCategory('ai-manju-hot', '当前热播'),
      CatalogCategory('ai-manju-random', '精选推荐'),
      CatalogCategory('ranks-recommend', '推荐榜'),
      CatalogCategory('ranks-potential', '飙升榜'),
    ],
    'cloudfront' => const [
      CatalogCategory.all,
      CatalogCategory('old-short', 'AI成人短剧'),
    ],
    _ => const [CatalogCategory.all],
  };

  @override
  Future<CatalogPage> catalog(
    String source, {
    int page = 1,
    String query = '',
    String category = '',
    bool force = false,
  }) async {
    categoryRequests.add('$source|$category|$page');
    if (source == 'cloudfront' && failLegacy) throw AppFailure('合成入口失败');
    if (category == 'ai-manju' && pendingComic != null) {
      return pendingComic!.future;
    }
    return CatalogPage(
      [
        Drama(
          id: '$source:$category:$page',
          source: source,
          title: '$source · ${category.isEmpty ? '全部' : category}',
          category: source == 'hongguo' ? '异能' : '',
        ),
      ],
      page: page,
      hasMore: paginate && source == 'huangguo-video' && page == 1,
    );
  }

  @override
  Future<List<RankingBoard>> rankingBoards() async => const [
    RankingBoard(id: 'hongguo-hot', source: 'hongguo', name: '总热播榜'),
    RankingBoard(id: 'hongguo-real', source: 'hongguo', name: '真人剧榜'),
  ];

  @override
  Future<RankingPage> rankings(
    String board, {
    int page = 1,
    bool force = false,
  }) async {
    rankRequests.add('$board|$page|$force');
    return RankingPage(
      items: [
        RankingItem(
          (page - 1) * 20 + 1,
          Drama(
            id: 'hongguo:rank:$board:$page',
            source: 'hongguo',
            title: '合成榜单第$page页',
          ),
        ),
      ],
      page: page,
      hasMore: page == 1,
    );
  }
}

class HuangjuCategoryRepository extends CategoryRepository {
  bool failCategories = false;
  static const rows = [
    Drama(
      id: 'huangju:cached-1',
      source: 'huangju',
      title: '缓存都市剧',
      category: '都市，重生； 都市',
    ),
    Drama(
      id: 'huangju:cached-2',
      source: 'huangju',
      title: '缓存青春剧',
      category: '青春',
    ),
  ];

  @override
  Future<CatalogPage> cached(String source, {String category = ''}) async =>
      CatalogPage(rows, fresh: true, hasMore: false);

  @override
  Future<List<CatalogCategory>> categories(
    String source, {
    bool force = false,
  }) async {
    if (failCategories) throw AppFailure('合成分类接口不可用');
    return const [
      CatalogCategory.all,
      CatalogCategory('@new', '最新'),
      CatalogCategory('urban', '都市'),
      CatalogCategory('qing-chun', '青春'),
      CatalogCategory('invalid-combined', '都市，重生'),
    ];
  }

  @override
  Future<CatalogPage> catalog(
    String source, {
    int page = 1,
    String query = '',
    String category = '',
    bool force = false,
  }) async {
    categoryRequests.add('$source|$category|$page');
    return CatalogPage(
      category == 'urban' ? [rows.first] : rows,
      hasMore: false,
      page: page,
    );
  }
}

void main() {
  Future<LocalStore> mount(
    WidgetTester tester,
    CategoryRepository repository, {
    String source = 'hongguo',
    double width = 390,
    double scale = 1,
  }) async {
    tester.view.physicalSize = Size(width, 844);
    tester.view.devicePixelRatio = 1;
    tester.platformDispatcher.textScaleFactorTestValue = scale;
    addTearDown(tester.view.reset);
    addTearDown(tester.platformDispatcher.clearTextScaleFactorTestValue);
    SharedPreferences.setMockInitialValues(await gatePreferences());
    final store = testStore(await SharedPreferences.getInstance());
    addTearDown(store.dispose);
    await unlockGate(store);
    await store.setSource(source);
    await tester.pumpWidget(DuanjuApp(repository: repository, store: store));
    await tester.pumpAndSettle();
    return store;
  }

  for (final width in [390.0, 320.0]) {
    testWidgets('compact home keeps search collapsed at $width', (
      tester,
    ) async {
      await mount(
        tester,
        CategoryRepository(),
        width: width,
        scale: width == 320 ? 2 : 1,
      );
      expect(find.byType(TextField), findsNothing);
      final coverTop = tester.getTopLeft(find.byType(DramaCover).first).dy;
      expect(
        coverTop,
        greaterThanOrEqualTo(
          tester.getBottomLeft(find.byType(CatalogFilters)).dy,
        ),
      );
      expect(coverTop, lessThan(tester.view.physicalSize.height / 2));
      final switcher = find.byKey(const ValueKey('source-switch'));
      final rankings = find.byKey(const ValueKey('open-rankings'));
      expect(
        tester.getCenter(switcher).dy,
        closeTo(tester.getCenter(rankings).dy, 2),
      );
      await tester.tap(find.byKey(const ValueKey('toggle-search')));
      await tester.pumpAndSettle();
      expect(find.byType(TextField), findsOneWidget);
      await tester.tap(find.byKey(const ValueKey('toggle-search')));
      await tester.pumpAndSettle();
      expect(find.byType(TextField), findsNothing);
      expect(tester.getTopLeft(find.byType(DramaCover).first).dy, coverTop);
      expect(tester.takeException(), isNull);
    });
  }

  testWidgets(
    'Huangguo AI has independent catalogs and categories and rejects stale replies',
    (tester) async {
      final repository = CategoryRepository();
      await mount(tester, repository, source: 'huangguoai');
      expect(find.text('黄果AI'), findsOneWidget);
      expect(find.text('黄果'), findsNothing);
      expect(find.text('入口'), findsNothing);
      expect(find.text('旧版'), findsNothing);
      expect(repository.categoryRequests.toSet(), equals({'huangguoai||1'}));
      expect(find.text('AI成人短剧'), findsNothing);
      expect(find.text('AI 短剧'), findsNothing);
      expect(find.text('AI 漫剧'), findsNothing);
      expect(find.text('黄果原创'), findsOneWidget);
      expect(find.text('短剧'), findsOneWidget);
      expect(find.text('漫剧'), findsOneWidget);
      Future<void> choose(String name) async {
        final chip = find.widgetWithText(ChoiceChip, name);
        await tester.ensureVisible(chip);
        await tester.tap(chip);
        await tester.pump();
        await tester.pump(const Duration(milliseconds: 250));
      }

      final pending = Completer<CatalogPage>();
      repository.pendingComic = pending;
      await choose('漫剧');
      await choose('短剧');
      await tester.pumpAndSettle();
      pending.complete(
        CatalogPage(const [
          Drama(id: 'huangguoai:stale', source: 'huangguoai', title: '过期分类结果'),
        ]),
      );
      await tester.pumpAndSettle();
      expect(find.text('过期分类结果'), findsNothing);
      expect(repository.categoryRequests, contains('huangguoai|ai-duanju|1'));
      expect(find.text('huangguoai · ai-duanju'), findsOneWidget);
      expect(find.text('cloudfront · old-short'), findsNothing);
      expect(
        repository.categoryRequests.any(
          (request) =>
              request.startsWith('cloudfront|') ||
              request.startsWith('huangguo-video|'),
        ),
        isFalse,
      );
      expect(tester.takeException(), isNull);
    },
    skip: !allSourcesEnabled,
  );

  test(
    'remote content types never hide fine categories from cached pages',
    () async {
      final repository = CategoryRepository();
      repository.cachedPages['hongguo'] = CatalogPage(const [
        Drama(
          id: 'hongguo:old1',
          source: 'hongguo',
          title: '缓存剧甲',
          category: '都市',
        ),
        Drama(
          id: 'hongguo:old2',
          source: 'hongguo',
          title: '缓存剧乙',
          category: '成长',
        ),
      ], page: 7);
      final browser = CatalogBrowser(repository);
      const group = SourceGroup('hongguo', '红果', [SourceSite.hongguo]);
      await browser.loadCategories(group);
      await browser.load(group, category: 'category:漫剧');
      expect(
        browser.categories(group).map((category) => category.name),
        containsAll(['真人剧', '漫剧', 'AI 剧', '都市', '成长', '异能']),
      );
      final local = await browser.load(group, category: 'local:都市');
      expect(local.items.map((drama) => drama.id), contains('hongguo:old1'));
    },
  );

  test(
    'Huangju cached tags become atomic choices without inventing server slugs',
    () async {
      final repository = HuangjuCategoryRepository();
      final browser = CatalogBrowser(repository);
      final group = SourceGroup('huangju', '剧果', [SourceSite.byId('huangju')]);
      await browser.loadCategories(group);
      final categories = browser.categories(group);
      expect(categories.map((entry) => entry.name), [
        '全部',
        '最新',
        '都市',
        '青春',
        '重生',
      ]);
      expect(categories.where((entry) => entry.name == '都市'), hasLength(1));
      await browser.load(group, category: 'category:都市');
      expect(repository.categoryRequests.last, 'huangju|urban|1');
      final local = await browser.load(group, category: 'local:重生');
      expect(repository.categoryRequests.last, 'huangju||1');
      expect(local.items.map((drama) => drama.id), ['huangju:cached-1']);
      expect(HuangjuCategoryRepository.rows.first.category, '都市，重生； 都市');
    },
  );

  test('Huangju offline fallback keeps atomic cached filters', () async {
    final repository = HuangjuCategoryRepository()..failCategories = true;
    final browser = CatalogBrowser(repository);
    final group = SourceGroup('huangju', '剧果', [SourceSite.byId('huangju')]);
    expect(await browser.loadCategories(group), isNotNull);
    expect(browser.categories(group).map((category) => category.name).toSet(), {
      '全部',
      '都市',
      '重生',
      '青春',
    });
    final page = await browser.load(
      group,
      category: 'local:重生',
      cacheOnly: true,
    );
    expect(page.items.map((drama) => drama.id), ['huangju:cached-1']);
    expect(repository.categoryRequests, isEmpty);
  });

  for (final source in ['fanguo', 'guanguo']) {
    test(
      '$source cached comma-separated tags are independent categories',
      () async {
        final repository = CategoryRepository();
        repository.cachedPages[source] = CatalogPage([
          Drama(
            id: '$source:one',
            source: source,
            title: '缓存剧甲',
            category: '都市, 重生，都市',
          ),
          Drama(
            id: '$source:two',
            source: source,
            title: '缓存剧乙',
            category: '青春',
          ),
        ]);
        final browser = CatalogBrowser(repository);
        final group = SourceGroup(source, source, [SourceSite.byId(source)]);
        await browser.loadCategories(group);
        expect(browser.categories(group).map((c) => c.name).toSet(), {
          '全部',
          '都市',
          '重生',
          '青春',
        });
        final page = await browser.load(group, category: 'local:都市');
        expect(page.items.map((d) => d.id), ['$source:one']);
      },
    );
  }

  testWidgets('Huangju category chips and old cached-title selection agree', (
    tester,
  ) async {
    final repository = HuangjuCategoryRepository();
    await mount(tester, repository, source: 'huangju', width: 1200);
    expect(find.widgetWithText(ChoiceChip, '都市，重生'), findsNothing);
    expect(find.widgetWithText(ChoiceChip, '都市'), findsOneWidget);
    final rebirth = find.widgetWithText(ChoiceChip, '重生');
    await tester.ensureVisible(rebirth);
    await tester.tap(rebirth);
    await tester.pumpAndSettle();
    expect(find.text('缓存都市剧'), findsWidgets);
    expect(find.text('缓存青春剧'), findsNothing);
    expect(tester.takeException(), isNull);
  }, skip: !allSourcesEnabled);

  test(
    'group pagination retries failed member without skipping or reloading exhausted members',
    () async {
      final repository = CategoryRepository()
        ..failLegacy = true
        ..paginate = true;
      final browser = CatalogBrowser(repository);
      final group = SourceGroup('all', '全部站源', [
        SourceSite.byId('huangguo-video'),
        SourceSite.byId('huangguoai'),
        SourceSite.byId('cloudfront'),
      ]);
      final first = await browser.load(group);
      expect(first.items.length, 2);
      expect(first.warning, '合成入口失败');
      repository.failLegacy = false;
      final next = await browser.load(group, more: true);
      expect(
        repository.categoryRequests
            .where((request) => request == 'cloudfront||1')
            .length,
        2,
      );
      expect(repository.categoryRequests, contains('huangguo-video||2'));
      expect(repository.categoryRequests, isNot(contains('huangguoai||2')));
      expect(next.items.length, 4);
      expect(next.warning, isEmpty);
    },
  );

  testWidgets(
    'rankings are separate from categories and preserve upstream rank numbers',
    (tester) async {
      final repository = CategoryRepository();
      await mount(tester, repository);
      await tester.tap(find.byKey(const ValueKey('open-rankings')));
      await tester.pumpAndSettle();
      expect(repository.rankRequests, ['hongguo-hot|1|false']);
      expect(find.text('总热播榜'), findsOneWidget);
      await tester.tap(find.text('加载更多'));
      await tester.pumpAndSettle();
      expect(
        find.byKey(const ValueKey('rank-21-hongguo:rank:hongguo-hot:2')),
        findsOneWidget,
      );
      await tester.tap(find.text('真人剧榜'));
      await tester.pumpAndSettle();
      expect(repository.rankRequests.last, 'hongguo-real|1|false');
      expect(
        find.byKey(const ValueKey('rank-21-hongguo:rank:hongguo-hot:2')),
        findsNothing,
      );
      expect(repository.categoryRequests, ['hongguo||1']);
      expect(tester.takeException(), isNull);
    },
  );
}
