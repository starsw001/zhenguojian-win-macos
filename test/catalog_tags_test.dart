import 'package:duanju_app/catalog_browser.dart';
import 'package:duanju_app/catalog_filters.dart';
import 'package:duanju_app/core_bridge.dart';
import 'package:duanju_app/models.dart';
import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';

import 'fixtures.dart';

class TagRepository extends FixtureRepository {
  final tagRequests = <String>[];
  bool failTags = false;

  @override
  Future<List<CatalogCategory>> categories(
    String source, {
    bool force = false,
  }) async {
    if (failTags) throw AppFailure('普通标签请求失败');
    return const [
      CatalogCategory.all,
      CatalogCategory('ai-duanju', 'AI 短剧'),
      CatalogCategory('tag:qihuan', '奇幻', tag: true),
      CatalogCategory('tag:dushi', '都市', tag: true),
    ];
  }

  @override
  Future<CatalogPage> cached(String source, {String category = ''}) async =>
      CatalogPage(
        const [
          Drama(
            id: 'huangguoai:old',
            source: 'huangguoai',
            title: '普通旧条目',
            category: 'AI 短剧',
          ),
        ],
        page: 4,
        fresh: true,
        hasMore: true,
      );

  @override
  Future<CatalogPage> catalog(
    String source, {
    int page = 1,
    String query = '',
    String category = '',
    bool force = false,
  }) async {
    tagRequests.add('$category|$page|$force');
    return CatalogPage(
      [Drama(id: '$source:$category:$page', source: source, title: '普通最新条目')],
      page: page,
      hasMore: page == 1,
      fresh: true,
      authoritative: true,
    );
  }
}

void main() {
  test('native category kind and current response marker survive JSON', () {
    expect(
      CatalogCategory.fromJson({
        'id': 'tag:qihuan',
        'name': '奇幻',
        'kind': 'tag',
      }).tag,
      true,
    );
    expect(CatalogCategory.fromJson({'id': '2', 'name': '普通分类'}).tag, false);
    expect(CatalogPage.fromJson({'authoritative': true}).authoritative, true);
    expect(CatalogPage.fromJson({}).authoritative, false);
  });

  test(
    'remote tags replace current rows without erasing cached history',
    () async {
      final repository = TagRepository();
      final browser = CatalogBrowser(repository);
      final group = SourceGroup('huangguoai', '黄果AI', [
        SourceSite.byId('huangguoai'),
      ]);
      await browser.loadCategories(group);
      final tags = browser
          .categories(group)
          .where((entry) => entry.tag)
          .toList();
      expect(tags.map((entry) => entry.name), ['奇幻', '都市']);
      final first = await browser.load(
        group,
        category: tags.first.id,
        useCache: true,
      );
      expect(first.items.map((drama) => drama.title), ['普通最新条目']);
      expect(first.page, 1);
      expect(repository.tagRequests.single, 'tag:qihuan|1|true');
      final more = await browser.load(
        group,
        category: tags.first.id,
        more: true,
      );
      expect(more.items.length, 2);
      expect(more.page, 2);
      expect(more.hasMore, false);
      expect(repository.tagRequests.last, 'tag:qihuan|2|false');
      await browser.load(group, category: tags.last.id);
      expect(repository.tagRequests.last, 'tag:dushi|1|true');
      repository.failTags = true;
      expect(await browser.loadCategories(group, force: true), isNotNull);
      expect(browser.categories(group).where((entry) => entry.tag).length, 2);
    },
  );

  testWidgets('popular tags start below categories and wrap at narrow width', (
    tester,
  ) async {
    final selections = <String>[];
    final tags = [
      for (var index = 0; index < 18; index++)
        CatalogCategory('tag:$index', '普通标签$index', tag: true),
    ];
    await tester.pumpWidget(
      MaterialApp(
        home: Scaffold(
          body: SizedBox(
            width: 360,
            child: CatalogFilters(
              categories: [
                CatalogCategory.all,
                const CatalogCategory('ai-duanju', 'AI 短剧'),
                ...tags,
              ],
              category: 'tag:0',
              onCategory: selections.add,
              onRetry: () {},
            ),
          ),
        ),
      ),
    );
    final main = find.byKey(const ValueKey('category-ai-duanju'));
    final first = find.byKey(const ValueKey('category-tag:0'));
    final fourth = find.byKey(const ValueKey('category-tag:4'));
    expect(
      tester.getTopLeft(first).dy,
      greaterThan(tester.getBottomLeft(main).dy),
    );
    expect(
      tester.getTopLeft(fourth).dy,
      greaterThan(tester.getTopLeft(first).dy),
    );
    await tester.tap(first);
    expect(selections.last, '');
    await tester.tap(find.byKey(const ValueKey('category-tag:1')));
    expect(selections.last, 'tag:1');
    expect(tester.takeException(), isNull);
  });

  testWidgets('a source without tags has no extra tag row', (tester) async {
    await tester.pumpWidget(
      MaterialApp(
        home: Scaffold(
          body: CatalogFilters(
            categories: const [CatalogCategory.all],
            category: '',
            onCategory: (_) {},
            onRetry: () {},
          ),
        ),
      ),
    );
    expect(find.byKey(const ValueKey('catalog-popular-tags')), findsNothing);
  });
}
