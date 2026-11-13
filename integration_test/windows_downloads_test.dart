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
import 'package:duanju_app/resource_settings.dart';
import 'package:duanju_app/source_playback_check.dart';
import 'windows_sources_test.dart' show LiveSourcePaths;

void main() {
  final binding = IntegrationTestWidgetsFlutterBinding.ensureInitialized();
  testWidgets(
    'real per-source episode download, independent MP4 and local playback',
    (tester) async {
      expect(Platform.isWindows && allSourcesEnabled, isTrue);
      expect(const bool.fromEnvironment('DISABLE_REMOTE_IMAGES'), isTrue);
      final coreFile = File(
        '${File(Platform.resolvedExecutable).parent.path}/duanju_core.dll',
      );
      final coreSHA256 = (await sha256.bind(coreFile.openRead()).first)
          .toString();
      const expectedCore = String.fromEnvironment('LIVE_CORE_SHA256');
      if (expectedCore.isNotEmpty)
        expect(
          coreSHA256,
          expectedCore,
          reason:
              'Native DLL is stale; build the native core before the Windows integration run',
        );

      const waitMinutes = int.fromEnvironment(
        'LIVE_DOWNLOAD_WAIT_MINUTES',
        defaultValue: 25,
      );
      const directory = String.fromEnvironment('LIVE_PROBE_DIRECTORY');
      const matrixPath = String.fromEnvironment('LIVE_MATRIX_FILE');
      expect(directory, isNotEmpty);
      expect(matrixPath, isNotEmpty);
      final paths = PathProviderPlatform.instance;
      final platform = debugDefaultTargetPlatformOverride;
      PathProviderPlatform.instance = LiveSourcePaths(directory);
      debugDefaultTargetPlatformOverride = TargetPlatform.windows;
      SharedPreferences.setMockInitialValues({});
      final store = LocalStore(await SharedPreferences.getInstance());
      await store.disableSourceGate();
      final repository = NativeRepository(background: true)..access = store;
      await repository.initialize();
      final library = MediaLibrary(repository, store);
      final video = ValueNotifier<VideoController?>(null);
      final progress = ValueNotifier<String>('准备真实分集下载');
      await windowManager.ensureInitialized();
      await windowManager.setTitle('$appName · 分集下载验证');
      MediaKit.ensureInitialized();
      await tester.pumpWidget(
        MaterialApp(
          home: Scaffold(
            body: Column(
              children: [
                ValueListenableBuilder<String>(
                  valueListenable: progress,
                  builder: (_, value, _) => Text(value),
                ),
                Expanded(
                  child: ValueListenableBuilder<VideoController?>(
                    valueListenable: video,
                    builder: (_, value, _) => value == null
                        ? const SizedBox.shrink()
                        : Video(controller: value, controls: NoVideoControls),
                  ),
                ),
              ],
            ),
          ),
        ),
      );
      Future<T> responsive<T>(Future<T> future) async {
        T? value;
        Object? error;
        var done = false;
        unawaited(
          future.then<void>(
            (result) {
              value = result;
              done = true;
            },
            onError: (Object failure) {
              error = failure;
              done = true;
            },
          ),
        );
        while (!done) {
          await tester.pump();
          await Future<void>.delayed(const Duration(milliseconds: 100));
        }
        if (error != null) throw error!;
        return value as T;
      }

      final matrix = jsonDecode(await File(matrixPath).readAsString()) as Map;
      final scenarios = (matrix['sources'] as List).cast<Map>();
      final checkpoint = File('$directory/real-download-report.json');
      final results = <Map<String, dynamic>>[];
      final history = <dynamic>[];
      if (await checkpoint.exists()) {
        final previous = jsonDecode(await checkpoint.readAsString()) as Map;
        history.addAll(previous['history'] as List? ?? []);
        for (final row in previous['sources'] as List? ?? []) {
          final result = Map<String, dynamic>.from(row as Map);
          if (result['state'] == 'ok' &&
              await File(result['file'] as String).exists()) {
            results.add(result);
          } else {
            history.add(result);
          }
        }
      }
      final report = <String, dynamic>{
        'independentEpisodes': true,
        'mergeRequested': false,
        'downloadDirectory': await repository.downloadDirectory(),
        'isolatedPreferences': true,
        'nativeCoreSHA256': coreSHA256,
        'sources': results,
        'history': history,
      };
      Future<void> save() async {
        await checkpoint.writeAsString(
          const JsonEncoder.withIndent('  ').convert(report),
        );
      }

      Future<void> finish(Map<String, dynamic> result, Stopwatch timer) async {
        result['elapsedMs'] = timer.elapsedMilliseconds;
        results.add(result);
        await save();
        debugPrint('REAL_DOWNLOAD_RESULT ${jsonEncode(result)}');
      }

      try {
        if (const bool.fromEnvironment('LIVE_MIGRATE_EXISTING')) {
          final existing = await responsive(repository.downloads());
          progress.value = '保留视频内容，迁移已有 MP4 到按集平铺目录并提交缓存清理';
          final missing = <Map<String, dynamic>>[];
          var migrated = 0;
          for (final job in existing) {
            try {
              await responsive(library.exportJobs([job]));
              migrated++;
            } catch (error) {
              missing.add({'jobId': job.id, 'reason': '$error'});
            }
          }
          report['migratedExistingJobs'] = migrated;
          report['existingJobsMissingFiles'] = missing;
          await save();
        }
        for (final mode
            in scenarios
                .map((row) => row['downloadMode'] as String? ?? 'auto')
                .toSet()) {
          await responsive(
            repository.saveResourceSettings(
              ResourceSettings(proxyMode: mode, downloadConcurrency: 4),
            ),
          );
          final pending = <String, (Map<String, dynamic>, Stopwatch)>{};
          for (final scenario in scenarios.where(
            (row) => (row['downloadMode'] as String? ?? 'auto') == mode,
          )) {
            final source = scenario['source'] as String;
            final task = scenario['task'] as String? ?? source;
            if (results.any((row) => (row['task'] ?? row['source']) == task)) {
              continue;
            }
            final result = <String, dynamic>{
              'source': source,
              'task': task,
              'networkMode': mode,
              'state': 'failed',
            };
            final timer = Stopwatch()..start();
            try {
              progress.value = '$source · 读取已实播样本并加入分集队列';
              var page = await responsive(repository.cached(source));
              if (page.items.isEmpty) {
                page = await responsive(
                  repository.catalog(source, force: true),
                );
              }
              final sample = (scenario['health'] as Map)['sample'] as String;
              final drama = page.items.firstWhere(
                (item) => item.title == sample,
                orElse: () =>
                    page.items.firstWhere((item) => item.vipStatus != true),
              );
              final detail = await responsive(repository.detail(drama));
              final requested = intValue(scenario['episode']);
              final episode = detail.episodes.firstWhere(
                (item) => requested == 0 ? !item.vip : item.number == requested,
              );
              if (episode.vip)
                throw AppFailure('第 ${episode.number} 集需要源站付费授权，未发送媒体下载请求');
              result['dramaId'] = drama.id;
              result['episode'] = episode.number;
              await responsive(
                repository.enqueueDownloads(detail, [episode], quality: 480),
              );
              final job = (await responsive(repository.downloads())).firstWhere(
                (item) =>
                    item.drama.id == drama.id &&
                    item.episode.number == episode.number,
              );
              result['nativeJobId'] = job.id;
              if (job.state == 'paused' || job.state == 'failed') {
                await responsive(
                  repository.controlDownloads('resume', id: job.id),
                );
              }
              pending[job.id] = (result, timer);
            } catch (error) {
              result['reason'] = '$error';
              await finish(result, timer);
            }
          }
          final deadline = Stopwatch()..start();
          var heartbeat = -1;
          while (pending.isNotEmpty) {
            final jobs = await responsive(repository.downloads());
            for (final id in pending.keys.toList()) {
              final job = jobs.where((item) => item.id == id).firstOrNull;
              final (result, timer) = pending[id]!;
              if (job == null) {
                result['reason'] = '原生队列未返回已加入的分集';
              } else if (job.completed) {
                try {
                  progress.value = '${result['source']} · 单集 MP4 封装与本地解码，不合并多集';
                  await responsive(library.exportJobs([job]));
                  final item = library.items.firstWhere(
                    (item) => item.jobId == job.id && item.kind == 'export',
                  );
                  final file = File(library.fileFor(item));
                  expect(file.path.toLowerCase(), endsWith('.mp4'));
                  expect(file.path.contains('Season 01'), isFalse);
                  expect(
                    await File(
                      file.path.replaceAll(RegExp(r'\.mp4$'), '.nfo'),
                    ).exists(),
                    isFalse,
                  );
                  expect(item.episodes, [job.episode.number]);
                  final published = (await responsive(
                    repository.downloads(),
                  )).firstWhere((entry) => entry.id == job.id);
                  expect(published.published, isTrue);
                  expect(
                    await Directory(
                      '${report['downloadDirectory']}/${job.id}',
                    ).exists(),
                    isFalse,
                  );
                  final duplicate = await responsive(
                    repository.enqueueDownloads(
                      DramaDetail(job.drama, [job.episode]),
                      [job.episode],
                    ),
                  );
                  expect(duplicate, 0);
                  final check = SourcePlaybackCheck(
                    repository,
                    onVideo: (controller) => video.value = controller,
                  );
                  final decoded = await responsive(
                    check.verifyPlayback(
                      PlaybackPlan(url: file.path, local: true),
                      job.episode.number,
                    ),
                  );
                  result.addAll({
                    'state': 'ok',
                    'file': file.path,
                    'bytes': await file.length(),
                    'sha256': (await sha256.bind(file.openRead()).first)
                        .toString(),
                    'durationSeconds': item.duration,
                    'localDecode': decoded,
                    'duplicateAdded': duplicate,
                    'rawCacheRemoved': true,
                  });
                } catch (error) {
                  result['reason'] = '$error';
                }
              } else if (job.state == 'failed' || job.state == 'paused') {
                result['reason'] = job.error.isNotEmpty
                    ? job.error
                    : '分集尚未完成，保留原文件和续传记录';
                result['downloadedBytes'] = job.bytes;
              } else if (deadline.elapsed >=
                  const Duration(minutes: waitMinutes)) {
                await responsive(
                  repository.controlDownloads('pause', id: job.id),
                );
                result['reason'] =
                    '本批实测 $waitMinutes 分钟内未下载完成，已暂停并保留续传记录，不误报 MP4 成功';
                result['downloadedBytes'] = job.bytes;
                result['progress'] = job.progress;
              } else {
                continue;
              }
              pending.remove(id);
              await finish(result, timer);
            }
            final tick = deadline.elapsed.inSeconds ~/ 30;
            if (tick != heartbeat && pending.isNotEmpty) {
              heartbeat = tick;
              final summary = [
                for (final job in jobs.where(
                  (job) => pending.containsKey(job.id),
                ))
                  '${job.drama.source} ${job.state} ${(job.progress * 100).toStringAsFixed(1)}% ${job.bytes} bytes',
              ].join(' · ');
              progress.value = summary;
              debugPrint('REAL_DOWNLOAD_PROGRESS mode=$mode $summary');
              await save();
            }
            await tester.pump();
            await Future<void>.delayed(const Duration(milliseconds: 500));
          }
        }
        report['completed'] = true;
        await save();
        binding.reportData = {'windowsRealDownloads': report};
        debugPrint(
          'REAL_DOWNLOAD_MATRIX_COMPLETE ${results.length} (failures remain explicit)',
        );
      } finally {
        library.dispose();
        store.dispose();
        await tester.pumpWidget(const SizedBox.shrink());
        video.dispose();
        progress.dispose();
        PathProviderPlatform.instance = paths;
        debugDefaultTargetPlatformOverride = platform;
      }
    },
    timeout: const Timeout(Duration(hours: 8)),
  );
}
