import 'package:duanju_app/core_bridge.dart';
import 'package:duanju_app/local_store.dart';
import 'package:duanju_app/settings_screen.dart';
import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:shared_preferences/shared_preferences.dart';

import 'fixtures.dart';

class LocationRepository extends FixtureRepository {
  String directory = 'D:/installed-app/download';
  int exactMoves = 0;
  int parentMoves = 0;
  bool failStorage = false;
  bool failMove = false;
  @override
  Future<String> downloadDirectory() async => directory;
  @override
  Future<Map<String, dynamic>> storage() async {
    if (failStorage) throw AppFailure('此位置不可读，请选择其他目录');
    return {'directory': directory, 'bytes': 1024, 'files': 1, 'free': 10000};
  }

  @override
  Future<void> moveDownloads(String directory) async => parentMoves++;
  @override
  Future<void> moveDownloadsExactly(String directory) async {
    exactMoves++;
    if (failMove) throw AppFailure('目标不是空目录，已保留原文件');
    this.directory = directory;
  }
}

void main() {
  late LocalStore store;
  late LocationRepository repository;

  Future<void> mount(
    WidgetTester tester, {
    String? picked = 'D:/my-videos',
  }) async {
    SharedPreferences.setMockInitialValues({});
    store = LocalStore(await SharedPreferences.getInstance());
    repository = LocationRepository();
    await tester.pumpWidget(
      MaterialApp(
        home: StorageScreen(
          repository: repository,
          store: store,
          directoryPicker: () async => picked,
        ),
      ),
    );
    await tester.pumpAndSettle();
  }

  Future<void> choose(WidgetTester tester) async {
    final button = find.widgetWithIcon(
      FilledButton,
      Icons.drive_file_move_outline,
    );
    await tester.ensureVisible(button);
    await tester.tap(button);
    await tester.pumpAndSettle();
  }

  Future<void> close(WidgetTester tester) async {
    await tester.pumpWidget(const SizedBox.shrink());
    store.dispose();
    expect(tester.takeException(), isNull);
  }

  testWidgets(
    'confirmed selection is the exact directory with no added suffix',
    (tester) async {
      await mount(tester);
      await choose(tester);
      expect(repository.exactMoves, 0);
      expect(find.textContaining('D:/my-videos'), findsOneWidget);
      await tester.tap(find.text('迁移'));
      await tester.pumpAndSettle();
      expect(repository.exactMoves, 1);
      expect(repository.parentMoves, 0);
      expect(repository.directory, 'D:/my-videos');
      expect(find.text('D:/my-videos'), findsOneWidget);
      await close(tester);
    },
  );

  testWidgets('cancelling the directory picker does not alter location', (
    tester,
  ) async {
    await mount(tester, picked: null);
    await choose(tester);
    expect(repository.exactMoves + repository.parentMoves, 0);
    expect(repository.directory, 'D:/installed-app/download');
    await close(tester);
  });

  testWidgets('cancelling migration preserves location and content', (
    tester,
  ) async {
    await mount(tester);
    await choose(tester);
    await tester.tap(find.text('取消'));
    await tester.pumpAndSettle();
    expect(repository.exactMoves + repository.parentMoves, 0);
    expect(repository.directory, 'D:/installed-app/download');
    await close(tester);
  });

  testWidgets('nonempty destination error keeps the original location', (
    tester,
  ) async {
    await mount(tester);
    repository.failMove = true;
    await choose(tester);
    await tester.tap(find.text('迁移'));
    await tester.pumpAndSettle();
    expect(find.text('目标不是空目录，已保留原文件'), findsOneWidget);
    expect(repository.directory, 'D:/installed-app/download');
    await close(tester);
  });

  testWidgets('unreadable default still shows its path and allows selection', (
    tester,
  ) async {
    await mount(tester);
    repository.failStorage = true;
    await tester.ensureVisible(find.text('刷新用量'));
    await tester.tap(find.text('刷新用量'));
    await tester.pumpAndSettle();
    expect(find.text('D:/installed-app/download'), findsOneWidget);
    expect(find.text('此位置不可读，请选择其他目录'), findsOneWidget);
    await choose(tester);
    expect(find.text('迁移已下载内容？'), findsOneWidget);
    await tester.tap(find.text('取消'));
    await tester.pumpAndSettle();
    await close(tester);
  });
}
