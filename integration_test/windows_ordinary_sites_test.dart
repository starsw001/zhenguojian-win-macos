import 'dart:convert';
import 'dart:io';
import 'dart:ui' as ui;

import 'package:crypto/crypto.dart';
import 'package:flutter/material.dart';
import 'package:flutter/rendering.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:integration_test/integration_test.dart';
import 'package:path_provider_platform_interface/path_provider_platform_interface.dart';
import 'package:shared_preferences/shared_preferences.dart';
import 'package:window_manager/window_manager.dart';

import 'package:duanju_app/app_build.dart';
import 'package:duanju_app/catalog_browser.dart';
import 'package:duanju_app/core_bridge.dart';
import 'package:duanju_app/detail_screen.dart';
import 'package:duanju_app/episode_browser.dart';
import 'package:duanju_app/local_store.dart';
import 'package:duanju_app/models.dart';
import 'package:duanju_app/ordinary_catalog.dart';
import 'package:duanju_app/playback_launch_screen.dart';
import 'package:duanju_app/player_menu.dart';
import 'package:duanju_app/widgets.dart';

import 'windows_sources_test.dart' show LiveSourcePaths;

class OrdinaryLiveRepository extends NativeRepository {
  OrdinaryLiveRepository(Directory directory)
    : super(background: true, dataDirectory: directory);
  final details = <String, DramaDetail>{};
  final requests = <String>[];
  int playbackRequests = 0;
  @override
  Future<DramaDetail> detail(Drama drama) async {
    requests.add(drama.id);
    final value = await super.detail(drama);
    details[drama.id] = value;
    return value;
  }

  @override
  Future<PlaybackPlan> resolve(
    Drama drama,
    Episode episode, {
    int quality = 0,
  }) async {
    playbackRequests++;
    throw StateError('本轮只检查媒体连接，不启动实际播放器');
  }
}

void main() {
  final binding = IntegrationTestWidgetsFlutterBinding.ensureInitialized();
  testWidgets('Windows live ordinary title grouping and isolated site switching', (
    tester,
  ) async {
    expect(Platform.isWindows && allSourcesEnabled, true);
    expect(const bool.fromEnvironment('DISABLE_REMOTE_IMAGES'), false);
    const folder = String.fromEnvironment('LIVE_ORDINARY_DIRECTORY');
    const expectedCore = String.fromEnvironment('LIVE_CORE_SHA256');
    const expectedVersion = String.fromEnvironment('LIVE_ORDINARY_VERSION');
    expect(folder, isNotEmpty);
    Directory(folder).createSync(recursive: true);
    final core = File(
      '${File(Platform.resolvedExecutable).parent.path}/duanju_core.dll',
    );
    final coreHash = (await sha256.bind(core.openRead()).first).toString();
    expect(coreHash, expectedCore);
    expect(expectedVersion, isNotEmpty);
    final oldPaths = PathProviderPlatform.instance;
    PathProviderPlatform.instance = LiveSourcePaths('$folder/client-data');
    SharedPreferences.setMockInitialValues({});
    final store = LocalStore(await SharedPreferences.getInstance());
    final repository = OrdinaryLiveRepository(Directory('$folder/client-data'))
      ..access = store;
    await repository.initialize();
    await windowManager.ensureInitialized();
    await windowManager.setTitle('$appName · 普通影视真实站点检查（不实播）');
    await windowManager.setSize(const Size(1280, 820));
    await windowManager.center();
    await windowManager.show();
    tester.view.physicalSize = const Size(1280, 820);
    tester.view.devicePixelRatio = 1;
    addTearDown(tester.view.resetPhysicalSize);
    addTearDown(tester.view.resetDevicePixelRatio);
    final imageKey = GlobalKey();
    final results = <Map<String, dynamic>>[];
    final report = <String, dynamic>{
      'version': expectedVersion,
      'versionEvidence':
          'pubspec and built executable, pinned by external runner',
      'coreSHA256': coreHash,
      'remoteImagesDisabled': false,
      'syntheticData': false,
      'syntheticMedia': false,
      'isolatedPreferencesAndDownloads': true,
      'actualPlayback': false,
      'groups': results,
      'completed': false,
    };
    Future<void> checkpoint() =>
        File('$folder/windows-ordinary-result.json').writeAsString(
          const JsonEncoder.withIndent('  ').convert(report),
          flush: true,
        );
    Future<void> waitFor(bool Function() ready) async {
      final end = DateTime.now().add(const Duration(seconds: 90));
      while (!ready() && DateTime.now().isBefore(end)) {
        await tester.pump(const Duration(milliseconds: 80));
        await Future<void>.delayed(const Duration(milliseconds: 80));
      }
      expect(ready(), true);
      await tester.pump(const Duration(milliseconds: 200));
    }

    bool detailVisible(String id) {
      final found = find.byType(EpisodeBrowser);
      return found.evaluate().isNotEmpty &&
          tester
              .widget<EpisodeBrowser>(found)
              .episodes
              .every((episode) => episode.id.startsWith('$id:'));
    }

    bool entryVisible(String id) {
      final found = find.byType(PlayerEpisodeGrid);
      return found.evaluate().isNotEmpty &&
          tester
              .widget<PlayerEpisodeGrid>(found)
              .episodes
              .every((episode) => episode.id.startsWith('$id:'));
    }

    Future<String> screenshot(String label) async {
      await tester.pump(const Duration(milliseconds: 200));
      final boundary =
          imageKey.currentContext!.findRenderObject()! as RenderRepaintBoundary;
      final image = await boundary.toImage(pixelRatio: 1);
      final bytes = await image.toByteData(format: ui.ImageByteFormat.png);
      final file = File('$folder/$label.png');
      await file.writeAsBytes(bytes!.buffer.asUint8List(), flush: true);
      image.dispose();
      return file.path;
    }

    try {
      for (final sample in [
        ('情深似海终成空', ['hongguo', 'yaguo', 'maoguo', 'wuguo']),
        ('观心者说', ['fanguo', 'yaguo', 'maoguo']),
      ]) {
        final browser = CatalogBrowser(repository);
        final page = await browser.load(
          SourceGroup('ordinary-real', '普通影视', [
            for (final id in sample.$2) SourceSite.byId(id),
          ]),
          query: sample.$1,
          force: true,
        );
        final cards = page.items
            .where(
              (item) =>
                  ordinaryTitleKey(item.title) == ordinaryTitleKey(sample.$1),
            )
            .toList();
        expect(cards.length, 1);
        final drama = cards.single;
        final sites = ordinarySiteOptions(drama);
        expect(sites.length, sample.$2.length);
        final result = <String, dynamic>{
          'title': sample.$1,
          'cards': cards.length,
          'siteCount': sites.length,
          'sites': <Map<String, dynamic>>[],
          'catalogWarning': page.warning,
        };
        results.add(result);
        await tester.pumpWidget(
          RepaintBoundary(
            key: imageKey,
            child: MaterialApp(
              home: Builder(
                builder: (context) => Scaffold(
                  appBar: AppBar(title: Text('$appName · 真实普通影视目录')),
                  body: Column(
                    children: [
                      Text(
                        '${sample.$1} · ${cards.length}部 · ${sites.length}站点',
                      ),
                      SizedBox(
                        width: 200,
                        height: DramaTile.extentFor(context, 200),
                        child: DramaTile(
                          key: const ValueKey('ordinary-real-card'),
                          drama: drama,
                          repository: repository,
                          onTap: () => Navigator.of(context).push(
                            MaterialPageRoute<void>(
                              builder: (_) => DetailScreen(
                                drama: drama,
                                repository: repository,
                                store: store,
                              ),
                            ),
                          ),
                        ),
                      ),
                    ],
                  ),
                ),
              ),
            ),
          ),
        );
        final poster = await repository.cover(drama);
        final codec = await ui.instantiateImageCodec(
          await File(poster).readAsBytes(),
        );
        final frame = await codec.getNextFrame();
        result['posterSize'] = [frame.image.width, frame.image.height];
        frame.image.dispose();
        codec.dispose();
        result['catalogScreenshot'] = await screenshot(
          'catalog-${results.length}',
        );
        await tester.tap(find.byKey(const ValueKey('ordinary-real-card')));
        for (final entry in sites.indexed) {
          final site = entry.$2;
          if (entry.$1 > 0) {
            await tester.tap(
              find.byKey(ValueKey('ordinary-site-${entry.$1 + 1}')),
            );
          }
          await waitFor(
            () =>
                repository.details.containsKey(site.id) &&
                detailVisible(site.id),
          );
          final detail = repository.details[site.id]!;
          expect(detail.drama.id, site.id);
          final displayed = tester
              .widget<EpisodeBrowser>(find.byType(EpisodeBrowser))
              .episodes;
          expect(displayed.length, detail.episodes.length);
          expect(
            displayed.map((episode) => episode.id),
            detail.episodes.map((episode) => episode.id),
          );
          expect(detail.drama.episodes, detail.episodes.length);
          final posterFile = await repository.cover(detail.drama, force: true);
          final posterCodec = await ui.instantiateImageCodec(
            await File(posterFile).readAsBytes(),
          );
          final posterFrame = await posterCodec.getNextFrame();
          final posterSize = [
            posterFrame.image.width,
            posterFrame.image.height,
          ];
          if (site.source == 'wuguo')
            expect(posterSize[1], greaterThan(posterSize[0]));
          posterFrame.image.dispose();
          posterCodec.dispose();
          await waitFor(
            () => find
                .descendant(
                  of: find.byType(CachedCoverImage),
                  matching: find.byType(Image),
                )
                .evaluate()
                .isNotEmpty,
          );
          final visible = <String, dynamic>{
            'id': site.id,
            'source': site.source,
            'title': detail.drama.title,
            'episodes': detail.episodes.length,
            'numbers': detail.episodes
                .map((episode) => episode.number)
                .toList(),
            'first': detail.episodes.first.raw,
            'last': detail.episodes.last.raw,
            'warning': detail.warning,
            'posterFile': posterFile,
            'posterSize': posterSize,
            'displayedIDsMatchNative': true,
            'screenshot': await screenshot(
              'detail-${results.length}-${entry.$1 + 1}',
            ),
          };
          (result['sites']! as List).add(visible);
          await checkpoint();
        }
        await tester.pumpWidget(const SizedBox.shrink());
        await tester.pump(const Duration(milliseconds: 100));
        await tester.pumpWidget(
          RepaintBoundary(
            key: imageKey,
            child: MaterialApp(
              home: PlaybackLaunchScreen(
                drama: drama,
                repository: repository,
                store: store,
                playerBuilder: (detail, index, position, selector) => Scaffold(
                  appBar: AppBar(
                    title: Text('${detail.drama.title} · 仅验证选集，不实播'),
                  ),
                  body: Column(
                    children: [
                      ?selector,
                      Text(
                        '${SourceSite.byId(detail.drama.source).name} · ${detail.episodes.length}集',
                      ),
                      Expanded(
                        child: PlayerEpisodeGrid(
                          episodes: detail.episodes,
                          currentIndex: index,
                          onSelected: (_) {},
                        ),
                      ),
                    ],
                  ),
                ),
              ),
            ),
          ),
        );
        await waitFor(() => entryVisible(drama.id));
        for (final entry in sites.indexed) {
          if (entry.$1 > 0) {
            await tester.tap(
              find.byKey(ValueKey('ordinary-site-${entry.$1 + 1}')),
            );
            await waitFor(() => entryVisible(entry.$2.id));
          }
          final detail = repository.details[entry.$2.id]!;
          final grid = tester.widget<PlayerEpisodeGrid>(
            find.byType(PlayerEpisodeGrid),
          );
          expect(
            grid.episodes.map((episode) => episode.id),
            detail.episodes.map((episode) => episode.id),
          );
        }
        result['entryGateSiteSwitchMatched'] = true;
        result['entryScreenshot'] = await screenshot('entry-${results.length}');
        await checkpoint();
        if (results.length == 2) {
          const seconds = int.fromEnvironment('LIVE_ORDINARY_HOLD_SECONDS');
          for (var elapsed = 0; elapsed < seconds; elapsed++) {
            await tester.pump(const Duration(milliseconds: 50));
            await Future<void>.delayed(const Duration(seconds: 1));
          }
        }
        await tester.pumpWidget(const SizedBox.shrink());
        await tester.pump(const Duration(milliseconds: 200));
      }
      expect(repository.playbackRequests, 0);
      report['playbackRequests'] = repository.playbackRequests;
      report['detailRequests'] = repository.requests;
      report['completed'] = true;
      binding.reportData = report;
    } finally {
      await checkpoint();
      await tester.pumpWidget(const SizedBox.shrink());
      store.dispose();
      PathProviderPlatform.instance = oldPaths;
    }
  }, timeout: const Timeout(Duration(minutes: 12)));
}
