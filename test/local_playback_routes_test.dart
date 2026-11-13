import 'dart:convert';
import 'package:duanju_app/local_store.dart';
import 'package:duanju_app/models.dart';
import 'package:duanju_app/player_screen.dart';
import 'package:duanju_app/playback_routes_bar.dart';
import 'package:flutter/foundation.dart';
import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:media_kit/media_kit.dart';
import 'package:shared_preferences/shared_preferences.dart';
import 'fixtures.dart';
import 'player_fixtures.dart';

class LocalRouteRepository extends FixtureRepository {
  int localReads = 0, onlineReads = 0, downloadWrites = 0;
  final checked = <int>[];
  final selected = <int>[];
  @override
  Future<PlaybackPlan?> localPlayback(Drama drama, Episode episode) async {
    localReads++;
    return const PlaybackPlan(url: '/local/fixture.mp4', local: true);
  }

  PlaybackPlan online(int index) => PlaybackPlan(
    url: 'https://media.test/route-$index.mp4',
    session: 'local-route-fixture',
    routeIndex: index,
    routeCount: 10,
    routes: List.generate(
      10,
      (i) => PlaybackRouteStatus(index: i, quality: 1080),
    ),
  );
  @override
  Future<PlaybackPlan> resolve(
    Drama drama,
    Episode episode, {
    int quality = 0,
  }) async {
    onlineReads++;
    return online(0);
  }

  @override
  Future<PlaybackRouteStatus> checkPlaybackRoute(
    PlaybackPlan current,
    int index,
  ) async {
    checked.add(index);
    return PlaybackRouteStatus(
      index: index,
      status: index == 1 ? 'failed' : 'passed',
    );
  }

  @override
  Future<PlaybackPlan> selectPlaybackRoute(
    PlaybackPlan current,
    int index,
  ) async {
    selected.add(index);
    return online(index);
  }

  @override
  Future<void> controlDownloads(String command, {String id = ''}) async {
    downloadWrites++;
  }

  @override
  Future<int> enqueueDownloads(
    DramaDetail detail,
    List<Episode> episodes, {
    int quality = 0,
  }) async {
    downloadWrites++;
    return 0;
  }
}

void main() {
  const original = String.fromEnvironment('LOCAL150_EXPECT') == 'original';
  Future<void> settle(WidgetTester tester) async {
    for (var i = 0; i < 15; i++) {
      await tester.pump(const Duration(milliseconds: 10));
    }
  }

  Future<void> mount(
    WidgetTester tester,
    LocalRouteRepository repo,
    ScriptedPlayer player, {
    bool allowOnline = true,
    bool localOnly = false,
    Size size = const Size(1536, 864),
    TargetPlatform platform = TargetPlatform.windows,
  }) async {
    debugDefaultTargetPlatformOverride = platform;
    tester.view.physicalSize = size;
    tester.view.devicePixelRatio = 1;
    addTearDown(() {
      debugDefaultTargetPlatformOverride = null;
      tester.view.resetPhysicalSize();
      tester.view.resetDevicePixelRatio();
    });
    SharedPreferences.setMockInitialValues({});
    final store = LocalStore(await SharedPreferences.getInstance());
    await tester.pumpWidget(
      MaterialApp(
        home: PlayerScreen(
          detail: await repo.detail(FixtureRepository.free),
          initialIndex: 0,
          initialPosition: 7,
          repository: repo,
          store: store,
          localOnly: localOnly,
          allowOnlineFallback: allowOnline,
          playerFactory: () => Player(platformPlayer: player),
          videoBuilder: (controls) => controls,
        ),
      ),
    );
    await settle(tester);
  }

  Future<void> close(WidgetTester tester, ScriptedPlayer player) async {
    await tester.pumpWidget(const SizedBox.shrink());
    await settle(tester);
    expect(player.disposed, isTrue);
    expect(tester.takeException(), isNull);
    debugDefaultTargetPlatformOverride = null;
  }

  testWidgets(
    'local playback exposes explicit online candidates without changing downloads',
    (tester) async {
      final repo = LocalRouteRepository();
      final player = ScriptedPlayer();
      await mount(tester, repo, player);
      expect(repo.localReads, 1);
      expect(repo.onlineReads, 0);
      expect(repo.checked, isEmpty);
      expect(find.byKey(const ValueKey('playback-route-list')), findsNothing);
      final button = find.byKey(const ValueKey('playback-switch-online'));
      final visible = button.evaluate().isNotEmpty;
      expect(visible, !original);
      await player.seek(const Duration(seconds: 28));
      await tester.pump();
      await tester.tap(find.byTooltip('暂停播放'));
      await settle(tester);
      if (!original) {
        expect(find.text('正在播放本地视频'), findsOneWidget);
        await tester.tap(button);
        await settle(tester);
        expect(repo.onlineReads, 1);
        expect(repo.localReads, 1);
        expect(repo.checked.toSet(), List.generate(10, (i) => i).toSet());
        expect(find.text('正在播放本地视频'), findsNothing);
        expect(player.opened.last.start, const Duration(seconds: 28));
        expect(player.played.last, isFalse);
        final bar = tester.widget<PlaybackRoutesBar>(
          find.byType(PlaybackRoutesBar),
        );
        expect(bar.routes[0].status, 'passed');
        expect(bar.routes[1].status, 'failed');
        await tester.tap(find.byKey(const ValueKey('playback-route-2')));
        await settle(tester);
        expect(repo.selected, [2]);
        expect(player.opened.last.start, const Duration(seconds: 28));
        expect(player.played.last, isFalse);
      }
      expect(repo.downloadWrites, 0);
      debugPrint(
        'LOCAL150_FIXTURE=${jsonEncode({'localFirst': true, 'onlineSwitchVisible': visible, 'onlineResolves': repo.onlineReads, 'candidates': original ? 0 : 10, 'selected': repo.selected, 'positionSeconds': player.state.position.inSeconds, 'paused': !player.state.playing, 'downloadWrites': repo.downloadWrites})}',
      );
      await close(tester, player);
    },
  );
  testWidgets(
    'local-only player without online fallback does not offer network switch',
    (tester) async {
      final repo = LocalRouteRepository();
      final player = ScriptedPlayer();
      await mount(tester, repo, player, allowOnline: false, localOnly: true);
      expect(
        find.byKey(const ValueKey('playback-switch-online')),
        findsNothing,
      );
      expect(find.text('正在播放本地视频'), original ? findsNothing : findsOneWidget);
      expect(repo.onlineReads, 0);
      expect(repo.checked, isEmpty);
      await close(tester, player);
    },
  );
  for (final size in [const Size(390, 844), const Size(844, 390)]) {
    testWidgets(
      'mobile local entry fits $size and switches without auto remote requests',
      (tester) async {
        final repo = LocalRouteRepository();
        final player = ScriptedPlayer();
        await mount(
          tester,
          repo,
          player,
          localOnly: true,
          size: size,
          platform: TargetPlatform.android,
        );
        expect(repo.onlineReads, 0);
        expect(tester.takeException(), isNull);
        final button = find.byKey(const ValueKey('playback-switch-online'));
        expect(button, original ? findsNothing : findsOneWidget);
        if (!original) {
          await tester.tap(button);
          await settle(tester);
          expect(repo.onlineReads, 1);
          expect(player.played.last, isTrue);
          expect(
            find.byKey(const ValueKey('playback-route-list')),
            findsOneWidget,
          );
        }
        expect(repo.downloadWrites, 0);
        await close(tester, player);
      },
    );
  }
}
