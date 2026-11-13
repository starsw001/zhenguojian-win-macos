import 'package:duanju_app/models.dart';
import 'package:duanju_app/playback_routes_bar.dart';
import 'package:duanju_app/playback_routes_header.dart';
import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';

void main() {
  Future<void> mount(
    WidgetTester tester, {
    required double width,
    required int count,
    double scale = 1,
    bool local = false,
    VoidCallback? onSwitchLocal,
    ValueChanged<int>? onSelected,
  }) async {
    await tester.binding.setSurfaceSize(Size(width, 900));
    await tester.pumpWidget(
      MaterialApp(
        home: Builder(
          builder: (context) => MediaQuery(
            data: MediaQuery.of(
              context,
            ).copyWith(textScaler: TextScaler.linear(scale)),
            child: Builder(
              builder: (context) => Scaffold(
                appBar: PlaybackRoutesHeader(
                  title: '回到古代当战神',
                  routes: PlaybackRoutesBar(
                    routes: [
                      for (var i = 0; i < count; i++)
                        PlaybackRouteStatus(index: i, status: 'passed'),
                    ],
                    selected: 0,
                    busy: false,
                    local: local,
                    onSwitchOnline: local ? () {} : null,
                    onSwitchLocal: onSwitchLocal,
                    onSelected: onSelected ?? (_) {},
                  ),
                  width: width,
                  textScaler: MediaQuery.textScalerOf(context),
                  titleStyle: Theme.of(context).textTheme.titleLarge!,
                  textDirection: TextDirection.ltr,
                  onRotate: () {},
                ),
                body: const SizedBox.expand(),
              ),
            ),
          ),
        ),
      ),
    );
    await tester.pumpAndSettle();
  }

  testWidgets('single address stays beside title with no empty lower row', (
    tester,
  ) async {
    addTearDown(() => tester.binding.setSurfaceSize(null));
    var chosen = -1;
    await mount(tester, width: 1500, count: 1, onSelected: (i) => chosen = i);
    final title = tester.getCenter(
      find.byKey(const ValueKey('playback-header-title')),
    );
    final chip = tester.getCenter(
      find.byKey(const ValueKey('playback-route-0')),
    );
    expect(chip.dy, closeTo(title.dy, 1));
    expect(chip.dx, greaterThan(title.dx));
    expect(
      find.byKey(const ValueKey('playback-header-overflow')),
      findsNothing,
    );
    await tester.tap(find.byKey(const ValueKey('playback-route-0')));
    expect(chosen, 0);
    expect(tester.takeException(), isNull);
  });

  testWidgets('only remaining addresses wrap and resize back to inline', (
    tester,
  ) async {
    addTearDown(() => tester.binding.setSurfaceSize(null));
    await mount(tester, width: 800, count: 10);
    final first = tester.getCenter(
      find.byKey(const ValueKey('playback-route-0')),
    );
    final last = tester.getCenter(
      find.byKey(const ValueKey('playback-route-9')),
    );
    expect(last.dy, greaterThan(first.dy));
    expect(first.dy, closeTo(tester.getCenter(find.text('回到古代当战神')).dy, 1));
    expect(tester.takeException(), isNull);
    await mount(tester, width: 1800, count: 10);
    expect(
      find.byKey(const ValueKey('playback-header-overflow')),
      findsNothing,
    );
    expect(tester.takeException(), isNull);
  });

  testWidgets(
    'large text and many addresses retain bounded vertical scrolling',
    (tester) async {
      addTearDown(() => tester.binding.setSurfaceSize(null));
      await mount(tester, width: 480, count: 80, scale: 2);
      final header = tester.widget<PlaybackRoutesHeader>(
        find.byType(PlaybackRoutesHeader),
      );
      expect(header.layout.overflowHeight, lessThanOrEqualTo(176));
      expect(find.byKey(const ValueKey('playback-route-79')), findsOneWidget);
      expect(tester.takeException(), isNull);
    },
  );

  testWidgets('inline and overflow addresses share a two row budget', (
    tester,
  ) async {
    addTearDown(() => tester.binding.setSurfaceSize(null));
    var selected = -1;
    await mount(tester, width: 800, count: 40, onSelected: (i) => selected = i);
    final header = tester.widget<PlaybackRoutesHeader>(
      find.byType(PlaybackRoutesHeader),
    );
    expect(header.layout.inlineCount, greaterThan(0));
    expect(header.layout.overflowHeight, 44);
    await tester.ensureVisible(find.byKey(const ValueKey('playback-route-39')));
    await tester.pumpAndSettle();
    await tester.tap(find.byKey(const ValueKey('playback-route-39')));
    expect(selected, 39);
    expect(tester.takeException(), isNull);
  });

  testWidgets('local online controls remain available in responsive header', (
    tester,
  ) async {
    addTearDown(() => tester.binding.setSurfaceSize(null));
    await mount(tester, width: 900, count: 0, local: true);
    expect(find.text('正在播放本地视频'), findsOneWidget);
    expect(
      find.byKey(const ValueKey('playback-switch-online')),
      findsOneWidget,
    );
    var returned = false;
    await mount(
      tester,
      width: 700,
      count: 5,
      onSwitchLocal: () => returned = true,
    );
    await tester.tap(find.byKey(const ValueKey('playback-switch-local')));
    expect(returned, isTrue);
    expect(tester.takeException(), isNull);
  });
}
