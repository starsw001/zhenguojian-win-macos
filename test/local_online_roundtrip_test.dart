import 'dart:async';
import 'package:duanju_app/core_bridge.dart';
import 'package:duanju_app/models.dart';
import 'dart:convert';
import 'package:duanju_app/local_store.dart';
import 'package:duanju_app/player_screen.dart';
import 'package:flutter/foundation.dart';
import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:media_kit/media_kit.dart';
import 'package:shared_preferences/shared_preferences.dart';
import 'fixtures.dart';
import 'player_fixtures.dart';
import 'local_playback_routes_test.dart';

Future<void> settleRoundtrip(WidgetTester tester) async {
  for (var i = 0; i < 15; i++) {
    await tester.pump(const Duration(milliseconds: 10));
  }
}

Future<void> mountRoundtrip(
  WidgetTester tester,
  LocalRouteRepository repo,
  ScriptedPlayer player, {
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
        initialPosition: 28,
        repository: repo,
        store: store,
        playerFactory: () => Player(platformPlayer: player),
        videoBuilder: (controls) => controls,
      ),
    ),
  );
  await settleRoundtrip(tester);
}

Future<void> closeRoundtrip(WidgetTester tester, ScriptedPlayer player) async {
  await tester.pumpWidget(const SizedBox.shrink());
  await settleRoundtrip(tester);
  debugDefaultTargetPlatformOverride = null;
  expect(player.disposed, isTrue);
  expect(tester.takeException(), isNull);
}

class RoundtripRepository extends LocalRouteRepository {
  bool missing = false,
      secondMissing = false,
      onlineFails = false,
      noRoutes = false;
  Completer<PlaybackPlan?>? localGate;
  @override
  Future<PlaybackPlan?> localPlayback(Drama drama, Episode episode) async {
    if (missing || (secondMissing && episode.number == 2)) {
      localReads++;
      return null;
    }
    if (localGate != null) {
      localReads++;
      return localGate!.future;
    }
    return super.localPlayback(drama, episode);
  }

  @override
  Future<PlaybackPlan> resolve(
    Drama drama,
    Episode episode, {
    int quality = 0,
  }) async {
    if (onlineFails) {
      onlineReads++;
      throw AppFailure('synthetic online failure');
    }
    return super.resolve(drama, episode, quality: quality);
  }

  @override
  PlaybackPlan online(int index) => noRoutes
      ? const PlaybackPlan(
          url: 'https://media.test/single.mp4',
          session: 'single',
        )
      : super.online(index);
}

void main() {
  const original = String.fromEnvironment('ROUNDTRIP151_EXPECT') == 'original';
  testWidgets(
    'online to local return preserves position and pause without download writes',
    (tester) async {
      final repo = LocalRouteRepository();
      final player = ScriptedPlayer();
      await mountRoundtrip(tester, repo, player);
      final onlineButton = find.byKey(const ValueKey('playback-switch-online'));
      final localButton = find.byKey(const ValueKey('playback-switch-local'));
      expect(localButton, findsNothing);
      await tester.tap(find.byTooltip('暂停播放'));
      await settleRoundtrip(tester);
      await tester.tap(onlineButton);
      await settleRoundtrip(tester);
      expect(repo.onlineReads, 1);
      expect(player.opened.last.start, const Duration(seconds: 28));
      expect(player.played.last, isFalse);
      final returnVisible = localButton.evaluate().isNotEmpty;
      expect(returnVisible, !original);
      await player.seek(const Duration(seconds: 64));
      await tester.pump();
      var returnedLocal = false;
      if (!original) {
        await tester.tap(localButton);
        await settleRoundtrip(tester);
        expect(player.opened.last.uri, contains('/local/fixture.mp4'));
        expect(player.opened.last.start, const Duration(seconds: 64));
        expect(player.played.last, isFalse);
        expect(repo.localReads, 2);
        expect(repo.onlineReads, 1);
        expect(onlineButton, findsOneWidget);
        expect(localButton, findsNothing);
        returnedLocal = true;
        await tester.tap(onlineButton);
        await settleRoundtrip(tester);
        expect(repo.onlineReads, 2);
        expect(player.opened.last.start, const Duration(seconds: 64));
        expect(player.played.last, isFalse);
      }
      expect(repo.downloadWrites, 0);
      debugPrint(
        'ROUNDTRIP151_FIXTURE=${jsonEncode({'localReturnVisible': returnVisible, 'returnedLocal': returnedLocal, 'localReads': repo.localReads, 'onlineReads': repo.onlineReads, 'positionSeconds': player.state.position.inSeconds, 'paused': !player.state.playing, 'downloadWrites': repo.downloadWrites})}',
      );
      await closeRoundtrip(tester, player);
    },
  );

  for (final layout in [
    (TargetPlatform.windows, const Size(1536, 864)),
    (TargetPlatform.android, const Size(390, 844)),
    (TargetPlatform.android, const Size(844, 390)),
  ]) {
    testWidgets('playing roundtrip at ${layout.$1} ${layout.$2}', (
      tester,
    ) async {
      final repo = LocalRouteRepository();
      final player = ScriptedPlayer();
      await mountRoundtrip(
        tester,
        repo,
        player,
        platform: layout.$1,
        size: layout.$2,
      );
      await tester.tap(find.byKey(const ValueKey('playback-switch-online')));
      await settleRoundtrip(tester);
      await player.seek(const Duration(seconds: 49));
      await tester.pump();
      await tester.tap(find.byKey(const ValueKey('playback-switch-local')));
      await settleRoundtrip(tester);
      expect(player.opened.last.uri, contains('/local/fixture.mp4'));
      expect(player.opened.last.start, const Duration(seconds: 49));
      expect(player.played.last, isTrue);
      expect(repo.onlineReads, 1);
      expect(repo.downloadWrites, 0);
      expect(
        find.byKey(const ValueKey('playback-switch-online')),
        findsOneWidget,
      );
      await closeRoundtrip(tester, player);
    });
  }
  testWidgets(
    'failed online resolution retains return path and paused position',
    (tester) async {
      final repo = RoundtripRepository()..onlineFails = true;
      final player = ScriptedPlayer();
      await mountRoundtrip(tester, repo, player);
      await tester.tap(find.byTooltip('暂停播放'));
      await settleRoundtrip(tester);
      await tester.tap(find.byKey(const ValueKey('playback-switch-online')));
      await settleRoundtrip(tester);
      expect(find.text('synthetic online failure'), findsOneWidget);
      await tester.tap(find.byKey(const ValueKey('playback-switch-local')));
      await settleRoundtrip(tester);
      expect(player.opened.last.uri, contains('/local/fixture.mp4'));
      expect(player.opened.last.start, const Duration(seconds: 28));
      expect(player.played.last, isFalse);
      expect(repo.onlineReads, 1);
      expect(repo.downloadWrites, 0);
      await closeRoundtrip(tester, player);
    },
  );
  testWidgets(
    'missing local file stays local-only without network fallback or download',
    (tester) async {
      final repo = RoundtripRepository();
      final player = ScriptedPlayer();
      await mountRoundtrip(tester, repo, player);
      await tester.tap(find.byTooltip('暂停播放'));
      await settleRoundtrip(tester);
      await tester.tap(find.byKey(const ValueKey('playback-switch-online')));
      await settleRoundtrip(tester);
      await player.seek(const Duration(seconds: 64));
      await tester.pump();
      repo.missing = true;
      await tester.tap(find.byKey(const ValueKey('playback-switch-local')));
      await settleRoundtrip(tester);
      expect(find.text('本地视频不可用，请重新下载或选择在线播放。'), findsOneWidget);
      expect(repo.onlineReads, 1);
      expect(repo.downloadWrites, 0);
      expect(player.opened.length, 2);
      repo.missing = false;
      await tester.tap(find.text('改为在线播放'));
      await settleRoundtrip(tester);
      expect(player.opened.last.start, const Duration(seconds: 64));
      expect(player.played.last, isFalse);
      expect(repo.onlineReads, 2);
      expect(repo.downloadWrites, 0);
      repo.missing = true;
      await tester.tap(find.byKey(const ValueKey('playback-switch-local')));
      await settleRoundtrip(tester);
      await tester.tap(find.text('重试本地播放'));
      await settleRoundtrip(tester);
      expect(repo.onlineReads, 2);
      expect(player.opened.length, 3);
      expect(repo.downloadWrites, 0);
      await closeRoundtrip(tester, player);
    },
  );
  testWidgets(
    'new episode does not reuse previous local availability or force-local mode',
    (tester) async {
      final repo = RoundtripRepository()..secondMissing = true;
      final player = ScriptedPlayer();
      await mountRoundtrip(tester, repo, player);
      await tester.tap(find.byKey(const ValueKey('playback-switch-online')));
      await settleRoundtrip(tester);
      await tester.tap(find.byKey(const ValueKey('playback-switch-local')));
      await settleRoundtrip(tester);
      await tester.tap(find.byTooltip('下一集'));
      await settleRoundtrip(tester);
      expect(repo.onlineReads, 2);
      expect(player.opened.last.uri, contains('media.test'));
      expect(find.byKey(const ValueKey('playback-switch-local')), findsNothing);
      expect(
        find.byKey(const ValueKey('playback-switch-online')),
        findsNothing,
      );
      expect(repo.downloadWrites, 0);
      await closeRoundtrip(tester, player);
    },
  );
  testWidgets('single online address still permits returning to local', (
    tester,
  ) async {
    final repo = RoundtripRepository()..noRoutes = true;
    final player = ScriptedPlayer();
    await mountRoundtrip(tester, repo, player);
    await tester.tap(find.byKey(const ValueKey('playback-switch-online')));
    await settleRoundtrip(tester);
    await tester.tap(find.byKey(const ValueKey('playback-switch-local')));
    await settleRoundtrip(tester);
    expect(player.opened.last.uri, contains('/local/fixture.mp4'));
    expect(repo.onlineReads, 1);
    expect(repo.downloadWrites, 0);
    await closeRoundtrip(tester, player);
  });
  testWidgets('repeated return clicks only load local once', (tester) async {
    final repo = RoundtripRepository();
    final player = ScriptedPlayer();
    await mountRoundtrip(tester, repo, player);
    await tester.tap(find.byKey(const ValueKey('playback-switch-online')));
    await settleRoundtrip(tester);
    repo.localGate = Completer<PlaybackPlan?>();
    final button = tester.widget<TextButton>(
      find.byKey(const ValueKey('playback-switch-local')),
    );
    button.onPressed!();
    button.onPressed!();
    await settleRoundtrip(tester);
    expect(repo.localReads, 2);
    expect(repo.onlineReads, 1);
    repo.localGate!.complete(
      const PlaybackPlan(url: '/local/fixture.mp4', local: true),
    );
    await settleRoundtrip(tester);
    expect(player.opened.length, 3);
    expect(repo.downloadWrites, 0);
    await closeRoundtrip(tester, player);
  });
}
