import 'dart:async';
import 'dart:io';
import 'dart:ui' as ui;

import 'package:duanju_app/app_theme.dart';
import 'package:duanju_app/downloads_screen.dart';
import 'package:duanju_app/local_profiles.dart';
import 'package:duanju_app/local_store.dart';
import 'package:duanju_app/models.dart';
import 'package:duanju_app/resource_settings_screen.dart';
import 'package:flutter/material.dart';
import 'package:flutter/rendering.dart';
import 'package:flutter/services.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:shared_preferences/shared_preferences.dart';

import 'interface_fixtures.dart';

const toolbarLabels = [
  '筛选下载合集',
  '多选下载任务',
  '本地媒体与合并',
  '排队与下载限速',
  '全部暂停',
  '全部继续',
  '清理已完成任务，保留视频',
  '刷新记录',
];

class ToolbarRepository extends InterfaceRepository {
  int reads = 0;
  Completer<void>? pending;
  @override
  Future<List<DownloadJob>> downloads() async {
    reads++;
    return super.downloads();
  }

  @override
  Future<void> controlDownloads(String command, {String id = ''}) async {
    await pending?.future;
    return super.controlDownloads(command, id: id);
  }
}

class ToolbarMemberStore extends LocalStore {
  ToolbarMemberStore(super.preferences);
  @override
  LocalProfile get profile => const LocalProfile(
    id: 'member',
    name: '测试用户',
    sources: ['hongguo'],
    salt: '00000000000000000000000000000000',
    pinHash: '0000000000000000000000000000000000000000000000000000000000000000',
  );
}

void main() {
  final snapshotRoot = Platform.environment['DOWNLOAD_TOOLBAR_SNAPSHOTS'];
  setUpAll(() async {
    if (snapshotRoot == null) return;
    final file = File(r'C:\Windows\Fonts\msyh.ttc');
    if (await file.exists()) {
      final loader = FontLoader('ToolbarSnapshot')
        ..addFont(Future.value(ByteData.sublistView(await file.readAsBytes())));
      await loader.load();
    }
    final icons = FontLoader('MaterialIcons')
      ..addFont(rootBundle.load('fonts/MaterialIcons-Regular.otf'));
    await icons.load();
  });

  Future<LocalStore> store({bool member = false}) async {
    SharedPreferences.setMockInitialValues({});
    final prefs = await SharedPreferences.getInstance();
    final value = member ? ToolbarMemberStore(prefs) : LocalStore(prefs);
    addTearDown(value.dispose);
    return value;
  }

  Future<void> show(
    WidgetTester tester,
    ToolbarRepository repository,
    LocalStore local, {
    bool embedded = false,
    double scale = 1,
    GlobalKey? boundary,
  }) async {
    final screen = DownloadsScreen(
      repository: repository,
      store: local,
      embedded: embedded,
    );
    final home = embedded ? Scaffold(body: screen) : screen;
    await tester.pumpWidget(
      MaterialApp(
        theme: ThemeData(
          useMaterial3: true,
          colorScheme: AppTheme.light.colorScheme,
          fontFamily: snapshotRoot == null ? null : 'ToolbarSnapshot',
        ),
        builder: (context, child) => MediaQuery(
          data: MediaQuery.of(
            context,
          ).copyWith(textScaler: TextScaler.linear(scale)),
          child: child!,
        ),
        home: boundary == null
            ? home
            : RepaintBoundary(key: boundary, child: home),
      ),
    );
    await tester.pumpAndSettle();
  }

  for (final layout in [
    (1280.0, 720.0, false, 1.0),
    (360.0, 800.0, false, 1.0),
    (320.0, 844.0, false, 2.0),
    (1280.0, 720.0, true, 1.0),
    (360.0, 800.0, true, 1.0),
    (320.0, 844.0, true, 2.0),
  ]) {
    testWidgets('eight direct actions keep order and fit layout $layout', (
      tester,
    ) async {
      tester.view.physicalSize = Size(layout.$1, layout.$2);
      tester.view.devicePixelRatio = 1;
      addTearDown(tester.view.resetPhysicalSize);
      addTearDown(tester.view.resetDevicePixelRatio);
      final boundary = GlobalKey();
      await show(
        tester,
        ToolbarRepository(),
        await store(),
        embedded: layout.$3,
        scale: layout.$4,
        boundary: boundary,
      );
      expect(
        find.byKey(const ValueKey('download-queue-actions')),
        findsNothing,
      );
      final searchTop = tester.getRect(find.byType(TextField).first).top;
      Rect? previous;
      for (final label in toolbarLabels) {
        final button = find.byTooltip(label);
        expect(button, findsOneWidget);
        final rect = tester.getRect(button);
        expect(rect.left, greaterThanOrEqualTo(0));
        expect(rect.right, lessThanOrEqualTo(layout.$1));
        expect(rect.bottom, lessThanOrEqualTo(searchTop));
        if (previous != null) {
          expect(rect.top, greaterThanOrEqualTo(previous.top));
          if ((rect.top - previous.top).abs() < 1) {
            expect(rect.left, greaterThanOrEqualTo(previous.right));
          }
        }
        previous = rect;
      }
      expect(tester.takeException(), isNull);
      if (snapshotRoot != null && !layout.$3 && layout.$4 == 1) {
        await tester.runAsync(() async {
          final render =
              boundary.currentContext!.findRenderObject()!
                  as RenderRepaintBoundary;
          final image = await render.toImage();
          final bytes = await image.toByteData(format: ui.ImageByteFormat.png);
          final output = File(
            '$snapshotRoot/toolbar-${layout.$1 > 640 ? 'wide' : 'narrow'}.png',
          );
          await output.parent.create(recursive: true);
          await output.writeAsBytes(bytes!.buffer.asUint8List());
          image.dispose();
        });
      }
      await tester.pumpWidget(const SizedBox.shrink());
    });
  }

  testWidgets(
    'settings pause resume archive and refresh need no overflow menu',
    (tester) async {
      tester.view.physicalSize = const Size(1280, 800);
      tester.view.devicePixelRatio = 1;
      addTearDown(tester.view.reset);
      final repo = ToolbarRepository();
      await show(tester, repo, await store());
      await tester.tap(find.byTooltip('排队与下载限速'));
      await tester.pumpAndSettle();
      expect(find.byType(ResourceSettingsScreen), findsOneWidget);
      await tester.pageBack();
      await tester.pumpAndSettle();
      await tester.tap(find.byTooltip('全部暂停'));
      await tester.pumpAndSettle();
      expect(repo.commands, ['pause:task-0']);
      await tester.tap(find.byTooltip('全部继续'));
      await tester.pumpAndSettle();
      expect(repo.commands, containsAll(['resume:task-0', 'resume:task-1']));
      await tester.tap(find.byTooltip('清理已完成任务，保留视频'));
      await tester.pumpAndSettle();
      expect(repo.commands, contains('archive:task-2'));
      expect(
        repo.commands.any((command) => command.startsWith('remove:')),
        isFalse,
      );
      final before = repo.reads;
      await tester.tap(find.byTooltip('刷新记录'));
      await tester.pumpAndSettle();
      expect(repo.reads, greaterThan(before));
      await tester.pumpWidget(const SizedBox.shrink());
    },
  );

  testWidgets('busy queue disables all five direct controls', (tester) async {
    tester.view.physicalSize = const Size(1280, 800);
    tester.view.devicePixelRatio = 1;
    addTearDown(tester.view.reset);
    final repo = ToolbarRepository()..pending = Completer<void>();
    await show(tester, repo, await store());
    await tester.tap(find.byTooltip('全部暂停'));
    await tester.pump();
    for (final suffix in [
      'settings',
      'pause',
      'resume',
      'archive',
      'refresh',
    ]) {
      expect(
        tester
            .widget<IconButton>(find.byKey(ValueKey('download-queue-$suffix')))
            .onPressed,
        isNull,
      );
    }
    repo.pending!.complete();
    await tester.pumpAndSettle();
    await tester.pumpWidget(const SizedBox.shrink());
  });

  testWidgets(
    'member has queue controls without admin settings and lock removes actions',
    (tester) async {
      tester.view.physicalSize = const Size(1280, 800);
      tester.view.devicePixelRatio = 1;
      addTearDown(tester.view.reset);
      final local = await store(member: true);
      await show(tester, ToolbarRepository(), local);
      expect(find.byTooltip('排队与下载限速'), findsNothing);
      expect(find.byTooltip('全部暂停'), findsOneWidget);
      local.lock();
      await tester.pumpAndSettle();
      expect(find.byTooltip('全部暂停'), findsNothing);
      await tester.pumpWidget(const SizedBox.shrink());
    },
  );
}
