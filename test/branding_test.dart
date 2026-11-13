import 'dart:convert';

import 'package:duanju_app/app_build.dart';
import 'package:duanju_app/main.dart';
import 'package:duanju_app/models.dart';
import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:shared_preferences/shared_preferences.dart';

import 'fixtures.dart';

class _BrandRepository extends FixtureRepository {
  @override
  bool get supportsSourceManagement => true;
}

void main() {
  TestWidgetsFlutterBinding.ensureInitialized();

  test('public branding follows the edition', () {
    expect(appName, allSourcesEnabled ? '真果赏' : '红果赏');
    expect(appSlug, allSourcesEnabled ? 'zhenguoshang' : 'hongguoshang');
  });

  test('new backups retain old backup import compatibility', () async {
    SharedPreferences.setMockInitialValues({});
    final store = testStore(await SharedPreferences.getInstance());
    addTearDown(store.dispose);
    final drama = FixtureRepository.free;
    await store.toggleFavorite(drama);
    await store.saveWatch(
      WatchEntry(
        drama: drama,
        episode: 2,
        position: 21,
        duration: 60,
        updatedAt: DateTime(2026, 10, 23),
      ),
    );
    final backup =
        jsonDecode(await store.exportBackup()) as Map<String, dynamic>;
    expect(backup['app'], 'zhenguoshang');
    for (final identity in ['zhenguojian', 'zhenguoshang']) {
      backup['app'] = identity;
      await store.importBackup(jsonEncode(backup));
      expect(store.favorites.single.id, drama.id);
      expect(store.watched(drama.id)?.position, 21);
    }
    backup['app'] = 'unrelated';
    expect(
      () => store.validateBackup(jsonEncode(backup)),
      throwsFormatException,
    );
    expect(store.watched(drama.id)?.position, 21);
  });

  for (final width in [390.0, 1280.0]) {
    testWidgets('resource button is absent at width $width', (tester) async {
      tester.view.physicalSize = Size(width, 900);
      tester.view.devicePixelRatio = 1;
      addTearDown(tester.view.resetPhysicalSize);
      addTearDown(tester.view.resetDevicePixelRatio);
      SharedPreferences.setMockInitialValues({});
      final store = testStore(await SharedPreferences.getInstance());
      addTearDown(store.dispose);
      await tester.pumpWidget(
        DuanjuApp(repository: _BrandRepository(), store: store),
      );
      await tester.pumpAndSettle();
      expect(
        tester.widget<MaterialApp>(find.byType(MaterialApp)).title,
        appName,
      );
      expect(find.byKey(const ValueKey('more-resources')), findsNothing);
      expect(find.byTooltip('更多资源'), findsNothing);
      expect(find.byIcon(Icons.card_giftcard_rounded), findsNothing);
      expect(find.byTooltip('更多'), findsOneWidget);
      expect(find.byKey(const ValueKey('toggle-search')), findsOneWidget);
      await tester.tap(find.byTooltip('更多'));
      await tester.pumpAndSettle();
      expect(find.text('站源管理'), findsOneWidget);
      expect(find.text('设置与备份'), findsOneWidget);
      expect(tester.takeException(), isNull);
      await tester.pumpWidget(const SizedBox.shrink());
    });
  }
}
