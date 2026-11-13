import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';

import 'package:duanju_app/source_status.dart';
import 'package:duanju_app/sources_screen.dart';

void main() {
  final status = SourceStatus.fromJson({
    'source': 'huangguoai',
    'mirrors': [
      {
        'origin': 'https://ready.example',
        'kind': 'huangguoai',
        'state': 'ready',
        'addedOn': '2026-10-23',
        'addedFrom': 'https://publication.example',
        'effectiveOrigin': 'https://canonical.example',
        'poster': true,
        'posterWidth': 300,
        'posterHeight': 400,
        'posterBytes': 8000,
        'posterMessage': '真实海报解码通过',
        'browse': true,
        'selection': true,
        'playback': true,
        'width': 1280,
        'height': 720,
        'positionMs': 500,
        'message': '真实解码通过',
      },
      {
        'origin': 'https://failed.example',
        'kind': 'huangguoai',
        'state': 'failed',
        'addedOn': '2026-10-23',
        'message': '首页访问失败：HTTP 503',
      },
    ],
    'mirrorPublications': [
      {
        'url': 'https://publication.example',
        'state': 'ok',
        'message': '读取2个地址，新增1个；旧地址全部保留',
      },
    ],
  });

  test('mirror activation requires actual decoded progress', () {
    expect(status.mirrors.first.usable, isTrue);
    expect(status.mirrors.last.usable, isFalse);
    for (final missing in [
      'poster',
      'posterWidth',
      'posterHeight',
      'posterBytes',
      'browse',
      'selection',
      'playback',
      'width',
      'positionMs',
    ]) {
      final input = {
        'state': 'ready',
        'poster': true,
        'posterWidth': 300,
        'posterHeight': 400,
        'posterBytes': 8000,
        'browse': true,
        'selection': true,
        'playback': true,
        'width': 1280,
        'height': 720,
        'positionMs': 500,
      }..remove(missing);
      expect(SourceMirror.fromJson(input).usable, isFalse, reason: missing);
    }
  });

  testWidgets('management preserves failure, date, type and redirect reason', (
    tester,
  ) async {
    await tester.pumpWidget(
      MaterialApp(
        home: Scaffold(
          body: SingleChildScrollView(
            child: HuangguoMirrorsPanel(status: status),
          ),
        ),
      ),
    );
    await tester.tap(find.text('备用线路（2）'));
    await tester.pumpAndSettle();
    expect(find.text('https://ready.example'), findsOneWidget);
    expect(find.text('https://failed.example'), findsOneWidget);
    expect(find.text('首页访问失败：HTTP 503'), findsOneWidget);
    expect(find.textContaining('2026-10-23'), findsNWidgets(2));
    expect(find.textContaining('实际落点'), findsOneWidget);
    expect(find.textContaining('可自动切换'), findsOneWidget);
    expect(find.textContaining('旧地址全部保留'), findsOneWidget);
    expect(tester.takeException(), isNull);
  });

  testWidgets(
    'duplicates are folded while their failure reasons remain accessible',
    (tester) async {
      tester.view.physicalSize = const Size(360, 800);
      tester.view.devicePixelRatio = 1;
      addTearDown(tester.view.resetPhysicalSize);
      addTearDown(tester.view.resetDevicePixelRatio);
      final grouped = SourceStatus.fromJson({
        'source': 'huangguoai',
        'mirrors': [
          {
            'origin': 'https://canonical.example',
            'kind': 'huangguoai',
            'state': 'ready',
            'poster': true,
            'posterWidth': 300,
            'posterHeight': 400,
            'posterBytes': 8000,
            'browse': true,
            'selection': true,
            'playback': true,
            'width': 1280,
            'height': 720,
            'positionMs': 500,
            'message': '四项通过',
          },
          {
            'origin': 'https://failed-alias.example',
            'effectiveOrigin': 'https://canonical.example',
            'duplicateOf': 'https://canonical.example',
            'kind': 'huangguoai',
            'state': 'failed',
            'addedOn': '2026-10-23',
            'message': '该重复地址本次连接失败：EOF',
          },
        ],
      });
      await tester.pumpWidget(
        MaterialApp(
          home: Scaffold(
            body: SingleChildScrollView(
              child: HuangguoMirrorsPanel(status: grouped),
            ),
          ),
        ),
      );
      await tester.tap(find.text('备用线路（1）'));
      await tester.pumpAndSettle();
      expect(find.text('https://canonical.example'), findsOneWidget);
      expect(find.text('https://failed-alias.example'), findsNothing);
      await tester.tap(find.text('已合并的重复地址（1）'));
      await tester.pumpAndSettle();
      expect(find.text('https://failed-alias.example'), findsOneWidget);
      expect(find.text('该重复地址本次连接失败：EOF'), findsOneWidget);
      expect(grouped.mirrors.last.usable, isFalse);
      expect(tester.takeException(), isNull);
    },
  );
}
