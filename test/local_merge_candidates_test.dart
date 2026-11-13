import 'dart:convert';
import 'dart:io';

import 'package:duanju_app/local_merge_candidates.dart';
import 'package:duanju_app/media_library.dart';
import 'package:duanju_app/merge_queue_screen.dart';
import 'package:duanju_app/models.dart';
import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:path/path.dart' as path;

import 'local_media_completion_test.dart';
import 'local_media_folders_test.dart';

Future<LocalMergeSnapshot> snapshot(FolderFixture fixture) =>
    readLocalMergeSnapshot(fixture.library, fixture.repository, {'hongguo'});

Future<void> removeFixtureFolder(FolderFixture fixture, String relative) async {
  final full = path.join(fixture.directory.path, relative);
  if (!path.isWithin(fixture.directory.path, full)) {
    throw StateError('Fixture scope violated');
  }
  await Directory(full).delete(recursive: true);
}

Future<void> openMergePicker(
  WidgetTester tester,
  FolderFixture fixture, {
  String? initialDramaId,
}) async {
  await tester.pumpWidget(
    MaterialApp(
      home: MergeQueueScreen(
        library: fixture.library,
        repository: fixture.repository,
        store: fixture.store,
        addOnOpen: true,
        initialDramaId: initialDramaId,
      ),
    ),
  );
  await waitForMediaText(tester, '选择要合并的剧');
}

Future<void> refreshPicker(WidgetTester tester) async {
  await tester.tap(find.byKey(const ValueKey('merge-picker-refresh')));
  for (var i = 0; i < 200; i++) {
    await tester.runAsync(
      () => Future<void>.delayed(const Duration(milliseconds: 10)),
    );
    await tester.pump();
    final button = find.byKey(const ValueKey('merge-picker-refresh'));
    if (tester.widget<IconButton>(button).onPressed != null) return;
  }
  throw StateError('Merge picker refresh did not complete');
}

class RawMergeRepository extends FolderRepository {
  RawMergeRepository(super.directory);
  final plans = <int, PlaybackPlan>{};
  int localCalls = 0;

  @override
  Future<PlaybackPlan?> localPlayback(Drama drama, Episode episode) async {
    localCalls++;
    return plans[episode.number];
  }
}

DownloadJob rawJob(int number, {bool published = false}) => DownloadJob(
  id: 'raw$number',
  drama: const Drama(id: 'hongguo:raw', source: 'hongguo', title: '本地原分集'),
  episode: Episode({'id': '$number', 'currentEpisode': number}, number),
  state: 'completed',
  created: number,
  bytes: 4,
  published: published,
);

void main() {
  testWidgets(
    'deleted directory never survives through old completed records',
    (tester) async {
      final fixture = (await tester.runAsync(() => completionFixture()))!;
      addTearDown(() => fixture.close(tester));
      final before = await tester.runAsync(fixture.index.readAsString);
      expect(
        (await tester.runAsync(() => snapshot(fixture)))!.candidates,
        hasLength(2),
      );
      await tester.runAsync(() => removeFixtureFolder(fixture, fixture.second));
      final value = (await tester.runAsync(() => snapshot(fixture)))!;
      expect(value.candidates.map((row) => row.id), ['hongguo:a']);
      expect(value.candidates.single.jobs, hasLength(2));
      expect(value.missing, 1);
      expect(await tester.runAsync(fixture.index.readAsString), before);
    },
  );

  testWidgets(
    'changed export bytes are not counted as matching local episodes',
    (tester) async {
      final fixture = (await tester.runAsync(() => completionFixture()))!;
      addTearDown(() => fixture.close(tester));
      await tester.runAsync(
        () => File(
          path.join(fixture.directory.path, fixture.first, 'S01E002.mp4'),
        ).writeAsString('changed-size'),
      );
      final value = (await tester.runAsync(() => snapshot(fixture)))!;
      final row = value.candidates.firstWhere((row) => row.id == 'hongguo:a');
      expect(row.jobs.map((job) => job.episode.number), [1]);
      expect(row.available, isFalse);
      expect(value.missing, 1);
    },
  );

  testWidgets('missing middle episode disables merge rather than renumbering', (
    tester,
  ) async {
    final fixture = (await tester.runAsync(() => completionFixture()))!;
    addTearDown(() => fixture.close(tester));
    await tester.runAsync(() async {
      final first = fixture.repository.jobs.firstWhere(
        (job) => job.drama.id == 'hongguo:a',
      );
      final job = DownloadJob(
        id: 'job-a3',
        drama: first.drama,
        episode: Episode({'id': 'a3', 'currentEpisode': 3}, 3),
        state: 'completed',
        created: 3,
        bytes: 4,
        published: true,
      );
      fixture.repository.jobs.add(job);
      final relative = path.join(fixture.first, 'S01E003.mp4');
      await File(
        path.join(fixture.directory.path, relative),
      ).writeAsString('keep');
      final data = jsonDecode(await fixture.index.readAsString()) as Map;
      (data['items'] as List).add(
        LocalMediaItem(
          id: 'export-${job.id}',
          drama: job.drama,
          file: relative,
          kind: 'export',
          episodes: [3],
          duration: 1,
          bytes: 4,
          created: DateTime(2026),
          jobId: job.id,
          sourceVersion: mediaSourceVersion(job),
        ).toJson(),
      );
      await fixture.index.writeAsString(jsonEncode(data));
      await File(
        path.join(fixture.directory.path, fixture.first, 'S01E002.mp4'),
      ).delete();
    });
    final value = (await tester.runAsync(() => snapshot(fixture)))!;
    final row = value.candidates.firstWhere((row) => row.id == 'hongguo:a');
    expect(row.jobs.map((job) => job.episode.number), [1, 3]);
    expect(row.error, contains('缺少第 2'));
    expect(row.available, isFalse);
  });

  testWidgets('valid unpublished local originals remain mergeable offline', (
    tester,
  ) async {
    final fixture = (await tester.runAsync(() => completionFixture()))!;
    addTearDown(() => fixture.close(tester));
    final repository = RawMergeRepository(fixture.directory);
    await tester.runAsync(() async {
      for (var i = 1; i <= 2; i++) {
        final file = File(path.join(fixture.directory.path, 'raw-$i.mp4'));
        await file.writeAsString('keep');
        repository.jobs.add(rawJob(i));
        repository.plans[i] = PlaybackPlan(url: file.path, local: true);
      }
    });
    final value = (await tester.runAsync(
      () => readLocalMergeSnapshot(fixture.library, repository, {'hongguo'}),
    ))!;
    expect(value.candidates.single.available, isTrue);
    expect(value.candidates.single.jobs, hasLength(2));
    expect(repository.localCalls, 2);
  });

  testWidgets(
    'published missing exports do not fall back and resurrect raw data',
    (tester) async {
      final fixture = (await tester.runAsync(() => completionFixture()))!;
      addTearDown(() => fixture.close(tester));
      final repository = RawMergeRepository(fixture.directory)
        ..jobs.add(rawJob(1, published: true));
      final value = (await tester.runAsync(
        () => readLocalMergeSnapshot(fixture.library, repository, {'hongguo'}),
      ))!;
      expect(value.candidates, isEmpty);
      expect(value.missing, 1);
      expect(repository.localCalls, 0);
    },
  );

  testWidgets('remote playback URLs never qualify as available local media', (
    tester,
  ) async {
    final fixture = (await tester.runAsync(() => completionFixture()))!;
    addTearDown(() => fixture.close(tester));
    final repository = RawMergeRepository(fixture.directory)
      ..jobs.add(rawJob(1))
      ..plans[1] = PlaybackPlan(
        url: 'https://example.invalid/media.mp4',
        local: false,
      );
    final value = (await tester.runAsync(
      () => readLocalMergeSnapshot(fixture.library, repository, {'hongguo'}),
    ))!;
    expect(value.candidates, isEmpty);
    expect(value.missing, 1);
  });

  testWidgets(
    'source permissions keep inaccessible records out of candidates',
    (tester) async {
      final fixture = (await tester.runAsync(() => completionFixture()))!;
      addTearDown(() => fixture.close(tester));
      final value = (await tester.runAsync(
        () => readLocalMergeSnapshot(fixture.library, fixture.repository, {}),
      ))!;
      expect(value.candidates, isEmpty);
      expect(value.missing, 0);
    },
  );

  testWidgets(
    'refresh removes deleted selection and preserves existing records',
    (tester) async {
      final fixture = (await tester.runAsync(
        () => completionFixture(merged: true),
      ))!;
      addTearDown(() => fixture.close(tester));
      final before = await tester.runAsync(fixture.index.readAsString);
      final queue = File(path.join(fixture.directory.path, 'merge-queue.json'));
      final queueBefore = await tester.runAsync(queue.readAsString);
      await openMergePicker(tester, fixture, initialDramaId: 'hongguo:a');
      expect(
        tester
            .widget<CheckboxListTile>(
              find.byKey(const ValueKey('merge-candidate:hongguo:a')),
            )
            .value,
        isTrue,
      );
      await tester.runAsync(() => removeFixtureFolder(fixture, fixture.first));
      await refreshPicker(tester);
      expect(
        find.byKey(const ValueKey('merge-candidate:hongguo:a')),
        findsNothing,
      );
      expect(find.text('加入 0 部'), findsOneWidget);
      expect(
        tester
            .widget<FilledButton>(
              find.byKey(const ValueKey('merge-picker-accept')),
            )
            .onPressed,
        isNull,
      );
      expect(await tester.runAsync(fixture.index.readAsString), before);
      expect(await tester.runAsync(queue.readAsString), queueBefore);
      expect(tester.takeException(), isNull);
    },
  );

  testWidgets('every dialog opening rereads actual directories', (
    tester,
  ) async {
    final fixture = (await tester.runAsync(() => completionFixture()))!;
    addTearDown(() => fixture.close(tester));
    await openMergePicker(tester, fixture);
    expect(
      find.byKey(const ValueKey('merge-candidate:hongguo:b')),
      findsOneWidget,
    );
    await tester.tap(find.text('取消'));
    await tester.pumpAndSettle();
    expect(find.byKey(const ValueKey('merge-queue-refresh')), findsOneWidget);
    await tester.runAsync(() => removeFixtureFolder(fixture, fixture.second));
    await tester.tap(find.byTooltip('添加多部剧'));
    await waitForMediaText(tester, '选择要合并的剧');
    expect(
      find.byKey(const ValueKey('merge-candidate:hongguo:b')),
      findsNothing,
    );
    expect(
      find.byKey(const ValueKey('merge-candidate:hongguo:a')),
      findsOneWidget,
    );
    expect(tester.takeException(), isNull);
  });

  testWidgets('confirmation rechecks files changed after selecting a drama', (
    tester,
  ) async {
    final fixture = (await tester.runAsync(() => completionFixture()))!;
    addTearDown(() => fixture.close(tester));
    await openMergePicker(tester, fixture, initialDramaId: 'hongguo:a');
    await tester.runAsync(
      () => File(
        path.join(fixture.directory.path, fixture.first, 'S01E002.mp4'),
      ).delete(),
    );
    await tester.tap(find.byKey(const ValueKey('merge-picker-accept')));
    await waitForMediaText(tester, '本地分集已移动、删除或改变，请刷新后重新选择；原有记录和文件保留。');
    expect(fixture.library.merges.items, isEmpty);
    expect(fixture.library.busy, isFalse);
    expect(tester.takeException(), isNull);
  });

  testWidgets(
    'refresh failure blocks old selections until a successful reread',
    (tester) async {
      final fixture = (await tester.runAsync(() => completionFixture()))!;
      addTearDown(() => fixture.close(tester));
      await openMergePicker(tester, fixture, initialDramaId: 'hongguo:a');
      fixture.repository.downloadsError = StateError('isolated-read-failure');
      await refreshPicker(tester);
      expect(find.textContaining('isolated-read-failure'), findsWidgets);
      expect(
        tester
            .widget<FilledButton>(
              find.byKey(const ValueKey('merge-picker-accept')),
            )
            .onPressed,
        isNull,
      );
      fixture.repository.downloadsError = null;
      await refreshPicker(tester);
      expect(
        tester
            .widget<FilledButton>(
              find.byKey(const ValueKey('merge-picker-accept')),
            )
            .onPressed,
        isNotNull,
      );
      expect(tester.takeException(), isNull);
    },
  );

  testWidgets(
    'unchanged available selection enqueues without deleting originals',
    (tester) async {
      final fixture = (await tester.runAsync(() => completionFixture()))!;
      addTearDown(() => fixture.close(tester));
      final before = await tester.runAsync(fixture.index.readAsString);
      await openMergePicker(tester, fixture, initialDramaId: 'hongguo:a');
      await tester.tap(find.byKey(const ValueKey('merge-picker-accept')));
      await waitForMediaText(tester, '已加入 1 部；完成后点击「查看合并成品」，默认保留原分集。');
      expect(fixture.library.merges.items, hasLength(1));
      expect(fixture.library.merges.items.single.cleanup, isFalse);
      expect(fixture.library.busy, isFalse);
      expect(await tester.runAsync(fixture.index.readAsString), before);
      expect(tester.takeException(), isNull);
    },
  );
}
