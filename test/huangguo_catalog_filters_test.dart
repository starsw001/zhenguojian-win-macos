import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:duanju_app/huangguo_catalog_filters.dart';
import 'package:duanju_app/models.dart';

void main() {
  testWidgets('黄果网站分区展示对应子分类并保留热门标签', (tester) async {
    tester.view.physicalSize = const Size(900, 1400);
    tester.view.devicePixelRatio = 1;
    addTearDown(tester.view.resetPhysicalSize);
    addTearDown(tester.view.resetDevicePixelRatio);
    var selected = 'original';
    const categories = [
      CatalogCategory('ai-duanju', 'AI 短剧'),
      CatalogCategory('ai-manju', 'AI 漫剧'),
      CatalogCategory('original-manju', '原创漫剧'),
      CatalogCategory('original-duanju', '原创短剧'),
      CatalogCategory('ai-duanju-hot', '当前热播'),
      CatalogCategory('ai-duanju-random', '精选推荐'),
      CatalogCategory('ai-manju-hot', '当前热播'),
      CatalogCategory('ai-manju-random', '精选推荐'),
      CatalogCategory('topic:hot-aiduanju', '精品高分AI短剧专题'),
      CatalogCategory('ranks-recommend', '推荐榜'),
      CatalogCategory('ranks-potential', '飙升榜'),
      CatalogCategory('tag:modern', '现代', tag: true),
    ];
    await tester.pumpWidget(
      MaterialApp(
        home: Scaffold(
          body: StatefulBuilder(
            builder: (context, update) => HuangguoCatalogFilters(
              categories: categories,
              category: selected,
              onCategory: (value) => update(() => selected = value),
              onRetry: () {},
            ),
          ),
        ),
      ),
    );

    for (final name in ['全部', '黄果原创', '短剧', '漫剧', '专题', '排行榜']) {
      expect(find.text(name), findsOneWidget);
    }
    for (final name in ['全部原创', '原创漫剧', '原创短剧', '热门标签', '现代']) {
      expect(find.text(name), findsOneWidget);
    }
    expect(find.text('AI 短剧'), findsNothing);
    expect(find.text('AI 漫剧'), findsNothing);

    await tester.tap(find.byKey(const ValueKey('category-ai-duanju')));
    await tester.pumpAndSettle();
    for (final name in ['最近更新', '当前热播', '精选推荐']) {
      expect(find.text(name), findsOneWidget);
    }
    expect(find.text('原创漫剧'), findsNothing);

    await tester.tap(find.byKey(const ValueKey('category-topics')));
    await tester.pumpAndSettle();
    expect(find.text('全部专题'), findsOneWidget);
    expect(find.text('精品高分AI短剧专题'), findsOneWidget);

    await tester.tap(find.byKey(const ValueKey('category-ranks-hot')));
    await tester.pumpAndSettle();
    for (final name in ['热播榜', '推荐榜', '飙升榜']) {
      expect(find.text(name), findsOneWidget);
    }
    expect(tester.takeException(), isNull);
  });
}
