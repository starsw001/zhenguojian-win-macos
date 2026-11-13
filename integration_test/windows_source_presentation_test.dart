import 'dart:convert';
import 'dart:io';
import 'dart:ui' as ui;

import 'package:crypto/crypto.dart';
import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:integration_test/integration_test.dart';
import 'package:path_provider_platform_interface/path_provider_platform_interface.dart';
import 'package:window_manager/window_manager.dart';

import 'package:duanju_app/app_build.dart';
import 'package:duanju_app/catalog_browser.dart';
import 'package:duanju_app/catalog_filters.dart';
import 'package:duanju_app/core_bridge.dart';
import 'package:duanju_app/models.dart';

import 'windows_sources_test.dart' show LiveSourcePaths;

void main() {
  final binding = IntegrationTestWidgetsFlutterBinding.ensureInitialized();
  testWidgets('Windows all-source wrapping and actual cover decoding', (
    tester,
  ) async {
    expect(Platform.isWindows && allSourcesEnabled, isTrue);
    expect(const bool.fromEnvironment('DISABLE_REMOTE_IMAGES'), isFalse);
    const directory = String.fromEnvironment('LIVE_PRESENTATION_DIRECTORY');
    const expectedCore = String.fromEnvironment('LIVE_CORE_SHA256');
    expect(directory, isNotEmpty);
    final core = File(
      '${File(Platform.resolvedExecutable).parent.path}/duanju_core.dll',
    );
    expect((await sha256.bind(core.openRead()).first).toString(), expectedCore);
    final oldPaths = PathProviderPlatform.instance;
    PathProviderPlatform.instance = LiveSourcePaths(
      '$directory/live-native-data',
    );
    await windowManager.ensureInitialized();
    final bounds = await windowManager.getBounds();
    final results = <Map<String, dynamic>>[];
    final report = <String, dynamic>{
      'remoteImagesDisabled': false,
      'nativeCoreSHA256': expectedCore,
      'window': {
        'x': bounds.left,
        'y': bounds.top,
        'width': bounds.width,
        'height': bounds.height,
      },
      'covers': results,
      'completed': false,
    };
    try {
      final native = NativeRepository(background: true);
      await native.initialize();
      final sourceReport =
          jsonDecode(await File('$directory/source-checks.json').readAsString())
              as List;
      for (final entry in sourceReport) {
        final source = Map<String, dynamic>.from(entry as Map);
        for (final raw in source['covers'] as List? ?? const []) {
          final cover = Map<String, dynamic>.from(raw as Map);
          if (cover['ok'] != true) continue;
          final drama = Drama.fromJson(
            Map<String, dynamic>.from(cover['drama'] as Map),
          );
          final item = <String, dynamic>{
            'source': source['source'],
            'dramaId': drama.id,
            'ok': false,
          };
          try {
            final file = await native.cover(drama);
            final codec = await ui.instantiateImageCodec(
              await File(file).readAsBytes(),
              targetWidth: 96,
            );
            final frame = await codec.getNextFrame();
            item.addAll({
              'ok': frame.image.width > 0 && frame.image.height > 0,
              'width': frame.image.width,
              'height': frame.image.height,
            });
            frame.image.dispose();
            codec.dispose();
          } catch (error) {
            item['error'] = error.toString();
          }
          results.add(item);
        }
      }
      final menus = <Map<String, dynamic>>[];
      report['sourceMenus'] = menus;
      for (final entry in sourceReport) {
        final source = Map<String, dynamic>.from(entry as Map);
        if (source['catalogOK'] != true) continue;
        final id = source['source'] as String;
        final browser = CatalogBrowser(native);
        final group = SourceGroup(id, source['name'] as String, [
          SourceSite.byId(id),
        ]);
        expect(await browser.loadCategories(group), isNull);
        final categories = browser.categories(group);
        expect(categories.first.id, isEmpty);
        expect(categories.map((c) => c.name).toSet().length, categories.length);
        await tester.pumpWidget(
          MaterialApp(
            home: Scaffold(
              body: Column(
                children: [
                  CatalogFilters(
                    key: ValueKey('live-$id'),
                    categories: categories,
                    category: '',
                    onCategory: (_) {},
                    onRetry: () {},
                  ),
                  const Expanded(child: Text('真实站源分类')),
                ],
              ),
            ),
          ),
        );
        await tester.pumpAndSettle();
        final positions = [
          for (final category in categories)
            tester
                .getTopLeft(find.byKey(ValueKey('category-${category.id}')))
                .dy,
        ];
        menus.add({
          'source': id,
          'categoryCount': categories.length,
          'renderedRows': positions.toSet().length,
          'ok': tester.takeException() == null,
        });
        expect(menus.last['ok'], isTrue);
      }
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
                  category: 'category-0',
                  onCategory: (_) {},
                  onRetry: () {},
                ),
                const Expanded(child: Text('真实海报已解码，分类自动换行')),
              ],
            ),
          ),
        ),
      );
      await tester.pumpAndSettle();
      final first = tester.getTopLeft(
        find.byKey(const ValueKey('category-category-0')),
      );
      final later = tester.getTopLeft(
        find.byKey(const ValueKey('category-category-30')),
      );
      expect(later.dy, greaterThan(first.dy));
      await tester.ensureVisible(
        find.byKey(const ValueKey('category-category-374')),
      );
      await tester.pumpAndSettle();
      expect(
        find.byKey(const ValueKey('category-category-374')).hitTestable(),
        findsOneWidget,
      );
      expect(results, isNotEmpty);
      expect(results.where((r) => r['ok'] != true), isEmpty);
      report['completed'] = true;
      binding.reportData = report;
    } finally {
      await File('$directory/windows-presentation-result.json').writeAsString(
        const JsonEncoder.withIndent('  ').convert(report),
        flush: true,
      );
      PathProviderPlatform.instance = oldPaths;
    }
  }, timeout: const Timeout(Duration(minutes: 8)));
}
