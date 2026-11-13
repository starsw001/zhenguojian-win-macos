import 'dart:convert';
import 'dart:io';

import 'package:crypto/crypto.dart';
import 'package:flutter/foundation.dart';
import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:integration_test/integration_test.dart';
import 'package:media_kit/media_kit.dart';
import 'package:path_provider_platform_interface/path_provider_platform_interface.dart';
import 'package:shared_preferences/shared_preferences.dart';
import 'package:window_manager/window_manager.dart';

import 'package:duanju_app/app_build.dart';
import 'package:duanju_app/core_bridge.dart';
import 'package:duanju_app/local_store.dart';
import 'package:duanju_app/models.dart';
import 'package:duanju_app/resource_settings.dart';
import 'package:duanju_app/sources_screen.dart';

class LiveSourcePaths extends PathProviderPlatform {
  LiveSourcePaths(this.directory);
  final String directory;
  @override
  Future<String?> getApplicationSupportPath() async => directory;
  @override
  Future<String?> getApplicationDocumentsPath() async => directory;
  @override
  Future<String?> getTemporaryPath() async => directory;
  @override
  Future<String?> getApplicationCachePath() async => directory;
  @override
  Future<String?> getDownloadsPath() async => directory;
}

void main() {
  final binding = IntegrationTestWidgetsFlutterBinding.ensureInitialized();
  testWidgets(
    'Windows real source management, search, episodes and remote decode',
    (tester) async {
      expect(Platform.isWindows, isTrue);
      expect(allSourcesEnabled, isTrue);
      expect(const bool.fromEnvironment('DISABLE_REMOTE_IMAGES'), isFalse);
      final coreFile = File(
        '${File(Platform.resolvedExecutable).parent.path}/duanju_core.dll',
      );
      final coreSHA256 = (await sha256.bind(coreFile.openRead()).first)
          .toString();
      const expectedCore = String.fromEnvironment('LIVE_CORE_SHA256');
      if (expectedCore.isNotEmpty) {
        expect(
          coreSHA256,
          expectedCore,
          reason:
              'Native DLL is stale; build the native core before the Windows integration run',
        );
      }

      final originalPlatform = debugDefaultTargetPlatformOverride;
      if (kDebugMode) {
        debugDefaultTargetPlatformOverride = TargetPlatform.windows;
      }
      final originalPaths = PathProviderPlatform.instance;
      const configuredDirectory = String.fromEnvironment(
        'LIVE_PROBE_DIRECTORY',
      );
      final directory = configuredDirectory.isEmpty
          ? await Directory.systemTemp.createTemp('zhenguoshang-real-sources-')
          : Directory(configuredDirectory);
      await directory.create(recursive: true);
      PathProviderPlatform.instance = LiveSourcePaths(directory.absolute.path);
      SharedPreferences.setMockInitialValues({});
      final store = LocalStore(await SharedPreferences.getInstance());
      expect(store.sources.length, 23);
      expect(store.sourceGateOff, isTrue);
      expect(store.preferences.getKeys(), isEmpty);
      final repository = NativeRepository(
        background: true,
        dataDirectory: directory,
      )..access = store;
      await repository.initialize();
      const mode = String.fromEnvironment(
        'LIVE_PROXY_MODE',
        defaultValue: 'auto',
      );
      if (mode == 'direct') {
        await repository.saveResourceSettings(
          const ResourceSettings(proxyMode: 'direct'),
        );
      }
      await windowManager.ensureInitialized();
      await windowManager.setTitle('$appName · 真实站源逐项检查');
      MediaKit.ensureInitialized();
      tester.view.physicalSize = const Size(1280, 900);
      tester.view.devicePixelRatio = 1;
      final results = <Map<String, dynamic>>[];
      final selected = const String.fromEnvironment(
        'LIVE_SOURCES',
      ).split(',').where((id) => id.isNotEmpty).toSet();
      final sources = SourceSite.values
          .where((source) => selected.isEmpty || selected.contains(source.id))
          .toList();
      final checkpoint = File('${directory.path}/real-source-report.json');
      if (await checkpoint.exists()) {
        final previous = jsonDecode(await checkpoint.readAsString()) as Map;
        if (previous['networkMode'] == mode &&
            previous['syntheticMedia'] == false) {
          for (final row in previous['sources'] as List? ?? []) {
            final result = Map<String, dynamic>.from(row as Map);
            if (sources.any((source) => source.id == result['source'])) {
              results.add(result);
            }
          }
        }
      }
      final report = <String, dynamic>{
        'edition': appName,
        'allSources': allSourcesEnabled,
        'networkMode': mode,
        'isolatedPreferences': true,
        'nativeCoreSHA256': coreSHA256,
        'nativeDirectory': directory.absolute.path,
        'remoteImagesDisabled': false,
        'firstRunDefaultSources': store.sources.map((site) => site.id).toList(),
        'legacyAppDataMigrationSkipped': true,
        'syntheticMedia': false,
        'sources': results,
      };
      Future<void> save() async {
        await File(
          '${directory.path}/real-source-report.json',
        ).writeAsString(const JsonEncoder.withIndent('  ').convert(report));
      }

      try {
        await tester.pumpWidget(
          MaterialApp(
            home: SourcesScreen(repository: repository, store: store),
          ),
        );
        await tester.pump(const Duration(seconds: 1));
        for (final source in sources) {
          if (results.any((result) => result['source'] == source.id)) {
            debugPrint(
              'REAL_SOURCE_REUSED ${source.id} (completed result retained)',
            );
            continue;
          }
          debugPrint(
            'REAL_SOURCE_START ${source.id} ${source.name} MODE=$mode',
          );
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
          await tester.pump(const Duration(milliseconds: 500));
          if (source.groupId == 'huangguo') {
            final group = find.byKey(
              const PageStorageKey('source-group-huangguo'),
            );
            if (find.byKey(ValueKey('check-${source.id}')).evaluate().isEmpty) {
              await tester.scrollUntilVisible(
                group,
                500,
                maxScrolls: 100,
                scrollable: find
                    .descendant(
                      of: find.byKey(const ValueKey('source-manager-list')),
                      matching: find.byType(Scrollable),
                    )
                    .first,
              );
              await tester.tap(group);
              await tester.pump(const Duration(milliseconds: 500));
            }
          }
          final button = find.byKey(ValueKey('check-${source.id}'));
          await tester.scrollUntilVisible(
            button,
            500,
            maxScrolls: 100,
            scrollable: find
                .descendant(
                  of: find.byKey(const ValueKey('source-manager-list')),
                  matching: find.byType(Scrollable),
                )
                .first,
          );
          final previous = (await repository.sourceStatus(
            source.id,
          )).health?.checkedAt;
          final activate = tester.widget<OutlinedButton>(button).onPressed;
          expect(activate, isNotNull, reason: '${source.id} is not enabled');
          activate!();
          await tester.pump();
          final timer = Stopwatch()..start();
          Map<String, dynamic>? result;
          while (timer.elapsed < const Duration(minutes: 5)) {
            await Future<void>.delayed(const Duration(milliseconds: 100));
            await tester.pump(const Duration(milliseconds: 250));
            final health = (await repository.sourceStatus(source.id)).health;
            if (health != null &&
                health.checkedAt != previous &&
                health.steps.length == 4) {
              result = {
                'source': source.id,
                'name': source.name,
                'health': {
                  'checkedAt': health.checkedAt?.toUtc().toIso8601String(),
                  'state': health.state,
                  'networkMode': health.networkMode,
                  'sample': health.sample,
                  'steps': [
                    for (final step in health.steps)
                      {
                        'name': step.name,
                        'state': step.state,
                        'message': step.message,
                        'elapsedMs': step.elapsedMs,
                      },
                  ],
                },
              };
              break;
            }
          }
          expect(
            result,
            isNotNull,
            reason: '${source.id} did not persist a terminal result',
          );
          results.add(result!);
          await save();
          debugPrint('REAL_SOURCE_RESULT ${jsonEncode(result)}');
          await tester.pump(const Duration(milliseconds: 500));
        }
        expect(results.length, sources.length);
        expect(
          results.map((result) => result['source']).toSet().length,
          sources.length,
        );
        expect(tester.takeException(), isNull);
        report['completed'] = true;
        await save();
        binding.reportData = {'windowsRealSources': report};
        debugPrint(
          'REAL_SOURCE_MATRIX_COMPLETE ${results.length} (completion is not a claim that all providers passed)',
        );
      } finally {
        await tester.pumpWidget(const SizedBox.shrink());
        await repository.cancelPlayback();
        store.dispose();
        tester.view.reset();
        PathProviderPlatform.instance = originalPaths;
        if (kDebugMode) {
          debugDefaultTargetPlatformOverride = originalPlatform;
        }
      }
    },
    timeout: const Timeout(Duration(minutes: 50)),
  );
}
