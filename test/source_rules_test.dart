import 'package:duanju_app/models.dart';
import 'package:duanju_app/source_status.dart';
import 'package:duanju_app/sources_screen.dart';
import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:shared_preferences/shared_preferences.dart';

import 'fixtures.dart';
import 'sources_test.dart' show SourceFixtureRepository;

class ConnectionOnlyRepository extends SourceFixtureRepository {
  int resolveCalls = 0;
  @override
  Future<PlaybackPlan> resolve(
    Drama drama,
    Episode episode, {
    int quality = 0,
  }) async {
    resolveCalls++;
    throw StateError('connection checks must not initialize playback');
  }

  @override
  bool get supportsRealSourcePlayback => true;

  @override
  Future<SourceStatus> startSourceJob(
    String source,
    String operation, {
    Drama? drama,
  }) async {
    operations.add('$source:$operation');
    return SourceStatus.fromJson({
      'source': source,
      'running': false,
      'operation': operation,
    });
  }
}

void main() {
  test('catalog page preserves source supplied continuation cursor', () {
    final page = CatalogPage.fromJson({
      'items': <Object>[],
      'page': 1,
      'nextPage': 7,
      'hasMore': true,
    });
    expect(page.nextPage, 7);
    expect(page.hasMore, isTrue);
  });

  test(
    'every compiled source sends search to native local then online rules',
    () {
      for (final source in SourceSite.values) {
        expect(source.onlineSearch, isTrue, reason: source.id);
        expect(source.pagedSearch, isTrue, reason: source.id);
      }
    },
  );

  test('connected video mirror is explicitly not decoded AI playback', () {
    final mirror = SourceMirror.fromJson({
      'kind': 'huangguo-video',
      'state': 'connected',
      'browse': true,
      'selection': true,
      'mediaReachable': true,
      'poster': true,
      'posterWidth': 10,
      'posterHeight': 10,
      'posterBytes': 40,
    });
    expect(mirror.usable, isTrue);
    expect(mirror.playback, isFalse);
    expect(mirror.label, contains('未实播'));
    expect(
      SourceMirror.fromJson({
        'kind': 'huangguoai',
        'state': 'connected',
      }).usable,
      isFalse,
    );
  });

  testWidgets('non AI source checks never initialize real playback', (
    tester,
  ) async {
    tester.view.physicalSize = const Size(1280, 900);
    tester.view.devicePixelRatio = 1;
    addTearDown(tester.view.reset);
    SharedPreferences.setMockInitialValues({});
    final store = testStore(await SharedPreferences.getInstance());
    final repository = ConnectionOnlyRepository();
    for (final source in SourceSite.values.where((s) => s.id != 'huangguoai')) {
      await tester.pumpWidget(
        MaterialApp(
          home: SourcesScreen(
            key: ValueKey(source.id),
            repository: repository,
            store: store,
            initialSource: source.id,
          ),
        ),
      );
      await tester.pumpAndSettle();
      expect(
        find.descendant(
          of: find.byKey(ValueKey('check-${source.id}')),
          matching: find.text('检测浏览、搜索与线路连接'),
        ),
        findsOneWidget,
      );
      await tester.tap(find.byKey(ValueKey('check-${source.id}')));
      await tester.pumpAndSettle();
      expect(repository.operations.last, '${source.id}:check');
      expect(repository.resolveCalls, 0);
    }
    await tester.pumpWidget(const SizedBox.shrink());
    store.dispose();
  });
}
