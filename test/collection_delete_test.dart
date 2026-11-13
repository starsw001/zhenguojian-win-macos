import 'package:duanju_app/core_bridge.dart';
import 'package:duanju_app/download_collections.dart';
import 'package:duanju_app/downloads_screen.dart';
import 'package:duanju_app/local_store.dart';
import 'package:duanju_app/models.dart';
import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:shared_preferences/shared_preferences.dart';

import 'fixtures.dart';

class CollectionDeleteRepository extends FixtureRepository {
  final calls = <List<String>>[];
  final commands = <String>[];
  bool deletionBlocked = false;
  final jobs = <DownloadJob>[
    for (var n = 1; n <= 3; n++)
      DownloadJob(
        id: '$n',
        drama: FixtureRepository.free,
        episode: Episode({'currentEpisode': n}, n),
        state: n == 3 ? 'paused' : 'completed',
        archived: n == 2,
        bytes: 4,
        progress: 1,
      ),
    DownloadJob(
      id: 'other',
      drama: const Drama(id: 'hongguo:other', source: 'hongguo', title: '其他作品'),
      episode: Episode({}, 1),
      state: 'completed',
      bytes: 4,
      progress: 1,
    ),
  ];
  @override
  Future<List<DownloadJob>> downloads() async => List.of(jobs);
  @override
  Future<DownloadBatchResult> removeDownloadCollection(
    Drama drama,
    List<String> ids,
  ) async {
    calls.add(List.of(ids));
    if (deletionBlocked) throw AppFailure('文件正在使用，任务记录已保留');
    jobs.removeWhere((job) => job.drama.id == drama.id);
    return DownloadBatchResult(completed: ids);
  }

  @override
  Future<void> controlDownloads(String command, {String id = ''}) async {
    commands.add('$command:$id');
    if (command == 'remove') jobs.removeWhere((job) => job.id == id);
  }
}

void main() {
  Future<LocalStore> store() async {
    SharedPreferences.setMockInitialValues({});
    final value = LocalStore(await SharedPreferences.getInstance());
    addTearDown(value.dispose);
    return value;
  }

  Future<void> show(
    WidgetTester tester,
    CollectionDeleteRepository repo,
  ) async {
    tester.view.physicalSize = const Size(1280, 800);
    tester.view.devicePixelRatio = 1;
    addTearDown(tester.view.reset);
    await tester.pumpWidget(
      MaterialApp(
        home: DownloadsScreen(repository: repo, store: await store()),
      ),
    );
    await tester.pumpAndSettle();
  }

  Future<void> removeCollection(WidgetTester tester) async {
    await tester.tap(find.byTooltip('${FixtureRepository.free.title} · 合集操作'));
    await tester.pumpAndSettle();
    await tester.tap(find.text('取消任务并删除视频'));
    await tester.pumpAndSettle();
    expect(find.textContaining('合并成品及导出的特别篇'), findsOneWidget);
    await tester.tap(find.text('确认删除'));
    await tester.pumpAndSettle();
  }

  testWidgets(
    'whole collection removal includes filtered paused and archived episodes',
    (tester) async {
      final repo = CollectionDeleteRepository();
      await show(tester, repo);
      await tester.tap(find.byKey(const ValueKey('download-filters')));
      await tester.pumpAndSettle();
      await tester.tap(find.widgetWithText(ChoiceChip, '已下载'));
      await tester.tap(find.text('应用'));
      await tester.pumpAndSettle();
      await removeCollection(tester);
      expect(repo.calls.single, containsAll(['1', '2', '3']));
      expect(repo.calls.single, hasLength(3));
      expect(repo.jobs.single.id, 'other');
      expect(repo.commands, isEmpty);
      await tester.pumpWidget(const SizedBox.shrink());
    },
  );
  testWidgets(
    'failed whole collection retains task and supports explicit retry',
    (tester) async {
      final repo = CollectionDeleteRepository()..deletionBlocked = true;
      await show(tester, repo);
      await removeCollection(tester);
      expect(repo.jobs, hasLength(4));
      expect(find.textContaining('项失败'), findsWidgets);
      expect(find.text('重试失败项'), findsOneWidget);
      repo.deletionBlocked = false;
      await tester.pump(const Duration(seconds: 5));
      await tester.pumpAndSettle();
      await tester.tap(find.text('重试失败项'));
      await tester.pumpAndSettle();
      await tester.tap(find.text('确认删除'));
      await tester.pumpAndSettle();
      expect(repo.calls, hasLength(2));
      expect(repo.jobs.single.id, 'other');
      await tester.pumpWidget(const SizedBox.shrink());
    },
  );
  testWidgets(
    'partial episode removal does not request whole collection cleanup',
    (tester) async {
      final repo = CollectionDeleteRepository();
      await show(tester, repo);
      await tester.tap(find.text(FixtureRepository.free.title));
      await tester.pumpAndSettle();
      await tester.tap(
        find.descendant(
          of: find.byKey(const ValueKey('download-task-1')),
          matching: find.byType(PopupMenuButton<String>),
        ),
      );
      await tester.pumpAndSettle();
      await tester.tap(find.text('删除视频'));
      await tester.pumpAndSettle();
      await tester.tap(find.text('确认删除'));
      await tester.pumpAndSettle();
      expect(repo.calls, isEmpty);
      expect(repo.commands, ['remove:1']);
      expect(repo.jobs.map((job) => job.id), containsAll(['2', '3', 'other']));
      await tester.pumpWidget(const SizedBox.shrink());
    },
  );
}
