import 'package:duanju_app/app_build.dart';
import 'package:duanju_app/catalog_browser.dart';
import 'package:duanju_app/home_screen.dart';
import 'package:duanju_app/library_updater.dart';
import 'package:duanju_app/models.dart';
import 'package:duanju_app/source_status.dart';
import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:shared_preferences/shared_preferences.dart';

import 'fixtures.dart';

class CompleteIndexRepository extends FixtureRepository {
  bool finished = false;
  bool warnWhileFinished = false;
  final queries = <String>[];

  @override
  bool get supportsSourceManagement => true;

  @override
  Future<SourceStatus> sourceStatus(String source) async =>
      SourceStatus.fromJson({
        'source': source,
        if (source == 'huangguoai') ...{
          'operation': 'refreshAll',
          'running': !finished,
          'stage': finished ? '已完成' : '全量目录更新中',
          'completed': finished ? 40 : 1,
          'count': finished ? 960 : 24,
          'startedAt': '2026-10-26T10:00:00Z',
          'updatedAt': finished
              ? '2026-10-26T10:00:40Z'
              : '2026-10-26T10:00:01Z',
          if (finished) 'finishedAt': '2026-10-26T10:00:40Z',
        },
      });

  @override
  Future<List<CatalogCategory>> categories(
    String source, {
    bool force = false,
  }) async => const [CatalogCategory.all];

  @override
  Future<List<CatalogCategory>> cachedCategories(String source) async =>
      finished
      ? const [
          CatalogCategory.all,
          CatalogCategory('tag:qihuan', '奇幻', tag: true),
        ]
      : const [];

  @override
  Future<CatalogPage> cached(String source, {String category = ''}) async =>
      CatalogPage([
        Drama(id: '$source:front', source: source, title: '普通首页剧集'),
      ], fresh: true);

  @override
  Future<CatalogPage> catalog(
    String source, {
    int page = 1,
    String query = '',
    String category = '',
    bool force = false,
  }) async {
    if (query.isEmpty) {
      return cached(source);
    }
    queries.add(query);
    return CatalogPage(
      [
        if (finished)
          Drama(id: '$source:last', source: source, title: '普通末页新剧'),
      ],
      localSearch: true,
      page: page,
      warning: finished && !warnWhileFinished ? '' : '全量目录更新中，当前结果可能不完整',
    );
  }
}

void main() {
  test('permanent entries and publication pages have distinct labels', () {
    expect(
      SourceMirrorPublication.fromJson({
        'url': 'https://huangguoai.com/',
      }).kindLabel,
      '永久入口',
    );
    expect(
      SourceMirrorPublication.fromJson({
        'url': 'https://huangguoai.ai',
      }).kindLabel,
      '永久入口',
    );
    expect(
      SourceMirrorPublication.fromJson({
        'url': 'https://huangguoai.pages.dev',
      }).kindLabel,
      '地址发布页',
    );
  });
  test('AI searches the paginated index rather than only the visible page', () {
    final source = SourceSite.byId('huangguoai');
    expect(source.onlineSearch, true);
    expect(source.pagedSearch, true);
  });

  test(
    'incomplete indexed search warnings do not create endless next pages',
    () async {
      final repo = CompleteIndexRepository()
        ..finished = true
        ..warnWhileFinished = true;
      final browser = CatalogBrowser(repo);
      final result = await browser.load(
        SourceGroup('huangguoai', '黄果AI', [SourceSite.byId('huangguoai')]),
        query: '普通末页新剧',
      );
      expect(result.items.single.title, '普通末页新剧');
      expect(result.hasMore, false);
      expect(result.warning, isNotEmpty);
      expect(repo.queries, ['普通末页新剧']);
    },
  );

  test(
    'full-index progress and completion invalidate search results',
    () async {
      SharedPreferences.setMockInitialValues(await gatePreferences());
      final store = testStore(await SharedPreferences.getInstance());
      await unlockGate(store);
      final repository = CompleteIndexRepository();
      final delivered = <String>[];
      final updater = LibraryUpdater(
        repository,
        store,
        onCatalogChanged: delivered.add,
      );
      await updater.refresh();
      repository.finished = true;
      await updater.refresh();
      expect(delivered.where((source) => source == 'huangguoai').length, 2);
      updater.dispose();
      store.dispose();
    },
    skip: !allSourcesEnabled,
  );

  testWidgets(
    'an open search is completed automatically by the full-index job',
    (tester) async {
      SharedPreferences.setMockInitialValues(await gatePreferences());
      final store = testStore(await SharedPreferences.getInstance());
      await unlockGate(store);
      await store.setSource('huangguoai');
      final repository = CompleteIndexRepository();
      await tester.pumpWidget(
        MaterialApp(
          home: HomeScreen(repository: repository, store: store),
        ),
      );
      await tester.pump(const Duration(milliseconds: 300));
      expect(find.textContaining('全量目录更新中'), findsOneWidget);
      await tester.tap(find.byKey(const ValueKey('toggle-search')));
      await tester.pump(const Duration(milliseconds: 300));
      await tester.enterText(find.byType(TextField), '普通末页新剧');
      await tester.testTextInput.receiveAction(TextInputAction.search);
      await tester.pump(const Duration(milliseconds: 300));
      expect(repository.queries, contains('普通末页新剧'));
      final resultTitle = find.byWidgetPredicate(
        (widget) => widget is Text && widget.data == '普通末页新剧',
      );
      expect(resultTitle, findsNothing);
      repository.finished = true;
      await tester.pump(const Duration(seconds: 2));
      await tester.pump(const Duration(milliseconds: 200));
      await tester.pump(const Duration(milliseconds: 300));
      expect(resultTitle, findsOneWidget);
      expect(find.text('奇幻'), findsOneWidget);
      expect(tester.takeException(), isNull);
      await tester.pumpWidget(const SizedBox.shrink());
      store.dispose();
    },
    skip: !allSourcesEnabled,
  );
}
