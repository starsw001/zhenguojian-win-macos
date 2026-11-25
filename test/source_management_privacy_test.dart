import 'dart:convert';
import 'package:duanju_app/local_store.dart';
import 'package:duanju_app/source_status.dart';
import 'package:duanju_app/sources_screen.dart';
import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:shared_preferences/shared_preferences.dart';
import 'fixtures.dart';
import 'sources_test.dart' show SourceFixtureRepository;

void main() {
  testWidgets('observe source address visibility with unchanged raw status', (
    tester,
  ) async {
    tester.view.physicalSize = const Size(1280, 1600);
    tester.view.devicePixelRatio = 1;
    addTearDown(tester.view.resetPhysicalSize);
    addTearDown(tester.view.resetDevicePixelRatio);
    SharedPreferences.setMockInitialValues({});
    final LocalStore store = testStore(await SharedPreferences.getInstance());
    final repository = SourceFixtureRepository();
    final status = SourceStatus.fromJson({
      'source': 'huangguoai',
      'count': 300,
      'page': 75,
      'catalogOrigin': 'https://catalog.example.invalid',
      'catalogFullAt': '2026-11-19T06:22:00Z',
      'stage': '检测 https://stage.example.invalid 已完成',
      'error': 'https://failed.example.invalid HTTP 503，保留失败记录',
      'storageError': 'https://storage.example.invalid 保存失败',
      'mirrors': [
        {
          'origin': 'https://mirror.example.invalid',
          'kind': 'huangguoai',
          'state': 'failed',
          'addedOn': '2026-11-19',
          'addedFrom': 'https://publication.example.invalid',
          'effectiveOrigin': 'https://redirect.example.invalid',
          'message': 'https://mirror.example.invalid 连接失败',
          'lastError': 'https://previous.example.invalid HTTP 502',
          'posterMessage': '海报 https://poster.example.invalid 失败',
          'catalogMessage': 'https://browse.example.invalid 目录未通过',
        },
        {
          'origin': 'https://alias.example.invalid',
          'duplicateOf': 'https://mirror.example.invalid',
          'kind': 'huangguoai',
          'message': 'https://alias.example.invalid 重复地址',
        },
      ],
      'mirrorPublications': [
        {
          'url': 'https://publication.example.invalid',
          'message': 'https://publication.example.invalid 已读取',
        },
      ],
      'health': {
        'state': 'partial',
        'sample': '检测 https://sample.example.invalid',
        'networkMode': 'auto',
        'steps': [
          {
            'name': '浏览',
            'state': 'failed',
            'message': 'https://health.example.invalid HTTP 503',
            'host': 'health.example.invalid',
            'httpStatus': 503,
            'elapsedMs': 12,
          },
        ],
      },
      'healthHistory': [
        {
          'state': 'failed',
          'steps': [
            {
              'name': '浏览',
              'state': 'failed',
              'message': 'https://history.example.invalid HTTP 502',
            },
          ],
        },
      ],
    });
    repository.statuses['huangguoai'] = status;
    await tester.pumpWidget(
      MaterialApp(
        home: SourcesScreen(
          repository: repository,
          store: store,
          initialSource: 'huangguoai',
        ),
      ),
    );
    await tester.pumpAndSettle();
    for (final label in ['备用线路（1）', '已合并的重复地址（1）', '历史检测（1）']) {
      final f = find.text(label);
      await tester.ensureVisible(f);
      await tester.tap(f);
      await tester.pumpAndSettle();
    }
    final toggle = find.byKey(const ValueKey('health-toggle-huangguoai'));
    await tester.ensureVisible(toggle);
    await tester.tap(toggle);
    await tester.pumpAndSettle();
    final texts = <String>[
      ...tester
          .widgetList<Text>(find.byType(Text))
          .map((w) => w.data ?? w.textSpan?.toPlainText() ?? ''),
      ...tester
          .widgetList<SelectableText>(find.byType(SelectableText))
          .map((w) => w.data ?? w.textSpan?.toPlainText() ?? ''),
    ];
    final urls = texts.where((s) => s.contains('example.invalid')).toList();
    expect(urls, isEmpty);
    expect(status.catalogOrigin, 'https://catalog.example.invalid');
    print(
      'SOURCE_URL_PROBE ' +
          jsonEncode({
            'visibleURLCount': urls.length,
            'rawCatalogRetained':
                status.catalogOrigin == 'https://catalog.example.invalid',
            'rawMirrorRetained':
                status.mirrors.first.origin == 'https://mirror.example.invalid',
            'hasFailureReason': texts.any((s) => s.contains('HTTP 503')),
            'hasMirrorResults': texts.any((s) => s.contains('海报：')),
            'hasSyncTime': texts.any((s) => s.contains('最近完整同步：')),
            'networkRequests': 0,
          }),
    );
    await tester.pumpWidget(const SizedBox.shrink());
    store.dispose();
  });
}
