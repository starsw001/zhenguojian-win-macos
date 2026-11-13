import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:duanju_app/models.dart';
import 'package:duanju_app/playback_routes_bar.dart';
import 'package:duanju_app/playback_recovery.dart';

void main() {
  testWidgets('access refusal disables online routes but keeps local return', (
    tester,
  ) async {
    var returned = false;
    await tester.pumpWidget(
      MaterialApp(
        home: Scaffold(
          body: PlaybackRoutesBar(
            routes: const [PlaybackRouteStatus(index: 0, status: 'failed')],
            selected: 0,
            busy: false,
            routesBlocked: true,
            onSelected: (_) =>
                fail('blocked online route must not be selected'),
            onSwitchLocal: () => returned = true,
          ),
        ),
      ),
    );
    expect(
      tester.widget<ChoiceChip>(find.byType(ChoiceChip)).onSelected,
      isNull,
    );
    await tester.tap(find.byKey(const ValueKey('playback-switch-local')));
    expect(returned, isTrue);
    expect(tester.takeException(), isNull);
  });
  testWidgets('local return and addresses fit within two shared rows', (
    tester,
  ) async {
    await tester.binding.setSurfaceSize(const Size(390, 800));
    addTearDown(() => tester.binding.setSurfaceSize(null));
    var selected = -1;
    var returned = false;
    final bar = PlaybackRoutesBar(
      routes: [
        for (var i = 0; i < 40; i++)
          PlaybackRouteStatus(index: i, status: 'unknown'),
      ],
      selected: 0,
      busy: false,
      onSelected: (i) => selected = i,
      onSwitchLocal: () => returned = true,
    );
    await tester.pumpWidget(MaterialApp(home: Scaffold(body: bar)));
    expect(bar.heightForWidth(390, 1), 88);
    await tester.tap(find.byKey(const ValueKey('playback-switch-local')));
    expect(returned, isTrue);
    await tester.ensureVisible(find.byKey(const ValueKey('playback-route-39')));
    await tester.pumpAndSettle();
    await tester.tap(find.byKey(const ValueKey('playback-route-39')));
    expect(selected, 39);
    expect(tester.takeException(), isNull);
  });

  test('every supplied candidate is attempted once without looping', () {
    final recovery = PlaybackRecovery();
    for (var i = 0; i < 40; i++) {
      expect(
        recovery.next(
          PlaybackPlan(
            url: 'fixture',
            session: 's',
            routeIndex: i,
            routeCount: 40,
          ),
          allowRefresh: false,
          allCandidates: true,
        ),
        i < 39
            ? PlaybackRecoveryAction.alternative
            : PlaybackRecoveryAction.stop,
      );
    }
  });
  test('unprobed connections stay unknown', () {
    final plan = PlaybackPlan.fromJson({
      'url': 'fixture',
      'routeCount': 3,
      'routes': [
        {'index': 0, 'status': 'passed'},
        {'index': 1, 'status': 'failed'},
        {'index': 2},
      ],
    });
    expect(plan.routes.map((r) => r.status), ['passed', 'failed', 'unknown']);
  });
  for (final width in [320.0, 390.0, 860.0, 1280.0]) {
    for (final scale in [1.0, 1.5]) {
      testWidgets('routes wrap and stay reachable $width scale $scale', (
        tester,
      ) async {
        tester.view.physicalSize = Size(width, 420);
        tester.view.devicePixelRatio = 1;
        addTearDown(tester.view.resetPhysicalSize);
        addTearDown(tester.view.resetDevicePixelRatio);
        var selected = -1;
        final routes = List.generate(
          40,
          (i) => PlaybackRouteStatus(
            index: i,
            quality: 1080,
            status: i == 0
                ? 'passed'
                : i == 1
                ? 'failed'
                : 'unknown',
          ),
        );
        Widget page(bool busy) => MaterialApp(
          builder: (context, child) => MediaQuery(
            data: MediaQuery.of(
              context,
            ).copyWith(textScaler: TextScaler.linear(scale)),
            child: child!,
          ),
          home: Scaffold(
            body: PlaybackRoutesBar(
              routes: routes,
              selected: 0,
              busy: busy,
              onSelected: (i) => selected = i,
            ),
          ),
        );
        await tester.pumpWidget(page(false));
        expect(tester.takeException(), isNull);
        expect(find.byIcon(Icons.check_circle), findsOneWidget);
        expect(find.byIcon(Icons.cancel), findsOneWidget);
        final top = tester.getTopLeft(
          find.byKey(const ValueKey('playback-route-0')),
        );
        final lower = tester.getTopLeft(
          find.byKey(const ValueKey('playback-route-15')),
        );
        expect(lower.dy, greaterThan(top.dy));
        await tester.tap(find.byKey(const ValueKey('playback-route-1')));
        expect(selected, 1);
        await tester.ensureVisible(
          find.byKey(const ValueKey('playback-route-39')),
        );
        await tester.pumpAndSettle();
        await tester.tap(find.byKey(const ValueKey('playback-route-39')));
        expect(selected, 39);
        expect(tester.takeException(), isNull);
        await tester.pumpWidget(page(true));
        expect(
          tester
              .widgetList<ChoiceChip>(find.byType(ChoiceChip))
              .every((c) => c.onSelected == null),
          isTrue,
        );
      });
    }
  }
}
