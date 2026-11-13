import 'package:duanju_app/models.dart';
import 'package:duanju_app/player_menu.dart';
import 'package:duanju_app/episode_browser.dart';
import 'package:duanju_app/download_picker.dart';
import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';

void main() {
  final duplicateLines = [
    Episode({'id': 'paid-1', 'currentEpisode': 1, 'vip': true}, 1),
    Episode({'id': 'free-1', 'currentEpisode': 1}, 1),
    Episode({'id': 'free-2', 'currentEpisode': 2}, 2),
    Episode({'id': 'alternate-2', 'currentEpisode': 2}, 2),
  ];

  test(
    'detail uses logical episode order for next playback and retains all original lines',
    () {
      final detail = DramaDetail(
        const Drama(id: 'hongguo:lines', source: 'hongguo', title: '线路合成数据'),
        duplicateLines,
      );
      expect(detail.episodes.map((e) => e.id), ['free-1', 'free-2']);
      expect(detail.rawEpisodes, same(duplicateLines));
      expect(detail.episodes[1].number, 2);
    },
  );

  testWidgets(
    'duplicate source lines have one grid cell and preserve original playback indices',
    (tester) async {
      final selected = <int>[];
      await tester.pumpWidget(
        MaterialApp(
          home: Scaffold(
            body: EpisodeBrowser(
              episodes: duplicateLines,
              onSelected: selected.add,
            ),
          ),
        ),
      );
      expect(find.byKey(const ValueKey('episode-1')), findsOneWidget);
      expect(find.byKey(const ValueKey('episode-2')), findsOneWidget);
      await tester.tap(find.byKey(const ValueKey('episode-1')));
      await tester.tap(find.byKey(const ValueKey('episode-2')));
      expect(selected, [1, 2]);
      expect(tester.takeException(), isNull);
    },
  );

  testWidgets(
    'download picker submits one free entry for each logical episode',
    (tester) async {
      DownloadSelection? selection;
      await tester.pumpWidget(
        MaterialApp(
          home: DownloadPicker(
            detail: DramaDetail(
              const Drama(
                id: 'hongguo:lines',
                source: 'hongguo',
                title: '线路合成数据',
              ),
              duplicateLines,
            ),
            onSubmit: (value) async => selection = value,
          ),
        ),
      );
      await tester.tap(find.byKey(const ValueKey('enqueue-downloads')));
      await tester.pumpAndSettle();
      expect(selection!.episodes.map((e) => e.id), ['free-1', 'free-2']);
      expect(tester.takeException(), isNull);
    },
  );

  final episodes = [
    for (var i = 1; i <= 7; i++) Episode({'id': '$i', 'currentEpisode': i}, i),
  ];

  testWidgets(
    'player checkboxes download one episode without changing playback and select all seven',
    (tester) async {
      final played = <int>[];
      final downloaded = <List<int>>[];
      await tester.pumpWidget(
        MaterialApp(
          home: Scaffold(
            body: SizedBox(
              width: 340,
              height: 500,
              child: PlayerEpisodeGrid(
                episodes: episodes,
                currentIndex: 0,
                onSelected: played.add,
                onDownload: (items) async =>
                    downloaded.add(items.map((e) => e.number).toList()),
              ),
            ),
          ),
        ),
      );
      await tester.tap(find.byKey(const ValueKey('play-episode-download-2')));
      await tester.pump();
      expect(played, isEmpty);
      await tester.tap(find.byKey(const ValueKey('player-download-selected')));
      await tester.pumpAndSettle();
      expect(downloaded, [
        [2],
      ]);
      expect(played, isEmpty);
      await tester.tap(find.byKey(const ValueKey('play-episode-3')));
      await tester.pump();
      expect(played, [2]);
      await tester.tap(
        find.byKey(const ValueKey('player-select-all-downloads')),
      );
      await tester.pump();
      expect(
        tester
            .widgetList<Checkbox>(find.byType(Checkbox))
            .every((c) => c.value == true),
        isTrue,
      );
      await tester.tap(find.byKey(const ValueKey('player-download-selected')));
      await tester.pumpAndSettle();
      expect(downloaded.last, [1, 2, 3, 4, 5, 6, 7]);
      await tester.tap(find.byKey(const ValueKey('player-download-all')));
      await tester.pumpAndSettle();
      expect(downloaded.last, [1, 2, 3, 4, 5, 6, 7]);
      expect(tester.takeException(), isNull);
    },
  );

  testWidgets('read-only player has no download selectors', (tester) async {
    await tester.pumpWidget(
      MaterialApp(
        home: Scaffold(
          body: PlayerEpisodeGrid(
            episodes: episodes,
            currentIndex: 0,
            onSelected: (_) {},
          ),
        ),
      ),
    );
    expect(find.byType(Checkbox), findsNothing);
    expect(find.byKey(const ValueKey('player-download-all')), findsNothing);
  });

  testWidgets(
    'coin and VIP episodes stay visible but select-all does not request paid media',
    (tester) async {
      final selected = <int>[];
      final paid = [
        episodes.first,
        for (var i = 2; i <= 7; i++)
          Episode({'id': '$i', 'currentEpisode': i, 'vip': true}, i),
      ];
      await tester.pumpWidget(
        MaterialApp(
          home: Scaffold(
            body: PlayerEpisodeGrid(
              episodes: paid,
              currentIndex: 0,
              onSelected: (_) {},
              onDownload: (items) async =>
                  selected.addAll(items.map((e) => e.number)),
            ),
          ),
        ),
      );
      expect(
        tester
            .widget<Checkbox>(
              find.byKey(const ValueKey('play-episode-download-2')),
            )
            .onChanged,
        isNull,
      );
      await tester.tap(find.byKey(const ValueKey('player-download-all')));
      await tester.pumpAndSettle();
      expect(selected, [1]);
      expect(find.byKey(const ValueKey('play-episode-7')), findsOneWidget);
    },
  );
}
