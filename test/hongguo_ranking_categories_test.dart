import 'dart:async';
import 'dart:convert';
import 'dart:io';
import 'package:duanju_app/models.dart';
import 'package:duanju_app/ranking_models.dart';
import 'package:duanju_app/rankings_screen.dart';
import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:shared_preferences/shared_preferences.dart';
import 'fixtures.dart';

class RankingMatrixRepository extends FixtureRepository {
  final requests = <String>[];
  Completer<RankingPage>? delayed;
  String delayID = '';
  late final List<RankingBoard> boards = [
    for (final dynamic row
        in (jsonDecode(
                  File(
                    'native/core/testdata/hongguo_ranking_navigation.json',
                  ).readAsStringSync(),
                )
                as Map)['expected']
            as List)
      RankingBoard(
        id: routeID(row['path'] as String),
        source: 'hongguo',
        name: row['name'] as String,
        categoryName: row['group'] as String,
        categoryId: groupID(row['group'] as String),
      ),
  ];
  static String groupID(String value) =>
      const {'全部': 'all', '漫剧': 'comic', '真人剧': 'real', 'AI剧': 'ai'}[value]!;
  static String routeID(String path) =>
      const {
        'hot-drama': 'hongguo-hot',
        'hot-comic-drama': 'hongguo-comic',
        'hot-real-drama': 'hongguo-real',
        'hot-ai-drama': 'hongguo-ai',
      }[path] ??
      'hongguo-web-$path';
  @override
  Future<List<RankingBoard>> rankingBoards() async => boards;
  RankingPage result(String board, int page) => RankingPage(
    items: [
      RankingItem(
        (page - 1) * 20 + 1,
        Drama(
          id: 'hongguo:$board:$page',
          source: 'hongguo',
          title: '$board 数据页$page',
          episodes: 20,
          description: '源站预约作品简介',
        ),
        metric: board.endsWith('subscribe-drama') ? '3555.6万期待' : '',
        status: board.endsWith('subscribe-drama')
            ? '预计12月18日上线 · 268.2万人预约'
            : '',
        reservationOnly: board.endsWith('subscribe-drama'),
      ),
    ],
    page: page,
    hasMore: page == 1,
    updatedText: '源站榜单样本',
  );
  @override
  Future<RankingPage> rankings(
    String board, {
    int page = 1,
    bool force = false,
  }) async {
    requests.add('$board|$page');
    if (board == delayID && delayed != null) return delayed!.future;
    return result(board, page);
  }
}

void main() {
  testWidgets(
    'source ranking categories expose 7 3 5 5 boards and bind every selection',
    (tester) async {
      tester.view.physicalSize = const Size(1280, 900);
      tester.view.devicePixelRatio = 1;
      addTearDown(tester.view.resetPhysicalSize);
      addTearDown(tester.view.resetDevicePixelRatio);
      SharedPreferences.setMockInitialValues({});
      final store = testStore(await SharedPreferences.getInstance());
      final repository = RankingMatrixRepository();
      await tester.pumpWidget(
        MaterialApp(
          home: RankingsScreen(
            repository: repository,
            store: store,
            initialGroup: 'hongguo',
          ),
        ),
      );
      await tester.pumpAndSettle();
      for (final group in ['全部', '漫剧', '真人剧', 'AI剧']) {
        await tester.tap(
          find.byKey(
            ValueKey('category-${RankingMatrixRepository.groupID(group)}'),
          ),
        );
        await tester.pumpAndSettle();
        final current = repository.boards
            .where((b) => b.categoryName == group)
            .toList();
        final boardBar = find.byKey(const ValueKey('ranking-boards-hongguo'));
        expect(
          find.descendant(of: boardBar, matching: find.byType(ChoiceChip)),
          findsNWidgets(current.length),
        );
        for (final board in current) {
          await tester.tap(find.byKey(ValueKey('category-${board.id}')));
          await tester.pumpAndSettle();
          expect(repository.requests.last, '${board.id}|1');
          expect(find.text('${board.id} 数据页1'), findsOneWidget);
          expect(RankingBoard.sourceForID(board.id), 'hongguo');
        }
      }
      await tester.tap(find.text('加载更多'));
      await tester.pumpAndSettle();
      expect(repository.requests.last.endsWith('|2'), isTrue);
      expect(find.textContaining('数据页2'), findsOneWidget);
      await tester.pumpWidget(const SizedBox.shrink());
      store.dispose();
    },
  );
  testWidgets(
    'ranking category switch ignores the previous category late response',
    (tester) async {
      tester.view.physicalSize = const Size(1280, 900);
      tester.view.devicePixelRatio = 1;
      addTearDown(tester.view.resetPhysicalSize);
      addTearDown(tester.view.resetDevicePixelRatio);
      SharedPreferences.setMockInitialValues({});
      final store = testStore(await SharedPreferences.getInstance());
      final repository = RankingMatrixRepository();
      await tester.pumpWidget(
        MaterialApp(
          home: RankingsScreen(
            repository: repository,
            store: store,
            initialGroup: 'hongguo',
          ),
        ),
      );
      await tester.pumpAndSettle();
      repository.delayID = 'hongguo-web-prestige-drama';
      repository.delayed = Completer<RankingPage>();
      await tester.tap(find.byKey(ValueKey('category-${repository.delayID}')));
      await tester.pump();
      await tester.tap(find.byKey(const ValueKey('category-comic')));
      await tester.pumpAndSettle();
      repository.delayed!.complete(repository.result(repository.delayID, 1));
      await tester.pumpAndSettle();
      expect(find.text('hongguo-comic 数据页1'), findsOneWidget);
      expect(find.text('${repository.delayID} 数据页1'), findsNothing);
      await tester.pumpWidget(const SizedBox.shrink());
      store.dispose();
    },
  );
  testWidgets(
    'upcoming ranking opens source information without trying playback',
    (tester) async {
      tester.view.physicalSize = const Size(1280, 900);
      tester.view.devicePixelRatio = 1;
      addTearDown(tester.view.resetPhysicalSize);
      addTearDown(tester.view.resetDevicePixelRatio);
      SharedPreferences.setMockInitialValues({});
      final store = testStore(await SharedPreferences.getInstance());
      final repository = RankingMatrixRepository();
      await tester.pumpWidget(
        MaterialApp(
          home: RankingsScreen(
            repository: repository,
            store: store,
            initialGroup: 'hongguo',
          ),
        ),
      );
      await tester.pumpAndSettle();
      await tester.tap(
        find.byKey(const ValueKey('category-hongguo-web-subscribe-drama')),
      );
      await tester.pumpAndSettle();
      expect(find.text('3555.6万期待'), findsOneWidget);
      expect(find.textContaining('预计12月18日上线'), findsOneWidget);
      await tester.tap(find.text('hongguo-web-subscribe-drama 数据页1'));
      await tester.pumpAndSettle();
      expect(find.textContaining('尚未上线，不提供播放'), findsOneWidget);
      expect(repository.detailCalls, 0);
      await tester.tap(find.text('关闭'));
      await tester.pumpAndSettle();
      await tester.pumpWidget(const SizedBox.shrink());
      store.dispose();
    },
  );
}
