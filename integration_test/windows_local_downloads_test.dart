import 'dart:async';
import 'dart:convert';
import 'dart:io';

import 'package:crypto/crypto.dart';
import 'package:flutter/foundation.dart';
import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:integration_test/integration_test.dart';
import 'package:media_kit/media_kit.dart';
import 'package:media_kit_video/media_kit_video.dart';
import 'package:path_provider_platform_interface/path_provider_platform_interface.dart';
import 'package:shared_preferences/shared_preferences.dart';
import 'package:window_manager/window_manager.dart';

import 'package:duanju_app/app_build.dart';
import 'package:duanju_app/core_bridge.dart';
import 'package:duanju_app/local_store.dart';
import 'package:duanju_app/media_library.dart';
import 'package:duanju_app/models.dart';
import 'package:duanju_app/source_playback_check.dart';
import 'windows_sources_test.dart' show LiveSourcePaths;

void main() {
  final binding = IntegrationTestWidgetsFlutterBinding.ensureInitialized();
  testWidgets(
    'published flat MP4s remain locally playable and deduplicated without raw caches',
    (tester) async {
      expect(Platform.isWindows && allSourcesEnabled, isTrue);
      expect(const bool.fromEnvironment('DISABLE_REMOTE_IMAGES'), isTrue);
      const directory = String.fromEnvironment('LIVE_PROBE_DIRECTORY');
      const expectedCore = String.fromEnvironment('LIVE_CORE_SHA256');
      const expectedCount = int.fromEnvironment('LIVE_EXPECTED_LOCAL_MP4');
      expect(directory, isNotEmpty);
      expect(expectedCore, isNotEmpty);
      final core = File(
        '${File(Platform.resolvedExecutable).parent.path}/duanju_core.dll',
      );
      final coreSHA256 = (await sha256.bind(core.openRead()).first).toString();
      expect(coreSHA256, expectedCore);
      final previousPaths = PathProviderPlatform.instance;
      final previousPlatform = debugDefaultTargetPlatformOverride;
      PathProviderPlatform.instance = LiveSourcePaths(directory);
      debugDefaultTargetPlatformOverride = TargetPlatform.windows;
      SharedPreferences.setMockInitialValues({});
      final store = LocalStore(await SharedPreferences.getInstance());
      await store.disableSourceGate();
      final repository = NativeRepository(background: true)..access = store;
      await repository.initialize();
      final library = MediaLibrary(repository, store);
      final video = ValueNotifier<VideoController?>(null);
      final label = ValueNotifier<String>('检查本地独立 MP4');
      MediaKit.ensureInitialized();
      await windowManager.ensureInitialized();
      await windowManager.setTitle('$appName · 本地 MP4 复验');
      await tester.pumpWidget(
        MaterialApp(
          home: Scaffold(
            body: Column(
              children: [
                ValueListenableBuilder<String>(
                  valueListenable: label,
                  builder: (_, value, _) => Text(value),
                ),
                Expanded(
                  child: ValueListenableBuilder<VideoController?>(
                    valueListenable: video,
                    builder: (_, controller, _) => controller == null
                        ? const SizedBox.shrink()
                        : Video(
                            controller: controller,
                            controls: NoVideoControls,
                          ),
                  ),
                ),
              ],
            ),
          ),
        ),
      );

      Future<T> responsive<T>(Future<T> future) async {
        T? result;
        Object? failure;
        var done = false;
        unawaited(
          future.then<void>(
            (value) {
              result = value;
              done = true;
            },
            onError: (Object error) {
              failure = error;
              done = true;
            },
          ),
        );
        while (!done) {
          await tester.pump();
          await Future<void>.delayed(const Duration(milliseconds: 100));
        }
        if (failure != null) throw failure!;
        return result as T;
      }

      final results = <Map<String, dynamic>>[];
      final report = <String, dynamic>{
        'nativeCoreSHA256': coreSHA256,
        'remoteImagesDisabled': true,
        'syntheticMedia': false,
        'isolatedPreferences': true,
        'files': results,
      };
      final checkpoint = File('$directory/local-replay-report.json');
      try {
        await responsive(library.reload());
        final jobs = {
          for (final job in await responsive(repository.downloads()))
            job.id: job,
        };
        final files = <LocalMediaItem>[];
        for (final item in library.items.where(
          (item) => item.kind == 'export',
        )) {
          if (await File(library.fileFor(item)).exists()) files.add(item);
        }
        expect(files, isNotEmpty);
        if (expectedCount > 0) expect(files, hasLength(expectedCount));
        for (final item in files) {
          final job = jobs[item.jobId]!;
          expect(job.completed && job.published, isTrue);
          label.value =
              '${job.drama.source} 第 ${job.episode.number} 集 · 本地播放及同集去重';
          final plan = await responsive(
            repository.localPlayback(job.drama, job.episode),
          );
          expect(plan, isNotNull);
          expect(plan!.local && plan.decryptionKey.isEmpty, isTrue);
          final file = File(library.fileFor(item));
          expect(File(plan.url).absolute.path, file.absolute.path);
          expect(file.path.contains('Season '), isFalse);
          final check = SourcePlaybackCheck(
            repository,
            onVideo: (controller) => video.value = controller,
          );
          final decoded = await responsive(
            check.verifyPlayback(plan, job.episode.number),
          );
          final added = await responsive(
            repository.enqueueDownloads(DramaDetail(job.drama, [job.episode]), [
              job.episode,
            ]),
          );
          expect(added, 0);
          results.add({
            'source': job.drama.source,
            'episode': job.episode.number,
            'file': file.path,
            'bytes': await file.length(),
            'sha256': (await sha256.bind(file.openRead()).first).toString(),
            'localDecode': decoded,
            'duplicateAdded': added,
          });
          await checkpoint.writeAsString(
            const JsonEncoder.withIndent('  ').convert(report),
          );
        }
        report['completed'] = true;
        await checkpoint.writeAsString(
          const JsonEncoder.withIndent('  ').convert(report),
        );
        binding.reportData = {'windowsLocalDownloads': report};
      } finally {
        library.dispose();
        store.dispose();
        await tester.pumpWidget(const SizedBox.shrink());
        video.dispose();
        label.dispose();
        PathProviderPlatform.instance = previousPaths;
        debugDefaultTargetPlatformOverride = previousPlatform;
      }
    },
    timeout: const Timeout(Duration(minutes: 15)),
  );
}
