import 'dart:async';
import 'dart:convert';
import 'dart:io';

import 'package:duanju_app/core_bridge.dart';
import 'package:duanju_app/downloads_screen.dart';
import 'package:duanju_app/local_store.dart';
import 'package:duanju_app/models.dart';
import 'package:duanju_app/player_screen.dart';
import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:integration_test/integration_test.dart';
import 'package:media_kit/media_kit.dart';
import 'package:shared_preferences/shared_preferences.dart';
import 'package:window_manager/window_manager.dart';

class OfflineNetworkBlocker extends HttpOverrides {
  int attempted = 0;
  @override
  HttpClient createHttpClient(SecurityContext? context) {
    attempted++;
    throw StateError('Offline local-media check: HTTP disabled');
  }
}

class OfflineNativeRepository extends AppRepository {
  OfflineNativeRepository(this.native);
  final NativeRepository native;
  int remoteCalls = 0, localCalls = 0;
  final plans = <PlaybackPlan>[];
  Never remote() {
    remoteCalls++;
    throw StateError('Offline check forbids source requests');
  }

  @override
  Future<void> initialize() => native.initialize();
  @override
  Future<List<DownloadJob>> downloads() => native.downloads();
  @override
  Future<void> cancelPlayback() => native.cancelPlayback();
  @override
  Future<void> release(String session) => native.release(session);
  @override
  Future<PlaybackPlan?> localPlayback(Drama drama, Episode episode) async {
    localCalls++;
    final plan = await native.localPlayback(drama, episode);
    if (plan != null) plans.add(plan);
    return plan;
  }

  @override
  Future<CatalogPage> catalog(
    String source, {
    int page = 1,
    String query = '',
    String category = '',
    bool force = false,
  }) async => remote();
  @override
  Future<CatalogPage> cached(String source, {String category = ''}) async =>
      remote();
  @override
  Future<String> cover(Drama drama, {bool force = false}) async => remote();
  @override
  Future<DramaDetail> detail(Drama drama) async => remote();
  @override
  Future<PlaybackPlan> resolve(
    Drama drama,
    Episode episode, {
    int quality = 0,
  }) async => remote();
  @override
  Future<PlaybackPlan> fallback(PlaybackPlan current) async => remote();
}

void main() {
  IntegrationTestWidgetsFlutterBinding.ensureInitialized();
  testWidgets(
    'real downloaded episodes play through DownloadsScreen without network',
    (tester) async {
      const directory = String.fromEnvironment('OFFLINE_CHECK_DIRECTORY');
      expect(Platform.isWindows && directory.isNotEmpty, isTrue);
      final report = File('$directory/result.json');
      final previous = HttpOverrides.current;
      final network = OfflineNetworkBlocker();
      HttpOverrides.global = network;
      SharedPreferences.setMockInitialValues({});
      final store = LocalStore(await SharedPreferences.getInstance());
      await store.disableSourceGate();
      final native = NativeRepository(
        background: true,
        dataDirectory: Directory('$directory/profile'),
      )..access = store;
      final repository = OfflineNativeRepository(native);
      final players = <Player>[];
      Timer? heartbeat;
      var ticks = 0, largestGapMs = 0;
      var last = DateTime.now();
      Future<void> until(bool Function() done, {int seconds = 30}) async {
        final deadline = DateTime.now().add(Duration(seconds: seconds));
        while (!done() && DateTime.now().isBefore(deadline)) {
          await tester.pump();
          await Future<void>.delayed(const Duration(milliseconds: 30));
        }
        expect(done(), isTrue, reason: 'local playback deadline exceeded');
      }

      try {
        await repository.initialize();
        final jobs = await repository.downloads();
        expect(jobs.length, 2);
        expect(jobs.every((job) => job.completed), isTrue);
        MediaKit.ensureInitialized();
        await windowManager.ensureInitialized();
        await windowManager.setTitle('真果赏 · 已下载离线播放检查');
        await tester.pumpWidget(
          MaterialApp(
            home: DownloadsScreen(
              repository: repository,
              store: store,
              playerBuilder: (detail, index, position) => PlayerScreen(
                detail: detail,
                initialIndex: index,
                initialPosition: position,
                localOnly: true,
                repository: repository,
                store: store,
                playerFactory: () {
                  final player = Player(
                    configuration: const PlayerConfiguration(muted: true),
                  );
                  players.add(player);
                  return player;
                },
              ),
            ),
          ),
        );
        await until(
          () => find.text(jobs.first.drama.title).evaluate().isNotEmpty,
        );
        await tester.tap(find.text(jobs.first.drama.title));
        await tester.pump();
        final tile = find.descendant(
          of: find.byKey(ValueKey('download-task-${jobs.first.id}')),
          matching: find.byType(ListTile),
        );
        heartbeat = Timer.periodic(const Duration(milliseconds: 20), (_) {
          final now = DateTime.now();
          final gap = now.difference(last).inMilliseconds;
          if (gap > largestGapMs) largestGapMs = gap;
          last = now;
          ticks++;
        });
        last = DateTime.now();
        await tester.tap(tile);
        await tester.tap(tile);
        await tester.tap(tile);
        await until(
          () =>
              players.isNotEmpty &&
              players.single.state.position.inMilliseconds >= 700 &&
              (players.single.state.width ?? 0) > 0,
        );
        expect(players.length, 1);
        final first = {
          'positionMs': players.single.state.position.inMilliseconds,
          'durationMs': players.single.state.duration.inMilliseconds,
          'width': players.single.state.width,
          'height': players.single.state.height,
        };
        final firstUri = players.single.state.playlist.medias.single.uri;
        await tester.tap(find.byTooltip('下一集').last);
        await until(
          () =>
              repository.localCalls == 2 &&
              players.single.state.position.inMilliseconds >= 700 &&
              players.single.state.playlist.medias.single.uri != firstUri &&
              (players.single.state.width ?? 0) > 0,
        );
        final second = {
          'positionMs': players.single.state.position.inMilliseconds,
          'durationMs': players.single.state.duration.inMilliseconds,
          'width': players.single.state.width,
          'height': players.single.state.height,
        };
        expect(repository.remoteCalls, 0);
        expect(network.attempted, 0);
        expect(
          repository.plans.every(
            (plan) => plan.local && File(plan.url).existsSync(),
          ),
          isTrue,
        );
        expect(repository.catalogUpdates.coverRevision(jobs.first.drama.id), 0);
        expect(largestGapMs, lessThan(5000));
        await report.writeAsString(
          jsonEncode({
            'ok': true,
            'realExistingMP4': true,
            'syntheticMedia': false,
            'isolatedProfile': true,
            'entry': 'DownloadsScreen completed episode',
            'singleFlightPlayers': players.length,
            'localCalls': repository.localCalls,
            'remoteRepositoryCalls': repository.remoteCalls,
            'dartHttpAttempts': network.attempted,
            'offlineCoverRefreshes': repository.catalogUpdates.coverRevision(
              jobs.first.drama.id,
            ),
            'uiHeartbeatTicks': ticks,
            'maxUiHeartbeatGapMs': largestGapMs,
            'episodes': [first, second],
            'paths': repository.plans.map((p) => p.url).toList(),
            'normalClientNotTouched': true,
          }),
        );
      } catch (error) {
        await report.writeAsString(
          jsonEncode({
            'ok': false,
            'error': error.toString(),
            'remoteRepositoryCalls': repository.remoteCalls,
            'dartHttpAttempts': network.attempted,
            'maxUiHeartbeatGapMs': largestGapMs,
          }),
        );
        rethrow;
      } finally {
        heartbeat?.cancel();
        await tester.pumpWidget(const SizedBox.shrink());
        for (var i = 0; i < 60; i++) {
          await tester.pump();
          await Future<void>.delayed(const Duration(milliseconds: 30));
        }
        store.dispose();
        HttpOverrides.global = previous;
      }
    },
  );
}
