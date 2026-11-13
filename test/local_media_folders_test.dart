import 'dart:async';
import 'dart:convert';
import 'dart:io';

import 'package:duanju_app/local_media_screen.dart';
import 'package:duanju_app/local_store.dart';
import 'package:duanju_app/media_library.dart';
import 'package:duanju_app/models.dart';
import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:path/path.dart' as path;
import 'package:shared_preferences/shared_preferences.dart';

import 'fixtures.dart';

class FolderRepository extends FixtureRepository {
  FolderRepository(this.directory);
  final Directory directory;
  List<DownloadJob> jobs = [];
  Completer<void>? delay;
  Object? downloadsError;
  @override
  Future<String> downloadDirectory() async => directory.path;
  @override
  Future<List<DownloadJob>> downloads() async {
    final pending = delay;
    delay = null;
    await pending?.future;
    if (downloadsError case final Object error) throw error;
    return jobs;
  }
}

class CountingFolderLibrary extends MediaLibrary {
  CountingFolderLibrary(super.repository, super.store);
  int reads = 0;
  @override
  List<LocalMediaItem> get items {
    reads++;
    return super.items;
  }

  void progressTick() {
    progress += .001;
    notifyListeners();
  }
}

class FolderFixture {
  FolderFixture(
    this.directory,
    this.repository,
    this.store,
    this.library,
    this.index,
  );
  final Directory directory;
  final FolderRepository repository;
  final LocalStore store;
  final CountingFolderLibrary library;
  final File index;
  final first = path.join('exports', 'A剧 [hongguo-111111111111]');
  final second = path.join('exports', 'B剧 [hongguo-222222222222]');

  static Future<FolderFixture> create({int episodes = 3}) async {
    final directory = await Directory.systemTemp.createTemp('media-folder-ui-');
    SharedPreferences.setMockInitialValues({});
    final store = testStore(await SharedPreferences.getInstance());
    final repository = FolderRepository(directory);
    final library = CountingFolderLibrary(repository, store);
    final index = File(path.join(directory.path, 'media-library.json'));
    final fixture = FolderFixture(directory, repository, store, library, index);
    final items = <LocalMediaItem>[];
    for (var i = 1; i <= episodes; i++) {
      final folder = i == episodes ? fixture.second : fixture.first;
      final relative = path.join(
        folder,
        'S01E${i.toString().padLeft(3, '0')}.mp4',
      );
      final file = File(path.join(directory.path, relative));
      await file.parent.create(recursive: true);
      await file.writeAsString('synthetic fixture');
      items.add(
        LocalMediaItem(
          id: 'episode-$i',
          drama: Drama(
            id: i == episodes ? 'hongguo:b' : 'hongguo:a',
            source: 'hongguo',
            title: i == episodes ? 'B剧' : 'A剧',
          ),
          file: relative,
          kind: 'export',
          episodes: [i],
          duration: 1,
          bytes: 17,
          created: DateTime(2026, 10, 26, 0, 0, i),
        ),
      );
    }
    await index.writeAsString(
      jsonEncode({
        'items': items.reversed.map((item) => item.toJson()).toList(),
      }),
    );
    return fixture;
  }

  Future<void> close(WidgetTester tester) async {
    await tester.pumpWidget(const SizedBox.shrink());
    MediaLibrary.current = null;
    library.dispose();
    store.dispose();
    await tester.runAsync(() => directory.delete(recursive: true));
  }

  Future<void> open(WidgetTester tester, {double textScale = 1}) async {
    MediaLibrary.current = library;
    await tester.pumpWidget(
      MaterialApp(
        builder: (_, child) => MediaQuery(
          data: MediaQueryData(
            size: tester.view.physicalSize,
            textScaler: TextScaler.linear(textScale),
          ),
          child: child!,
        ),
        home: LocalMediaScreen(repository: repository, store: store),
      ),
    );
    await waitForMediaRefresh(tester);
    await tester.tap(find.text('独立 MP4'));
    await tester.pump();
  }
}

Future<void> waitForMediaRefresh(WidgetTester tester) async {
  for (var i = 0; i < 200; i++) {
    await tester.runAsync(
      () => Future<void>.delayed(const Duration(milliseconds: 10)),
    );
    await tester.pump();
    final refresh = find.byWidgetPredicate(
      (widget) => widget is IconButton && widget.tooltip == '刷新',
    );
    if (refresh.evaluate().isNotEmpty &&
        tester.widget<IconButton>(refresh).onPressed != null) {
      return;
    }
  }
  throw StateError('Local media refresh did not finish');
}

Finder mediaFiles() => find.byWidgetPredicate(
  (widget) =>
      widget is Card &&
      widget.key is ValueKey<String> &&
      (widget.key! as ValueKey<String>).value.startsWith('local-media-file:'),
);
Finder mediaFolders() => find.byWidgetPredicate(
  (widget) =>
      widget is Card &&
      widget.key is ValueKey<String> &&
      (widget.key! as ValueKey<String>).value.startsWith('local-media-folder:'),
);

void main() {
  testWidgets(
    'exports opens as folders, drills down and returns without mixed episodes',
    (tester) async {
      final fixture = (await tester.runAsync(() => FolderFixture.create()))!;
      addTearDown(() => fixture.close(tester));
      final before = await tester.runAsync(fixture.index.readAsString);
      await fixture.open(tester);
      expect(mediaFolders(), findsNWidgets(2));
      expect(mediaFiles(), findsNothing);
      await tester.tap(
        find.byKey(ValueKey('local-media-folder:${fixture.first}')),
      );
      await tester.pump();
      expect(mediaFiles(), findsNWidgets(2));
      expect(find.text('第 1 集'), findsOneWidget);
      expect(find.text('第 2 集'), findsOneWidget);
      expect(find.text('第 3 集'), findsNothing);
      await tester.tap(find.byKey(const ValueKey('local-media-exports-root')));
      await tester.pump();
      expect(mediaFolders(), findsNWidgets(2));
      await tester.tap(
        find.byKey(ValueKey('local-media-folder:${fixture.second}')),
      );
      await tester.pump();
      expect(find.text('第 3 集'), findsOneWidget);
      expect(find.text('第 1 集'), findsNothing);
      expect(await tester.runAsync(fixture.index.readAsString), before);
      expect(tester.takeException(), isNull);
    },
  );

  testWidgets(
    'refresh updates actual files, retains current folder and exits removed folders',
    (tester) async {
      final fixture = (await tester.runAsync(() => FolderFixture.create()))!;
      addTearDown(() => fixture.close(tester));
      final before = await tester.runAsync(fixture.index.readAsString);
      await fixture.open(tester);
      await tester.tap(
        find.byKey(ValueKey('local-media-folder:${fixture.first}')),
      );
      await tester.pump();
      await tester.runAsync(() async {
        await File(
          path.join(fixture.directory.path, fixture.first, 'S01E001.mp4'),
        ).delete();
        await File(
          path.join(fixture.directory.path, fixture.first, 'S01E010.mp4'),
        ).writeAsString('manual addition');
      });
      await tester.tap(find.byTooltip('刷新'));
      await waitForMediaRefresh(tester);
      expect(find.text('第 1 集'), findsNothing);
      expect(find.text('第 2 集'), findsOneWidget);
      expect(find.text('S01E010.mp4'), findsOneWidget);
      expect(find.textContaining('未登记文件'), findsWidgets);
      await tester.runAsync(
        () => Directory(
          path.join(fixture.directory.path, fixture.first),
        ).rename(path.join(fixture.directory.path, 'exports', '已移动目录')),
      );
      await tester.tap(find.byTooltip('刷新'));
      await waitForMediaRefresh(tester);
      expect(mediaFiles(), findsNothing);
      expect(find.text('已移动目录'), findsOneWidget);
      expect(await tester.runAsync(fixture.index.readAsString), before);
      expect(tester.takeException(), isNull);
    },
  );

  testWidgets(
    'large episode scroll jumps stay lazy and progress does not resort the library',
    (tester) async {
      final fixture = (await tester.runAsync(
        () => FolderFixture.create(episodes: 2001),
      ))!;
      addTearDown(() => fixture.close(tester));
      await fixture.open(tester);
      await tester.tap(
        find.byKey(ValueKey('local-media-folder:${fixture.first}')),
      );
      await tester.pump();
      final list = tester.widget<ListView>(find.byType(ListView));
      expect(list.itemExtent, isNotNull);
      expect(list.childrenDelegate.estimatedChildCount, 2000);
      final reads = fixture.library.reads;
      var peak = mediaFiles().evaluate().length;
      final stopwatch = Stopwatch()..start();
      for (var i = 0; i < 20; i++) {
        list.controller!.jumpTo(
          i.isEven ? list.controller!.position.maxScrollExtent : 0,
        );
        fixture.library.progressTick();
        await tester.pump();
        peak = peak > mediaFiles().evaluate().length
            ? peak
            : mediaFiles().evaluate().length;
      }
      list.controller!.jumpTo(list.controller!.position.maxScrollExtent);
      await tester.pump();
      stopwatch.stop();
      expect(find.text('第 2000 集'), findsOneWidget);
      expect(peak, lessThan(24));
      expect(fixture.library.reads, reads);
      expect(tester.takeException(), isNull);
      debugPrint(
        'LOCAL_MEDIA_SCROLL_METRICS episodes=2000 jumps=21 peakMounted=$peak catalogReads=$reads elapsedMs=${stopwatch.elapsedMilliseconds}',
      );
    },
  );

  testWidgets('many folders are fixed-extent lazy rows too', (tester) async {
    final fixture = (await tester.runAsync(() => FolderFixture.create()))!;
    addTearDown(() => fixture.close(tester));
    await tester.runAsync(() async {
      for (var i = 0; i < 400; i++) {
        await Directory(
          path.join(fixture.directory.path, 'exports', '目录$i'),
        ).create();
      }
    });
    await fixture.open(tester);
    final list = tester.widget<ListView>(find.byType(ListView));
    expect(list.itemExtent, isNotNull);
    expect(list.childrenDelegate.estimatedChildCount, 402);
    expect(mediaFolders().evaluate().length, lessThan(24));
    list.controller!.jumpTo(list.controller!.position.maxScrollExtent);
    await tester.pump();
    expect(find.text('目录399'), findsOneWidget);
    expect(mediaFolders().evaluate().length, lessThan(24));
    expect(tester.takeException(), isNull);
  });

  testWidgets('merged products keep their own list and existing actions', (
    tester,
  ) async {
    final fixture = (await tester.runAsync(() => FolderFixture.create()))!;
    addTearDown(() => fixture.close(tester));
    await tester.runAsync(() async {
      final relative = path.join('library', 'merged-one', 'full.mkv');
      final file = File(path.join(fixture.directory.path, relative));
      await file.parent.create(recursive: true);
      await file.writeAsString('merged fixture');
      final data =
          jsonDecode(await fixture.index.readAsString())
              as Map<String, dynamic>;
      (data['items'] as List).add(
        LocalMediaItem(
          id: 'merged-one',
          drama: const Drama(
            id: 'hongguo:a',
            source: 'hongguo',
            title: 'A剧',
            episodes: 3,
          ),
          file: relative,
          kind: 'merged',
          episodes: [1, 2, 3],
          duration: 60,
          bytes: 14,
          created: DateTime(2026, 10, 26),
        ).toJson(),
      );
      await fixture.index.writeAsString(jsonEncode(data));
    });
    await fixture.open(tester, textScale: 1.6);
    await tester.tap(find.text('合并成品'));
    await tester.pump();
    expect(
      find.byKey(const ValueKey('local-media-merged:merged-one')),
      findsOneWidget,
    );
    expect(find.text('全集播放'), findsOneWidget);
    expect(find.text('另存特别篇视频'), findsOneWidget);
    expect(mediaFolders(), findsNothing);
    expect(mediaFiles(), findsNothing);
    expect(tester.takeException(), isNull);
  });

  testWidgets(
    'narrow layout and enlarged text retain folder navigation and file actions',
    (tester) async {
      tester.view.physicalSize = const Size(360, 740);
      tester.view.devicePixelRatio = 1;
      addTearDown(tester.view.resetPhysicalSize);
      addTearDown(tester.view.resetDevicePixelRatio);
      final fixture = (await tester.runAsync(() => FolderFixture.create()))!;
      addTearDown(() => fixture.close(tester));
      await fixture.open(tester, textScale: 1.6);
      await tester.tap(
        find.byKey(ValueKey('local-media-folder:${fixture.first}')),
      );
      await tester.pump();
      expect(find.byTooltip('本地播放'), findsWidgets);
      expect(find.byTooltip('删除成品'), findsWidgets);
      expect(tester.takeException(), isNull);
    },
  );

  testWidgets(
    'locking permissions clears cached folders and discards delayed refresh',
    (tester) async {
      final fixture = (await tester.runAsync(() => FolderFixture.create()))!;
      addTearDown(() => fixture.close(tester));
      await tester.runAsync(
        () => fixture.store.saveProfile(
          id: 'default',
          name: '管理员',
          sources: ['hongguo'],
          download: true,
          pin: '123456',
        ),
      );
      await fixture.open(tester);
      expect(mediaFolders(), findsWidgets);
      final pending = Completer<void>();
      fixture.repository.delay = pending;
      await tester.tap(find.byTooltip('刷新'));
      await tester.pump();
      fixture.store.lock();
      await tester.pump();
      expect(mediaFolders(), findsNothing);
      pending.complete();
      await tester.runAsync(
        () => Future<void>.delayed(const Duration(milliseconds: 80)),
      );
      await tester.pump();
      expect(mediaFolders(), findsNothing);
      expect(find.text('当前用户没有本地媒体权限'), findsWidgets);
      expect(tester.takeException(), isNull);
    },
  );
}
