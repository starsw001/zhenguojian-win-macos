import 'dart:io';

import 'package:duanju_app/models.dart';
import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:path/path.dart' as path;

import 'local_media_completion_test.dart' show waitForMediaText;
import 'local_media_folders_test.dart';

DownloadJob missingExportJob() => DownloadJob(
  id: 'removed-video',
  drama: const Drama(id: 'hongguo:old', source: 'hongguo', title: '旧任务'),
  episode: Episode({'id': '1', 'currentEpisode': 1}, 1),
  state: 'completed',
  bytes: 10,
  created: 1,
);

Future<void> openExportPicker(WidgetTester tester) async {
  await tester.tap(find.text('导出已下载分集'));
  await waitForMediaText(tester, '保存哪部剧的独立 MP4？');
}

Future<void> waitForPickerRefresh(WidgetTester tester) async {
  for (var i = 0; i < 200; i++) {
    await tester.runAsync(
      () => Future<void>.delayed(const Duration(milliseconds: 10)),
    );
    await tester.pump();
    final button = find.byKey(const ValueKey('local-media-picker-refresh'));
    if (button.evaluate().isNotEmpty &&
        tester.widget<IconButton>(button).onPressed != null) {
      return;
    }
  }
  throw StateError('Picker refresh did not finish');
}

void main() {
  testWidgets(
    'cleared download records retain every actual directory in picker',
    (tester) async {
      final fixture = (await tester.runAsync(() => FolderFixture.create()))!;
      addTearDown(() => fixture.close(tester));
      final before = await tester.runAsync(fixture.index.readAsString);
      await fixture.open(tester);
      expect(mediaFolders(), findsNWidgets(2));
      await openExportPicker(tester);
      for (final directory in [fixture.first, fixture.second]) {
        expect(
          find.byKey(ValueKey('export-folder:$directory')),
          findsOneWidget,
        );
        expect(find.text(path.basename(directory)), findsNWidgets(2));
      }
      expect(find.textContaining('2 个目录 · 3 个视频'), findsOneWidget);
      await tester.tap(find.byKey(ValueKey('export-folder:${fixture.first}')));
      await waitForMediaText(tester, '第 1 集');
      expect(mediaFiles(), findsNWidgets(2));
      expect(await tester.runAsync(fixture.index.readAsString), before);
      expect(fixture.library.busy, isFalse);
      expect(tester.takeException(), isNull);
    },
  );

  testWidgets('stale tasks are separate from the actual directory list', (
    tester,
  ) async {
    final fixture = (await tester.runAsync(() => FolderFixture.create()))!;
    addTearDown(() => fixture.close(tester));
    fixture.repository.jobs.add(missingExportJob());
    await fixture.open(tester);
    await openExportPicker(tester);
    expect(find.text('旧任务'), findsNothing);
    await tester.tap(find.byKey(const ValueKey('local-media-picker-pending')));
    await tester.pump();
    expect(find.text('旧任务'), findsOneWidget);
    expect(find.text('待导出 1 集'), findsOneWidget);
    await tester.tap(find.text('返回下载目录'));
    await tester.pump();
    expect(find.text('旧任务'), findsNothing);
    expect(
      find.byKey(ValueKey('export-folder:${fixture.first}')),
      findsOneWidget,
    );
    expect(tester.takeException(), isNull);
  });

  testWidgets(
    'picker refresh updates both views for manual filesystem changes',
    (tester) async {
      final fixture = (await tester.runAsync(() => FolderFixture.create()))!;
      addTearDown(() => fixture.close(tester));
      final before = await tester.runAsync(fixture.index.readAsString);
      await fixture.open(tester);
      await openExportPicker(tester);
      final added = path.join('exports', '手动添加目录');
      await tester.runAsync(() async {
        final removed = path.join(fixture.directory.path, fixture.second);
        expect(path.isWithin(fixture.directory.path, removed), isTrue);
        await Directory(removed).delete(recursive: true);
        final file = File(path.join(fixture.directory.path, added, '02.mp4'));
        await file.parent.create(recursive: true);
        await file.writeAsString('isolated manual file');
      });
      await tester.tap(
        find.byKey(const ValueKey('local-media-picker-refresh')),
      );
      await waitForPickerRefresh(tester);
      expect(find.byKey(ValueKey('export-folder:$added')), findsOneWidget);
      expect(
        find.byKey(ValueKey('export-folder:${fixture.second}')),
        findsNothing,
      );
      await tester.tap(find.text('取消'));
      await tester.pumpAndSettle();
      expect(find.byKey(ValueKey('local-media-folder:$added')), findsOneWidget);
      expect(
        find.byKey(ValueKey('local-media-folder:${fixture.second}')),
        findsNothing,
      );
      expect(await tester.runAsync(fixture.index.readAsString), before);
      expect(tester.takeException(), isNull);
    },
  );

  testWidgets('download task failure never hides existing directory videos', (
    tester,
  ) async {
    final fixture = (await tester.runAsync(() => FolderFixture.create()))!;
    addTearDown(() => fixture.close(tester));
    fixture.repository.downloadsError = StateError(
      'isolated task read failure',
    );
    await fixture.open(tester);
    expect(mediaFolders(), findsNWidgets(2));
    await openExportPicker(tester);
    expect(
      find.byKey(ValueKey('export-folder:${fixture.first}')),
      findsOneWidget,
    );
    expect(find.textContaining('目录视频仍可查看'), findsOneWidget);
    await tester.tap(find.byKey(ValueKey('export-folder:${fixture.first}')));
    await waitForMediaText(tester, '第 1 集');
    expect(mediaFiles(), findsNWidgets(2));
    expect(tester.takeException(), isNull);
  });
}
