import 'dart:async';

import 'package:duanju_app/core_bridge.dart';
import 'package:duanju_app/local_store.dart';
import 'package:duanju_app/models.dart';
import 'package:duanju_app/playback_launch_screen.dart';
import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:shared_preferences/shared_preferences.dart';

import 'fixtures.dart';

class DelayedLaunchRepository extends FixtureRepository {
  final pending = <Completer<DramaDetail>>[];
  @override
  Future<DramaDetail> detail(Drama drama) {
    detailCalls++;
    final result = Completer<DramaDetail>();
    pending.add(result);
    return result.future;
  }
}

class PlaybackRouteObserver extends NavigatorObserver {
  int launches = 0;
  @override
  void didPush(Route<dynamic> route, Route<dynamic>? previousRoute) {
    if (previousRoute != null) launches++;
  }
}

void main() {
  late LocalStore store;
  late DelayedLaunchRepository repository;
  late PlaybackRouteObserver observer;
  late GlobalKey<NavigatorState> navigator;
  late BuildContext origin;

  Future<void> mount(WidgetTester tester) async {
    SharedPreferences.setMockInitialValues({});
    store = LocalStore(await SharedPreferences.getInstance());
    repository = DelayedLaunchRepository();
    observer = PlaybackRouteObserver();
    navigator = GlobalKey<NavigatorState>();
    await tester.pumpWidget(
      MaterialApp(
        navigatorKey: navigator,
        navigatorObservers: [observer],
        home: Builder(
          builder: (context) {
            origin = context;
            return const Scaffold(body: Text('catalog'));
          },
        ),
      ),
    );
  }

  void launch([Drama drama = FixtureRepository.free]) {
    unawaited(
      openPlaybackDirectly(
        origin,
        drama: drama,
        repository: repository,
        store: store,
      ),
    );
  }

  Future<void> settle(WidgetTester tester) async {
    await tester.pump();
    await tester.pump(const Duration(milliseconds: 350));
  }

  Future<void> close(WidgetTester tester) async {
    for (final pending in repository.pending) {
      if (!pending.isCompleted) pending.completeError(AppFailure('已取消'));
    }
    await settle(tester);
    navigator.currentState!.popUntil((route) => route.isFirst);
    await tester.pumpAndSettle();
    await tester.pumpWidget(const SizedBox.shrink());
    store.dispose();
    expect(tester.takeException(), isNull);
  }

  testWidgets('rapid same and different cards create one immediate launch', (
    tester,
  ) async {
    await mount(tester);
    launch();
    launch();
    launch(FixtureRepository.vip);
    await settle(tester);
    expect(find.text('正在进入播放'), findsOneWidget);
    expect(repository.detailCalls, 1);
    expect(observer.launches, 1);
    await close(tester);
  });

  testWidgets(
    'back while waiting ignores late details and permits a new launch',
    (tester) async {
      await mount(tester);
      launch();
      await settle(tester);
      navigator.currentState!.pop();
      await tester.pumpAndSettle();
      repository.pending.first.complete(
        DramaDetail(FixtureRepository.free, []),
      );
      await settle(tester);
      expect(find.text('catalog'), findsOneWidget);
      expect(find.text('暂时没有可播放的集数'), findsNothing);
      launch();
      await settle(tester);
      expect(repository.detailCalls, 2);
      expect(observer.launches, 2);
      expect(find.text('正在进入播放'), findsOneWidget);
      await close(tester);
    },
  );

  testWidgets(
    'detail errors can retry without adding another route or request burst',
    (tester) async {
      await mount(tester);
      launch();
      await settle(tester);
      repository.pending.first.completeError(AppFailure('测试详情失败'));
      await settle(tester);
      expect(find.text('测试详情失败'), findsOneWidget);
      await tester.tap(find.text('重试'));
      await tester.tap(find.text('重试'));
      await settle(tester);
      expect(repository.detailCalls, 2);
      expect(observer.launches, 1);
      expect(find.text('正在进入播放'), findsOneWidget);
      await close(tester);
    },
  );

  testWidgets('empty episodes display an error in the same route', (
    tester,
  ) async {
    await mount(tester);
    launch();
    await settle(tester);
    repository.pending.first.complete(DramaDetail(FixtureRepository.free, []));
    await settle(tester);
    expect(find.text('暂时没有可播放的集数'), findsOneWidget);
    expect(observer.launches, 1);
    await close(tester);
  });
}
