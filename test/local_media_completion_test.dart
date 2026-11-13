import 'dart:convert';
import 'dart:io';

import 'package:duanju_app/media_library.dart';
import 'package:duanju_app/models.dart';
import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:path/path.dart' as path;

import 'local_media_folders_test.dart';

Future<FolderFixture> completionFixture({bool merged = false}) async {
  final fixture = await FolderFixture.create();
  final index =
      jsonDecode(await fixture.index.readAsString()) as Map<String, dynamic>;
  final entries = (index['items'] as List).cast<Map>();
  for (final entry in entries) {
    final number = (entry['episodes'] as List).first as int;
    final job = DownloadJob(
      id: 'job$number',
      drama: Drama.fromJson(Map<String, dynamic>.from(entry['drama'] as Map)),
      episode: Episode({'id': '$number', 'currentEpisode': number}, number),
      state: 'completed',
      created: number,
      bytes: 17,
      actualQuality: 720,
      revision: 1,
      published: true,
    );
    fixture.repository.jobs.add(job);
    entry['jobId'] = job.id;
    entry['sourceVersion'] = mediaSourceVersion(job);
  }
  if (merged) {
    final jobs =
        fixture.repository.jobs
            .where((job) => job.drama.id == 'hongguo:a')
            .toList()
          ..sort((a, b) => a.episode.number.compareTo(b.episode.number));
    final task = MergeQueueJob(
      id: 'finished',
      owner: 'default',
      drama: jobs.first.drama,
      jobIds: jobs.map((job) => job.id).toList(),
      episodes: [1, 2],
      versions: {for (final job in jobs) job.id: mediaSourceVersion(job)},
      state: 'completed',
      created: DateTime(2026, 10, 26),
    );
    final item = LocalMediaItem(
      id: task.outputId,
      drama: task.drama,
      file: path.join('library', task.outputId, 'full.mp4'),
      kind: 'merged',
      episodes: [1, 2],
      duration: 120,
      bytes: 4,
      created: task.created,
      decodeVerified: true,
    );
    final file = File(path.join(fixture.directory.path, item.file));
    await file.parent.create(recursive: true);
    await file.writeAsString('keep');
    (index['items'] as List).add(item.toJson());
    await File(
      path.join(fixture.directory.path, 'merge-queue.json'),
    ).writeAsString(
      jsonEncode({
        'schema': 1,
        'jobs': [task.toJson()],
      }),
    );
  }
  await fixture.index.writeAsString(jsonEncode(index));
  fixture.library.suspended = true;
  fixture.library.merges;
  return fixture;
}

Future<void> waitForMediaText(WidgetTester tester, String text) async {
  for (var i = 0; i < 200; i++) {
    await tester.runAsync(
      () => Future<void>.delayed(const Duration(milliseconds: 10)),
    );
    await tester.pump();
    if (find.text(text).evaluate().isNotEmpty) return;
  }
  throw StateError('Did not reach media state: $text');
}

void main() {
  testWidgets(
    'export picker marks saved episodes and selecting them opens the directory without rewriting index',
    (tester) async {
      final fixture = (await tester.runAsync(() => completionFixture()))!;
      addTearDown(() => fixture.close(tester));
      final before = await tester.runAsync(fixture.index.readAsString);
      await fixture.open(tester);
      await tester.tap(find.text('导出已下载分集'));
      await waitForMediaText(tester, '保存哪部剧的独立 MP4？');
      expect(find.text('2 个视频 · 0.0 KB'), findsWidgets);
      await tester.tap(find.byKey(ValueKey('export-folder:${fixture.first}')));
      await waitForMediaText(tester, '合并这部剧');
      expect(
        find.byKey(const ValueKey('local-media-export-result')),
        findsOneWidget,
      );
      expect(find.text('合并这部剧'), findsOneWidget);
      expect(find.text('第 1 集'), findsOneWidget);
      expect(find.text('第 3 集'), findsNothing);
      expect(await tester.runAsync(fixture.index.readAsString), before);
      expect(fixture.library.busy, isFalse);
      expect(tester.takeException(), isNull);
    },
  );

  testWidgets(
    'folder merge preselects only that drama and preserves originals by default',
    (tester) async {
      final fixture = (await tester.runAsync(() => completionFixture()))!;
      addTearDown(() => fixture.close(tester));
      await fixture.open(tester);
      await tester.tap(
        find.byKey(ValueKey('local-media-folder:${fixture.first}')),
      );
      await tester.pump();
      await tester.tap(find.byKey(const ValueKey('local-media-merge-folder')));
      await waitForMediaText(tester, '选择要合并的剧');
      final checkboxes = tester
          .widgetList<CheckboxListTile>(find.byType(CheckboxListTile))
          .toList();
      expect(checkboxes.where((tile) => tile.value == true), hasLength(1));
      expect(
        (checkboxes.firstWhere((tile) => tile.value == true).title! as Text)
            .data,
        'A剧',
      );
      expect(
        checkboxes
            .firstWhere((tile) => (tile.title! as Text).data == '完成后删除原分集')
            .value,
        isFalse,
      );
      expect(tester.takeException(), isNull);
    },
  );

  testWidgets('completed queue opens and locates the final merged product', (
    tester,
  ) async {
    final fixture = (await tester.runAsync(
      () => completionFixture(merged: true),
    ))!;
    addTearDown(() => fixture.close(tester));
    await fixture.open(tester);
    await tester.tap(find.text('合并成品'));
    await tester.pump();
    await tester.tap(find.text('合并已下载分集'));
    await waitForMediaText(tester, '选择要合并的剧');
    await tester.tap(find.text('取消'));
    await tester.pump(const Duration(milliseconds: 300));
    await waitForMediaText(tester, '查看合并成品');
    await tester.tap(find.text('查看合并成品'));
    await waitForMediaRefresh(tester);
    expect(
      find.byKey(const ValueKey('local-media-merged:merged-finished')),
      findsOneWidget,
    );
    expect(find.byTooltip('打开合并成品文件夹'), findsOneWidget);
    expect(find.textContaining('MP4'), findsWidgets);
    expect(tester.takeException(), isNull);
  });

  testWidgets(
    'empty merged view explains export versus merge and exposes its action',
    (tester) async {
      final fixture = (await tester.runAsync(() => completionFixture()))!;
      addTearDown(() => fixture.close(tester));
      await fixture.open(tester);
      await tester.tap(find.text('合并成品'));
      await tester.pump();
      expect(find.text('尚无合并成品。独立 MP4 导出不会自动合并。'), findsOneWidget);
      expect(
        find.byKey(const ValueKey('local-media-start-merge')),
        findsOneWidget,
      );
      expect(tester.takeException(), isNull);
    },
  );
}
