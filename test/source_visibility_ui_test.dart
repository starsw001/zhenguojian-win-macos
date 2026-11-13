import 'dart:async';

import 'package:duanju_app/app_build.dart';
import 'package:duanju_app/local_store.dart';
import 'package:duanju_app/main.dart';
import 'package:duanju_app/models.dart';
import 'package:duanju_app/ranking_models.dart';
import 'package:duanju_app/rankings_screen.dart';
import 'package:duanju_app/recommendations_screen.dart';
import 'package:duanju_app/saved_library.dart';
import 'package:duanju_app/source_status.dart';
import 'package:duanju_app/sources_screen.dart';
import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:shared_preferences/shared_preferences.dart';

import 'fixtures.dart';
import 'sources_test.dart' show SourceFixtureRepository;

class VisibilityRepository extends FixtureRepository {
  Completer<CatalogPage>? delayed;

  @override
  Future<CatalogPage> catalog(
    String source, {
    int page = 1,
    String query = '',
    String category = '',
    bool force = false,
  }) async {
    requests.add(source);
    if (source == 'sorani' && delayed != null) return delayed!.future;
    return CatalogPage(
      [
        Drama(
          id: '$source:visible',
          source: source,
          title: '界面样本-$source',
          episodes: 2,
        ),
      ],
      page: 1,
      hasMore: false,
    );
  }
}

class DiscoveryVisibilityRepository extends VisibilityRepository {
  int recommendationRequests = 0;

  @override
  Future<CatalogPage> recommendations(
    String genre, {
    bool more = false,
    bool force = false,
  }) async {
    recommendationRequests++;
    return CatalogPage([
      const Drama(
        id: 'hongguo:recommend',
        source: 'hongguo',
        title: '可隐藏的推荐样本',
      ),
    ]);
  }

  @override
  Future<List<RankingBoard>> rankingBoards() async => [
    const RankingBoard(id: 'hongguo-hot', source: 'hongguo', name: '热榜'),
    if (allSourcesEnabled)
      const RankingBoard(id: 'sorani-fixture', source: 'sorani', name: '合成榜'),
  ];

  @override
  Future<RankingPage> rankings(
    String board, {
    int page = 1,
    bool force = false,
  }) async => RankingPage(
    items: [
      RankingItem(
        1,
        Drama(
          id: '$board:rank',
          source: board == 'hongguo-hot' ? 'hongguo' : 'sorani',
          title: '榜单样本-$board',
        ),
      ),
    ],
  );
}

void main() {
  Future<LocalStore> create(WidgetTester tester) async {
    tester.view.physicalSize = const Size(1280, 840);
    tester.view.devicePixelRatio = 1;
    addTearDown(tester.view.reset);
    SharedPreferences.setMockInitialValues({});
    final store = testStore(await SharedPreferences.getInstance());
    addTearDown(store.dispose);
    return store;
  }

  Future<void> close(WidgetTester tester) async {
    await tester.pumpWidget(const SizedBox.shrink());
    await tester.pumpAndSettle();
  }

  testWidgets(
    'every compiled source can be hidden and restored even with failed or busy status',
    (tester) async {
      final store = await create(tester);
      final repository = SourceFixtureRepository();
      for (final source in SourceSite.values) {
        repository.statuses[source.id] = SourceStatus.fromJson({
          'source': source.id,
          'count': 3,
          'running': true,
          'total': 3,
          'completed': 1,
          'error': '保留的失败记录',
        });
        await tester.pumpWidget(
          MaterialApp(
            home: SourcesScreen(
              key: ValueKey(source.id),
              repository: repository,
              store: store,
              initialSource: source.id,
            ),
          ),
        );
        await tester.pumpAndSettle();
        final more = find.byKey(ValueKey('source-more-${source.id}'));
        await tester.tap(more);
        await tester.pumpAndSettle();
        await tester.tap(find.text('隐藏').last);
        await tester.pumpAndSettle();
        expect(store.isSourceHidden(source.id), isTrue, reason: source.id);
        expect(find.byKey(ValueKey('source-${source.id}')), findsOneWidget);
        expect(find.textContaining('已隐藏 · 前端不展示'), findsWidgets);
        await tester.tap(more);
        await tester.pumpAndSettle();
        await tester.tap(find.text('取消隐藏').last);
        await tester.pumpAndSettle();
        expect(store.isSourceHidden(source.id), isFalse, reason: source.id);
        expect(store.allowsSource(source.id), isTrue);
      }
      expect(repository.operations, isEmpty);
      await close(tester);
    },
  );

  testWidgets(
    'all hidden sources show a source-manager recovery action, including red edition',
    (tester) async {
      final store = await create(tester);
      for (final source in SourceSite.values) {
        await store.setSourceHidden(source.id, true);
      }
      final repository = VisibilityRepository();
      await tester.pumpWidget(DuanjuApp(repository: repository, store: store));
      await tester.pumpAndSettle();
      expect(find.text('暂无显示站源'), findsWidgets);
      expect(find.text('站源已全部隐藏，请到站源管理取消隐藏。'), findsOneWidget);
      expect(repository.requests, isEmpty);
      await tester.tap(find.widgetWithText(FilledButton, '站源管理'));
      await tester.pumpAndSettle();
      await tester.tap(find.byKey(const ValueKey('source-more-hongguo')));
      await tester.pumpAndSettle();
      await tester.tap(find.text('取消隐藏').last);
      await tester.pumpAndSettle();
      Navigator.of(tester.element(find.byType(SourcesScreen))).pop();
      await tester.pumpAndSettle();
      expect(find.text('界面样本-hongguo'), findsOneWidget);
      expect(store.visibleSources.map((source) => source.id), ['hongguo']);
      expect(tester.takeException(), isNull);
      await close(tester);
    },
  );

  testWidgets(
    'saved-library display hides records without removing the underlying data',
    (tester) async {
      final store = await create(tester);
      await store.toggleFavorite(FixtureRepository.free);
      await store.saveWatch(
        WatchEntry(
          drama: FixtureRepository.free,
          episode: 1,
          position: 5,
          duration: 60,
          updatedAt: DateTime(2026, 10, 26),
        ),
      );
      for (final history in [false, true]) {
        await store.setSourceHidden('hongguo', true);
        await tester.pumpWidget(
          MaterialApp(
            home: Scaffold(
              body: SavedLibrary(
                repository: FixtureRepository(),
                store: store,
                history: history,
                onOpen: (_) {},
                onContinue: (_) {},
              ),
            ),
          ),
        );
        await tester.pumpAndSettle();
        expect(find.byKey(const ValueKey('saved-hongguo:100')), findsNothing);
        expect(store.favorites.single.id, FixtureRepository.free.id);
        expect(store.history.single.drama.id, FixtureRepository.free.id);
        await store.setSourceHidden('hongguo', false);
        await tester.pumpAndSettle();
        expect(find.byKey(const ValueKey('saved-hongguo:100')), findsOneWidget);
      }
      await close(tester);
    },
  );

  testWidgets(
    'recommendations respect hidden state before loading and after manual restoration',
    (tester) async {
      final store = await create(tester);
      await store.setSourceHidden('hongguo', true);
      final repository = DiscoveryVisibilityRepository();
      await tester.pumpWidget(
        MaterialApp(
          home: RecommendationsScreen(repository: repository, store: store),
        ),
      );
      await tester.pumpAndSettle();
      expect(repository.recommendationRequests, 0);
      expect(find.text('可隐藏的推荐样本'), findsNothing);
      await store.setSourceHidden('hongguo', false);
      await tester.pumpAndSettle();
      expect(repository.recommendationRequests, 1);
      expect(find.text('可隐藏的推荐样本'), findsOneWidget);
      await store.setSourceHidden('hongguo', true);
      await tester.pumpAndSettle();
      expect(find.text('可隐藏的推荐样本'), findsNothing);
      await close(tester);
    },
  );

  testWidgets(
    'rankings remove hidden source choices and restore them only after unhide',
    (tester) async {
      final store = await create(tester);
      final repository = DiscoveryVisibilityRepository();
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
      expect(find.text('榜单样本-hongguo-hot'), findsOneWidget);
      await store.setSourceHidden('hongguo', true);
      await tester.pumpAndSettle();
      expect(find.text('榜单样本-hongguo-hot'), findsNothing);
      expect(find.text('红果榜单'), findsNothing);
      await store.setSourceHidden('hongguo', false);
      await tester.pumpAndSettle();
      expect(find.text('榜单样本-hongguo-hot'), findsOneWidget);
      await close(tester);
    },
  );

  if (allSourcesEnabled) {
    testWidgets(
      'a hidden selected source is absent from first render and the switcher after reopening',
      (tester) async {
        final store = await create(tester);
        await store.setSource('sorani');
        await store.setSourceHidden('sorani', true);
        final reopened = testStore(store.preferences);
        addTearDown(reopened.dispose);
        final repository = VisibilityRepository();
        await tester.pumpWidget(
          DuanjuApp(repository: repository, store: reopened),
        );
        await tester.pumpAndSettle();
        expect(repository.requests, isNot(contains('sorani')));
        await tester.tap(find.byKey(const ValueKey('source-switch')));
        await tester.pumpAndSettle();
        expect(find.text('青空'), findsNothing);
        expect(find.text('红果'), findsWidgets);
        await tester.tapAt(const Offset(1100, 700));
        await tester.pumpAndSettle();
        await close(tester);
      },
    );

    testWidgets(
      'aggregate catalog and search stop requesting a newly hidden source',
      (tester) async {
        final store = await create(tester);
        final repository = VisibilityRepository();
        await tester.pumpWidget(
          DuanjuApp(repository: repository, store: store),
        );
        await tester.pumpAndSettle();
        await tester.tap(find.byKey(const ValueKey('source-switch')));
        await tester.pumpAndSettle();
        await tester.tap(find.text('全部站源').last);
        await tester.pumpAndSettle();
        expect(repository.requests, contains('sorani'));
        final before = repository.requests.length;
        await store.setSourceHidden('sorani', true);
        await tester.pumpAndSettle();
        expect(repository.requests.skip(before), isNot(contains('sorani')));
        expect(find.text('界面样本-sorani'), findsNothing);
        final searchBefore = repository.requests.length;
        await tester.tap(find.byKey(const ValueKey('toggle-search')));
        await tester.pumpAndSettle();
        await tester.enterText(find.byType(TextField).first, '界面样本');
        await tester.testTextInput.receiveAction(TextInputAction.search);
        await tester.pumpAndSettle();
        expect(
          repository.requests.skip(searchBefore),
          isNot(contains('sorani')),
        );
        await close(tester);
      },
    );

    testWidgets(
      'late catalog responses cannot restore a hidden source to the frontend',
      (tester) async {
        final store = await create(tester);
        await store.setSource('sorani');
        final delayed = Completer<CatalogPage>();
        final repository = VisibilityRepository()..delayed = delayed;
        await tester.pumpWidget(
          DuanjuApp(repository: repository, store: store),
        );
        await tester.pump(const Duration(milliseconds: 100));
        await store.setSourceHidden('sorani', true);
        await tester.pumpAndSettle();
        delayed.complete(
          CatalogPage([
            const Drama(
              id: 'sorani:late',
              source: 'sorani',
              title: '不得重新显示的旧响应',
            ),
          ]),
        );
        await tester.pumpAndSettle();
        expect(find.text('不得重新显示的旧响应'), findsNothing);
        expect(find.text('界面样本-hongguo'), findsOneWidget);
        expect(tester.takeException(), isNull);
        await close(tester);
      },
    );

    testWidgets('Huangguo group keeps only its non-hidden entries', (
      tester,
    ) async {
      final store = await create(tester);
      await store.setSource('huangguoai');
      await store.setSourceHidden('huangguoai', true);
      await store.setSourceHidden('huangguo-video', true);
      final repository = VisibilityRepository();
      await store.setSource('cloudfront');
      await tester.pumpWidget(DuanjuApp(repository: repository, store: store));
      await tester.pumpAndSettle();
      expect(repository.requests, contains('cloudfront'));
      expect(repository.requests, isNot(contains('huangguoai')));
      expect(repository.requests, isNot(contains('huangguo-video')));
      await close(tester);
    });
  }
}
