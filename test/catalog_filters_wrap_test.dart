import 'package:duanju_app/catalog_filters.dart';
import 'package:duanju_app/models.dart';
import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';

void main() {
  for (final width in [320.0, 800.0, 1280.0]) {
    testWidgets('375 categories wrap and remain reachable at $width', (
      tester,
    ) async {
      tester.view.physicalSize = Size(width, 720);
      tester.view.devicePixelRatio = 1;
      addTearDown(tester.view.reset);
      var selected = '';
      await tester.pumpWidget(
        MaterialApp(
          home: Scaffold(
            body: Column(
              children: [
                CatalogFilters(
                  categories: [
                    for (var i = 0; i < 375; i++)
                      CatalogCategory('category-$i', '分类 $i'),
                  ],
                  category: selected,
                  onCategory: (id) => selected = id,
                  onRetry: () {},
                ),
                const Expanded(child: Text('剧集内容')),
              ],
            ),
          ),
        ),
      );
      final first = find.byKey(const ValueKey('category-category-0'));
      final middle = find.byKey(const ValueKey('category-category-30'));
      expect(
        tester.getTopLeft(middle).dy,
        greaterThan(tester.getTopLeft(first).dy),
      );
      final last = find.byKey(const ValueKey('category-category-374'));
      await tester.ensureVisible(last);
      await tester.pumpAndSettle();
      await tester.tap(last);
      expect(selected, 'category-374');
      expect(tester.getSize(find.text('剧集内容')).height, greaterThan(0));
      expect(tester.takeException(), isNull);
    });
  }

  testWidgets(
    'category selection scrolls vertically and controls keep working',
    (tester) async {
      tester.view.physicalSize = const Size(480, 600);
      tester.view.devicePixelRatio = 1;
      addTearDown(tester.view.reset);
      var selection = 'category-0';
      var retries = 0;
      var actions = 0;
      late StateSetter update;
      await tester.pumpWidget(
        MaterialApp(
          home: Scaffold(
            body: StatefulBuilder(
              builder: (context, setState) {
                update = setState;
                return Column(
                  children: [
                    CatalogFilters(
                      categories: [
                        for (var i = 0; i < 375; i++)
                          CatalogCategory('category-$i', '分类 $i'),
                      ],
                      category: selection,
                      error: '重新读取分类',
                      onCategory: (_) {},
                      onRetry: () => retries++,
                      trailing: IconButton(
                        icon: const Icon(Icons.star),
                        onPressed: () => actions++,
                      ),
                    ),
                    const Expanded(child: Text('内容')),
                  ],
                );
              },
            ),
          ),
        ),
      );
      update(() => selection = 'category-374');
      await tester.pumpAndSettle();
      expect(
        find.byKey(const ValueKey('category-category-374')).hitTestable(),
        findsOneWidget,
      );
      await tester.tap(find.byTooltip('重新读取分类'));
      await tester.tap(find.byIcon(Icons.star));
      expect(retries, 1);
      expect(actions, 1);
      expect(tester.takeException(), isNull);
    },
  );
}
