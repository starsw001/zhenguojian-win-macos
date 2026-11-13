import 'package:duanju_app/app_build.dart';
import 'package:duanju_app/main.dart';
import 'package:duanju_app/models.dart';
import 'package:duanju_app/sources_screen.dart';
import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:shared_preferences/shared_preferences.dart';

import 'fixtures.dart';
import 'catalog_categories_test.dart' show CategoryRepository;
import 'sources_test.dart' show SourceFixtureRepository;

void main() {
  const ids = ['huangguo-video', 'huangguoai', 'cloudfront'];
  const names = ['黄果视频', '黄果AI', '黄果旧版'];

  test(
    'all 23 source identities remain separate without changing stored IDs',
    () {
      final groups = SourceGroup.fromSources(SourceSite.knownValues);
      expect(groups.length, 23);
      expect(groups.every((group) => group.sources.length == 1), isTrue);
      expect(groups.any((group) => group.id == 'huangguo'), isFalse);
      for (var i = 0; i < ids.length; i++) {
        final group = groups.singleWhere((group) => group.id == ids[i]);
        expect(group.name, names[i]);
        expect(group.sources.single.id, ids[i]);
      }
      expect(
        SourceGroup.fromSources(SourceSite.values).length,
        allSourcesEnabled ? 23 : 1,
      );
    },
  );

  for (var i = 0; i < ids.length; i++) {
    final id = ids[i], name = names[i];
    testWidgets('$name only requests its own catalog and categories', (
      tester,
    ) async {
      SharedPreferences.setMockInitialValues({});
      final store = testStore(await SharedPreferences.getInstance());
      await store.setSource(id);
      final repository = CategoryRepository();
      await tester.pumpWidget(DuanjuApp(repository: repository, store: store));
      await tester.pumpAndSettle();
      expect(find.text(name), findsWidgets);
      expect(repository.categoryRequests, ['$id||1']);
      expect(find.text('黄果'), findsNothing);
      await tester.tap(find.byKey(const ValueKey('source-switch')));
      await tester.pumpAndSettle();
      for (final label in names) {
        expect(find.text(label), findsWidgets);
      }
      await tester.tapAt(const Offset(5, 5));
      await tester.pumpAndSettle();
      await tester.pumpWidget(const SizedBox.shrink());
      await tester.pumpAndSettle();
      store.dispose();
      expect(tester.takeException(), isNull);
    }, skip: !allSourcesEnabled);

    testWidgets('$name has an independent management card and hide state', (
      tester,
    ) async {
      SharedPreferences.setMockInitialValues({});
      final store = testStore(await SharedPreferences.getInstance());
      final repository = SourceFixtureRepository();
      await tester.pumpWidget(
        MaterialApp(
          home: SourcesScreen(
            repository: repository,
            store: store,
            initialSource: id,
          ),
        ),
      );
      await tester.pumpAndSettle();
      expect(
        find.byKey(const PageStorageKey('source-group-huangguo')),
        findsNothing,
      );
      expect(find.byKey(ValueKey('source-$id')), findsOneWidget);
      await tester.tap(find.byKey(ValueKey('source-more-$id')));
      await tester.pumpAndSettle();
      await tester.tap(find.text('隐藏').last);
      await tester.pumpAndSettle();
      expect(store.isSourceHidden(id), isTrue);
      for (final other in ids.where((source) => source != id)) {
        expect(store.isSourceHidden(other), isFalse);
      }
      expect(find.byKey(ValueKey('source-$id')), findsOneWidget);
      expect(repository.operations, isEmpty);
      await tester.pumpWidget(const SizedBox.shrink());
      await tester.pumpAndSettle();
      store.dispose();
      expect(tester.takeException(), isNull);
    }, skip: !allSourcesEnabled);
  }
}
