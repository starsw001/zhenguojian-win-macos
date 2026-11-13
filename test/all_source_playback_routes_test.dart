import 'package:duanju_app/local_store.dart';
import 'package:duanju_app/models.dart';
import 'package:duanju_app/player_screen.dart';
import 'package:duanju_app/playback_routes_bar.dart';
import 'package:flutter/foundation.dart';
import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:media_kit/media_kit.dart';
import 'package:shared_preferences/shared_preferences.dart';
import 'player_fixtures.dart';
import 'local_playback_routes_test.dart' show LocalRouteRepository;

void main() {
  Future<void> settle(WidgetTester t) async {
    for (var i = 0; i < 15; i++) {
      await t.pump(const Duration(milliseconds: 20));
    }
  }

  for (final source in ['hongguo', 'wuguo', 'yeguo', 'sorani']) {
    testWidgets('$source preserves position paused state and local return', (
      tester,
    ) async {
      debugDefaultTargetPlatformOverride = TargetPlatform.windows;
      tester.view.physicalSize = const Size(1100, 760);
      tester.view.devicePixelRatio = 1;
      addTearDown(() {
        debugDefaultTargetPlatformOverride = null;
        tester.view.resetPhysicalSize();
        tester.view.resetDevicePixelRatio();
      });
      SharedPreferences.setMockInitialValues({});
      final store = LocalStore(await SharedPreferences.getInstance());
      final repo = LocalRouteRepository();
      final player = ScriptedPlayer();
      final drama = Drama(
        id: '$source:100',
        source: source,
        title: 'Synthetic route fixture',
        episodes: 2,
      );
      await tester.pumpWidget(
        MaterialApp(
          home: PlayerScreen(
            detail: await repo.detail(drama),
            initialIndex: 0,
            initialPosition: 7,
            repository: repo,
            store: store,
            playerFactory: () => Player(platformPlayer: player),
            videoBuilder: (controls) => controls,
          ),
        ),
      );
      await settle(tester);
      expect(repo.onlineReads, 0);
      await player.seek(const Duration(seconds: 28));
      await tester.pump();
      await tester.tap(find.byTooltip('暂停播放'));
      await settle(tester);
      await tester.tap(find.byKey(const ValueKey('playback-switch-online')));
      await settle(tester);
      expect(repo.checked.toSet(), List.generate(10, (i) => i).toSet());
      expect(
        tester
            .widget<PlaybackRoutesBar>(find.byType(PlaybackRoutesBar))
            .routes[1]
            .status,
        'failed',
      );
      await tester.tap(find.byKey(const ValueKey('playback-route-2')));
      await settle(tester);
      expect(repo.selected, [2]);
      expect(player.opened.last.start, const Duration(seconds: 28));
      expect(player.played.last, isFalse);
      await tester.tap(find.byKey(const ValueKey('playback-switch-local')));
      await settle(tester);
      expect(player.opened.last.start, const Duration(seconds: 28));
      expect(player.played.last, isFalse);
      expect(find.text('正在播放本地视频'), findsOneWidget);
      expect(repo.downloadWrites, 0);
      expect(tester.takeException(), isNull);
      await tester.pumpWidget(const SizedBox.shrink());
      await settle(tester);
      expect(player.disposed, isTrue);
      debugDefaultTargetPlatformOverride = null;
    });
  }
}
