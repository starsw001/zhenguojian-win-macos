import 'dart:async';

import 'package:duanju_app/core_bridge.dart';
import 'package:duanju_app/downloads_screen.dart';
import 'package:duanju_app/local_store.dart';
import 'package:duanju_app/models.dart';
import 'package:duanju_app/playback_loader.dart';
import 'package:duanju_app/player_runtime_options.dart';
import 'package:duanju_app/player_screen.dart';
import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:media_kit/media_kit.dart';
import 'package:shared_preferences/shared_preferences.dart';

import 'fixtures.dart';
import 'player_fixtures.dart';

class OfflinePlaybackRepository extends FixtureRepository {
  Completer<void>? cancellation;
  Completer<PlaybackPlan?>? pendingLocal;
  PlaybackPlan? local = const PlaybackPlan(
    url: '/local/offline.mp4',
    local: true,
  );
  int localReads = 0, onlineReads = 0, downloadReads = 0;
  bool cancellationFails = false;
  Completer<void>? releaseGate;
  final released = <String>[];
  Episode episode(int n) => Episode({'id': '$n', 'currentEpisode': n}, n);
  @override
  Future<void> release(String session) async {
    released.add(session);
    await releaseGate?.future;
  }

  @override
  Future<void> cancelPlayback() async {
    if (cancellationFails) {
      throw StateError('previous online cancellation failed');
    }
    await cancellation?.future;
  }

  @override
  Future<PlaybackPlan?> localPlayback(Drama drama, Episode episode) async {
    localReads++;
    return pendingLocal == null ? local : pendingLocal!.future;
  }

  @override
  Future<PlaybackPlan> resolve(
    Drama drama,
    Episode episode, {
    int quality = 0,
  }) async {
    onlineReads++;
    throw StateError('network disabled');
  }

  @override
  Future<List<DownloadJob>> downloads() async {
    downloadReads++;
    return [
      for (var n = 1; n <= 2; n++)
        DownloadJob(
          id: '$n',
          drama: FixtureRepository.free,
          episode: episode(n),
          state: 'completed',
          bytes: 1024,
          total: 1024,
          progress: 1,
        ),
    ];
  }
}

void main() {
  test(
    'default playback selects local without waiting for online cancellation',
    () async {
      final repo = OfflinePlaybackRepository()
        ..cancellation = Completer<void>();
      final loader = PlaybackLoader(repo);
      try {
        final plan = await loader
            .load(FixtureRepository.free, repo.episode(1))
            .timeout(const Duration(seconds: 1));
        expect(plan!.local, isTrue);
        expect(repo.onlineReads, 0);
        expect(repo.localReads, 1);
      } finally {
        repo.cancellation!.complete();
        await loader.close();
      }
    },
  );

  test(
    'newly downloaded episode takes priority over a prepared online plan',
    () async {
      final repo = OfflinePlaybackRepository()..releaseGate = Completer<void>();
      final loader = PlaybackLoader(repo);
      final plan = await loader
          .load(
            FixtureRepository.free,
            repo.episode(1),
            prepared: const PlaybackPlan(
              url: 'https://example.test/stale.mp4',
              session: 'old',
            ),
          )
          .timeout(const Duration(seconds: 1));
      expect(plan!.local, isTrue);
      expect(repo.onlineReads, 0);
      expect(repo.released, contains('old'));
      repo.releaseGate!.complete();
      await loader.close();
    },
  );

  test(
    'missing local registration cannot reuse a stale prepared local plan',
    () async {
      final repo = OfflinePlaybackRepository()..local = null;
      final loader = PlaybackLoader(repo);
      await expectLater(
        loader.load(
          FixtureRepository.free,
          repo.episode(1),
          prepared: const PlaybackPlan(url: '/deleted/old.mp4', local: true),
        ),
        throwsStateError,
      );
      expect(repo.onlineReads, 1);
      await loader.close();
    },
  );

  test('explicit online selection can use a prepared network plan', () async {
    final repo = OfflinePlaybackRepository();
    final loader = PlaybackLoader(repo);
    const online = PlaybackPlan(
      url: 'https://example.test/chosen.mp4',
      session: 'chosen',
    );
    expect(
      (await loader.load(
        FixtureRepository.free,
        repo.episode(1),
        online: true,
        prepared: online,
      ))!.session,
      'chosen',
    );
    expect(repo.localReads, 0);
    await loader.close();
  });

  Future<LocalStore> store() async {
    SharedPreferences.setMockInitialValues({});
    final local = LocalStore(await SharedPreferences.getInstance());
    addTearDown(local.dispose);
    return local;
  }

  Future<void> tick(WidgetTester tester, [int n = 12]) async {
    for (var i = 0; i < n; i++) {
      await tester.pump(const Duration(milliseconds: 20));
    }
  }

  test(
    'offline loading does not wait for a held online cancellation',
    () async {
      final repo = OfflinePlaybackRepository()
        ..cancellation = Completer<void>();
      final loader = PlaybackLoader(repo);
      try {
        final plan = await loader
            .load(FixtureRepository.free, repo.episode(1), localOnly: true)
            .timeout(const Duration(seconds: 1));
        expect(plan!.local, isTrue);
        expect(repo.localReads, 1);
        expect(repo.onlineReads, 0);
      } finally {
        repo.cancellation!.complete();
        await loader.close();
      }
    },
  );
  test(
    'failed cancellation cannot turn local playback into a network dependency',
    () async {
      final repo = OfflinePlaybackRepository()..cancellationFails = true;
      final loader = PlaybackLoader(repo);
      expect(
        (await loader.load(
          FixtureRepository.free,
          repo.episode(1),
          localOnly: true,
        ))!.local,
        isTrue,
      );
      repo.cancellationFails = false;
      await loader.close();
    },
  );
  for (final url in [
    '',
    'https://media.test/video.mp4',
    'http://media.test/video.m3u8',
    'rtsp://media.test/video',
  ]) {
    test('local-only rejects remote or empty media: $url', () async {
      final repo = OfflinePlaybackRepository()
        ..local = PlaybackPlan(url: url, local: true);
      final loader = PlaybackLoader(repo);
      await expectLater(
        loader.load(FixtureRepository.free, repo.episode(1), localOnly: true),
        throwsA(isA<AppFailure>().having((e) => e.code, 'code', 'local_media')),
      );
      expect(repo.onlineReads, 0);
      await loader.close();
    });
  }
  for (final url in [
    r'D:\download\中文 剧集\S01E001.mp4',
    'file:///D:/download/video.mp4',
    '/download/video.m3u8',
  ]) {
    test('local-only accepts local media: $url', () async {
      final repo = OfflinePlaybackRepository()
        ..local = PlaybackPlan(url: url, local: true);
      final loader = PlaybackLoader(repo);
      expect(
        (await loader.load(
          FixtureRepository.free,
          repo.episode(1),
          localOnly: true,
        ))!.url,
        url,
      );
      await loader.close();
    });
  }
  test(
    'player properties use an asynchronous command with literal arguments',
    () async {
      final gate = Completer<void>();
      final sent = <List<String>>[];
      var complete = false;
      final pending = setPlaybackProperty(
        (args) {
          sent.add(args);
          return gate.future;
        },
        'demuxer-lavf-o',
        'protocol_whitelist=[file,crypto,data]',
      ).then((_) => complete = true);
      expect(sent.single, [
        'set',
        'demuxer-lavf-o',
        'protocol_whitelist=[file,crypto,data]',
      ]);
      await Future<void>.delayed(Duration.zero);
      expect(complete, isFalse);
      gate.complete();
      await pending;
      expect(complete, isTrue);
    },
  );
  testWidgets(
    'local player gives loading feedback without covers or remote reads',
    (tester) async {
      final repo = OfflinePlaybackRepository()
        ..pendingLocal = Completer<PlaybackPlan?>();
      final player = ScriptedPlayer();
      await tester.pumpWidget(
        MaterialApp(
          home: PlayerScreen(
            detail: DramaDetail(FixtureRepository.free, [
              repo.episode(1),
              repo.episode(2),
            ]),
            initialIndex: 0,
            localOnly: true,
            repository: repo,
            store: await store(),
            playerFactory: () => Player(platformPlayer: player),
            videoBuilder: (controls) => controls,
          ),
        ),
      );
      await tick(tester);
      expect(find.text('正在读取本地视频'), findsWidgets);
      expect(repo.catalogUpdates.coverRevision(FixtureRepository.free.id), 0);
      expect(repo.onlineReads, 0);
      repo.pendingLocal!.complete(repo.local);
      await tick(tester);
      expect(player.opened.single.uri, contains('offline.mp4'));
      await tester.tap(find.byTooltip('下一集').last);
      await tick(tester);
      expect(repo.localReads, 2);
      expect(repo.onlineReads, 0);
      await tester.pumpWidget(const SizedBox.shrink());
      await tick(tester);
      expect(player.disposed, isTrue);
    },
  );
  testWidgets(
    'download tap is single-flight and hidden download polling is paused',
    (tester) async {
      final repo = OfflinePlaybackRepository();
      var pages = 0;
      await tester.pumpWidget(
        MaterialApp(
          home: DownloadsScreen(
            repository: repo,
            store: await store(),
            playerBuilder: (_, _, _) {
              pages++;
              return const Scaffold(body: Text('local player'));
            },
          ),
        ),
      );
      await tick(tester);
      await tester.tap(find.text(FixtureRepository.free.title));
      await tick(tester);
      final tile = find.descendant(
        of: find.byKey(const ValueKey('download-task-1')),
        matching: find.byType(ListTile),
      );
      await tester.tap(tile);
      await tester.tap(tile);
      await tester.tap(tile);
      await tester.pumpAndSettle();
      expect(pages, 1);
      final reads = repo.downloadReads;
      await tester.pump(const Duration(seconds: 5));
      expect(repo.downloadReads, reads);
      tester.state<NavigatorState>(find.byType(Navigator)).pop();
      await tick(tester);
      expect(repo.downloadReads, greaterThan(reads));
      await tester.pumpWidget(const SizedBox.shrink());
      expect(tester.takeException(), isNull);
    },
  );
}
