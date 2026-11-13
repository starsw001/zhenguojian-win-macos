import 'dart:async';

import 'package:duanju_app/catalog_browser.dart';
import 'package:duanju_app/core_bridge.dart';
import 'package:duanju_app/home_screen.dart';
import 'package:duanju_app/hongguo_catalog_filters.dart';
import 'package:duanju_app/models.dart';
import 'package:duanju_app/ranking_models.dart';
import 'package:duanju_app/rankings_screen.dart';
import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:shared_preferences/shared_preferences.dart';

import 'fixtures.dart';

class WebNavigationRepository extends FixtureRepository {
  final webRequests = <String>[];
  final boardRequests = <String>[];
  bool failSecondRankingPage = false;
  Completer<RankingPage>? delayedHot;

  @override
  Future<List<CatalogCategory>> categories(
    String source, {
    bool force = false,
  }) async => [
    CatalogCategory.all,
    ...hongguoWebCategories,
    const CatalogCategory('web:real-drama:romance', '爱情'),
    const CatalogCategory('web:real-drama:fantasy', '玄幻'),
    const CatalogCategory('web:comic-drama:fantasy', '玄幻'),
    const CatalogCategory('web:ai-drama:fantasy', '玄幻'),
    const CatalogCategory('web:ai-drama:creative', '脑洞'),
  ];

  @override
  Future<CatalogPage> catalog(
    String source, {
    int page = 1,
    String query = '',
    String category = '',
    bool force = false,
  }) async {
    webRequests.add('$category|$page');
    return CatalogPage(
      [
        Drama(
          id: '$source:$category:$page',
          source: source,
          title: '网页目录 $category 第$page页',
          episodes: 8,
        ),
      ],
      page: page,
      hasMore: page < 3,
      authoritative: true,
    );
  }

  @override
  Future<List<RankingBoard>> rankingBoards() async => const [
    RankingBoard(id: 'hongguo-hot', source: 'hongguo', name: '红果热播榜'),
    RankingBoard(id: 'hongguo-real', source: 'hongguo', name: '真人剧热播榜'),
    RankingBoard(id: 'hongguo-ai', source: 'hongguo', name: 'AI剧热播榜'),
    RankingBoard(id: 'hongguo-comic', source: 'hongguo', name: '漫剧热播榜'),
    RankingBoard(id: 'other-board', source: 'hanxiaoquan', name: '其他站源榜单'),
  ];

  RankingPage rankingResult(String board, int page) => RankingPage(
    items: [
      for (var i = 1; i <= 20; i++)
        RankingItem(
          (page - 1) * 20 + i,
          Drama(
            id: 'hongguo:$board:$page:$i',
            source: 'hongguo',
            title: '$board 第$page页 第$i部',
            episodes: 8,
          ),
          metric: '${100 - i}万热度',
        ),
    ],
    page: page,
    hasMore: page == 1,
    updatedText: '本轮网页榜单',
  );

  @override
  Future<RankingPage> rankings(
    String board, {
    int page = 1,
    bool force = false,
  }) async {
    boardRequests.add('$board|$page|$force');
    if (board == 'hongguo-hot' && page == 1 && delayedHot != null) {
      return delayedHot!.future;
    }
    if (page == 2 && failSecondRankingPage) throw AppFailure('分集以外的榜单续页失败');
    return rankingResult(board, page);
  }
}

void main() {
  test(
    'first web page is requested before category menus finish loading',
    () async {
      final repository = WebNavigationRepository();
      final browser = CatalogBrowser(repository);
      const group = SourceGroup('hongguo', '红果', [SourceSite.hongguo]);
      await browser.load(group, category: 'web:comic');
      expect(repository.webRequests, ['web:comic|1']);
      expect(hongguoWebSelection('comic_series'), 'web:comic-drama');
      expect(hongguoWebSelection(null), 'web:real-drama');
    },
  );

  test(
    'each content type and identically named topic has its own cursor',
    () async {
      final repository = WebNavigationRepository();
      final browser = CatalogBrowser(repository);
      const group = SourceGroup('hongguo', '红果', [SourceSite.hongguo]);
      await browser.loadCategories(group);
      await browser.load(group, category: 'web:real-drama');
      await browser.load(group, category: 'web:comic-drama');
      await browser.load(group, category: 'web:real-drama', more: true);
      await browser.load(group, category: 'web:comic-drama:fantasy');
      await browser.load(group, category: 'web:ai-drama:fantasy');
      expect(repository.webRequests, [
        'web:real-drama|1',
        'web:comic-drama|1',
        'web:real-drama|2',
        'web:comic-drama:fantasy|1',
        'web:ai-drama:fantasy|1',
      ]);
    },
  );

  testWidgets('four root types and only current type topics are displayed', (
    tester,
  ) async {
    var selected = 'web:real-drama';
    final categories = await WebNavigationRepository().categories('hongguo');
    await tester.pumpWidget(
      MaterialApp(
        home: Scaffold(
          body: StatefulBuilder(
            builder: (context, update) => HongguoCatalogFilters(
              categories: categories,
              category: selected,
              onCategory: (value) => update(() => selected = value),
              onRetry: () {},
            ),
          ),
        ),
      ),
    );
    for (final name in ['真人剧', '漫剧', 'AI剧', '漫画']) {
      expect(find.text(name), findsOneWidget);
    }
    expect(find.text('爱情'), findsOneWidget);
    expect(find.text('脑洞'), findsNothing);
    await tester.tap(find.byKey(const ValueKey('category-web:ai-drama')));
    await tester.pumpAndSettle();
    expect(find.text('爱情'), findsNothing);
    expect(find.text('脑洞'), findsOneWidget);
    expect(
      find.byKey(const ValueKey('category-web:ai-drama:fantasy')),
      findsOneWidget,
    );
    await tester.tap(find.byKey(const ValueKey('category-web:comic')));
    await tester.pumpAndSettle();
    expect(find.text('玄幻'), findsNothing);
    expect(find.text('全部'), findsOneWidget);
    expect(tester.takeException(), isNull);
  });

  testWidgets(
    'home has adjacent category and ranking sections with four boards',
    (tester) async {
      SharedPreferences.setMockInitialValues({});
      final store = testStore(await SharedPreferences.getInstance());
      final repository = WebNavigationRepository();
      await tester.pumpWidget(
        MaterialApp(
          home: HomeScreen(repository: repository, store: store),
        ),
      );
      await tester.pumpAndSettle();
      expect(repository.webRequests.first, 'web:real-drama|1');
      expect(
        find.byKey(const ValueKey('hongguo-section-categories')),
        findsOneWidget,
      );
      await tester.tap(find.byKey(const ValueKey('hongguo-section-rankings')));
      await tester.pumpAndSettle();
      for (final name in ['红果热播榜', '真人剧热播榜', 'AI剧热播榜', '漫剧热播榜']) {
        expect(find.text(name), findsOneWidget);
      }
      expect(find.text('其他站源榜单'), findsNothing);
      await tester.tap(find.text('AI剧热播榜'));
      await tester.pumpAndSettle();
      expect(repository.boardRequests.last, 'hongguo-ai|1|false');
      await tester.tap(
        find.byKey(const ValueKey('hongguo-section-categories')),
      );
      await tester.pumpAndSettle();
      expect(find.byType(HongguoCatalogFilters), findsOneWidget);
      expect(tester.takeException(), isNull);
      await tester.pumpWidget(const SizedBox());
    },
  );

  testWidgets('late old ranking response cannot populate a different board', (
    tester,
  ) async {
    SharedPreferences.setMockInitialValues({});
    final store = testStore(await SharedPreferences.getInstance());
    final repository = WebNavigationRepository()
      ..delayedHot = Completer<RankingPage>();
    await tester.pumpWidget(
      MaterialApp(
        home: Scaffold(
          body: RankingsScreen(
            repository: repository,
            store: store,
            initialGroup: 'hongguo',
            embedded: true,
          ),
        ),
      ),
    );
    await tester.pump();
    await tester.tap(find.text('AI剧热播榜'));
    await tester.pumpAndSettle();
    repository.delayedHot!.complete(repository.rankingResult('hongguo-hot', 1));
    await tester.pumpAndSettle();
    expect(find.text('hongguo-ai 第1页 第1部'), findsOneWidget);
    expect(find.text('hongguo-hot 第1页 第1部'), findsNothing);
    await tester.pumpWidget(const SizedBox());
  });

  testWidgets(
    'failed ranking continuation does not automatically retry while scrolling',
    (tester) async {
      SharedPreferences.setMockInitialValues({});
      final store = testStore(await SharedPreferences.getInstance());
      final repository = WebNavigationRepository()
        ..failSecondRankingPage = true;
      await tester.pumpWidget(
        MaterialApp(
          home: RankingsScreen(
            repository: repository,
            store: store,
            initialGroup: 'hongguo',
          ),
        ),
      );
      await tester.pumpAndSettle();
      await tester.drag(find.byType(ListView), const Offset(0, -6000));
      await tester.pumpAndSettle();
      final attempted = repository.boardRequests
          .where((v) => v.startsWith('hongguo-hot|2|'))
          .length;
      expect(attempted, 1);
      await tester.drag(find.byType(ListView), const Offset(0, -500));
      await tester.pumpAndSettle();
      expect(
        repository.boardRequests
            .where((v) => v.startsWith('hongguo-hot|2|'))
            .length,
        attempted,
      );
      expect(tester.takeException(), isNull);
      await tester.pumpWidget(const SizedBox());
    },
  );
}
