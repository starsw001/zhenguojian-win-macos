import 'dart:async';

import 'package:duanju_app/local_store.dart';
import 'package:duanju_app/player_screen.dart';
import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:media_kit/media_kit.dart';
import 'package:shared_preferences/shared_preferences.dart';

import 'fixtures.dart';
import 'player_fixtures.dart';

class DelayedRatePlayer extends ScriptedPlayer {
  Completer<void>? heldRate;
  int stops = 0;
  @override
  Future<void> stop() async {
    stops++;
    await super.stop();
  }

  @override
  Future<void> setRate(double rate) async {
    if (heldRate != null) await heldRate!.future;
    await super.setRate(rate);
  }
}

void main() {
  testWidgets(
    'leaving playback stops media before a delayed rate update completes',
    (tester) async {
      final messenger =
          TestDefaultBinaryMessengerBinding.instance.defaultBinaryMessenger;
      messenger.setMockMethodCallHandler(
        const MethodChannel('window_manager'),
        (_) async => null,
      );
      addTearDown(
        () => messenger.setMockMethodCallHandler(
          const MethodChannel('window_manager'),
          null,
        ),
      );
      SharedPreferences.setMockInitialValues({});
      final store = LocalStore(await SharedPreferences.getInstance());
      addTearDown(store.dispose);
      final repository = RouteRepository();
      final platform = DelayedRatePlayer();
      final detail = await repository.detail(FixtureRepository.free);
      await tester.pumpWidget(
        MaterialApp(
          theme: ThemeData.dark(),
          home: PlayerScreen(
            detail: detail,
            initialIndex: 0,
            repository: repository,
            store: store,
            playerFactory: () => Player(platformPlayer: platform),
            videoBuilder: (controls) => controls,
          ),
        ),
      );
      for (var i = 0; i < 15; i++) {
        await tester.pump(const Duration(milliseconds: 10));
      }
      await platform.seek(const Duration(seconds: 12));
      await tester.pump();
      final rate = Completer<void>();
      platform.heldRate = rate;
      await tester.tap(find.byKey(const ValueKey('player-speed')));
      await tester.pumpAndSettle();
      await tester.tap(find.text('1.5x').last);
      await tester.pump(const Duration(milliseconds: 100));
      final stops = platform.stops;
      await tester.pumpWidget(const SizedBox.shrink());
      await tester.pump(const Duration(milliseconds: 100));
      expect(platform.stops, stops + 1);
      expect(platform.disposed, isFalse);
      rate.complete();
      platform.heldRate = null;
      for (var i = 0; i < 15; i++) {
        await tester.pump(const Duration(milliseconds: 10));
      }
      expect(platform.disposed, isTrue);
      expect(store.history.single.position, 12);
      expect(tester.takeException(), isNull);
    },
  );
}
