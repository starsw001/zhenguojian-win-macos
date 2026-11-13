import 'dart:convert';
import 'dart:io';

import 'package:crypto/crypto.dart';
import 'package:flutter/foundation.dart';
import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:integration_test/integration_test.dart';
import 'package:path/path.dart' as path;
import 'package:path_provider_platform_interface/path_provider_platform_interface.dart';
import 'package:shared_preferences/shared_preferences.dart';
import 'package:url_launcher/url_launcher.dart';
import 'package:window_manager/window_manager.dart';

import 'package:duanju_app/app_build.dart';
import 'package:duanju_app/core_bridge.dart';
import 'package:duanju_app/download_folders.dart';
import 'package:duanju_app/downloads_screen.dart';
import 'package:duanju_app/local_store.dart';
import 'package:duanju_app/models.dart';
import 'package:duanju_app/resource_settings.dart';

import '../test/fixtures.dart';
import 'windows_sources_test.dart' show LiveSourcePaths;

class WindowsFolderRepository extends FixtureRepository {
  WindowsFolderRepository(this.file);
  final String file;
  @override
  bool get supportsDownloads => true;
  @override
  Future<List<DownloadJob>> downloads() async => [
    DownloadJob(
      id: 'folder-probe',
      drama: FixtureRepository.free,
      episode: Episode({'currentEpisode': 1}, 1),
      state: 'completed',
      bytes: 20,
      total: 20,
      progress: 1,
      published: true,
    ),
  ];
  @override
  Future<String?> downloadedFolder(Iterable<DownloadJob> jobs) async =>
      File(file).parent.path;
}

void main() {
  final binding = IntegrationTestWidgetsFlutterBinding.ensureInitialized();
  testWidgets(
    'Windows opens actual downloaded folder and native queue accepts1-10',
    (tester) async {
      expect(Platform.isWindows && allSourcesEnabled, isTrue);
      expect(const bool.fromEnvironment('DISABLE_REMOTE_IMAGES'), isTrue);
      const directory = String.fromEnvironment('LIVE_PROBE_DIRECTORY');
      const existingFile = String.fromEnvironment('LIVE_EXISTING_MP4');
      const expectedCore = String.fromEnvironment('LIVE_CORE_SHA256');
      expect(directory, isNotEmpty);
      expect(existingFile, isNotEmpty);
      final core = File(
        '${File(Platform.resolvedExecutable).parent.path}/duanju_core.dll',
      );
      final coreHash = (await sha256.bind(core.openRead()).first).toString();
      expect(coreHash, expectedCore);
      expect(await File(existingFile).exists(), isTrue);
      final oldPaths = PathProviderPlatform.instance;
      final oldPlatform = debugDefaultTargetPlatformOverride;
      PathProviderPlatform.instance = LiveSourcePaths(directory);
      debugDefaultTargetPlatformOverride = TargetPlatform.windows;
      SharedPreferences.setMockInitialValues({});
      final store = LocalStore(await SharedPreferences.getInstance());
      await store.disableSourceGate();
      final report = <String, dynamic>{
        'nativeCoreSHA256': coreHash,
        'remoteImagesDisabled': true,
        'isolatedPreferences': true,
        'existingVideoReadOnly': true,
        'completed': false,
      };
      try {
        final native = NativeRepository(background: true)..access = store;
        await native.initialize();
        final settings = await native.saveResourceSettings(
          const ResourceSettings(
            downloadConcurrency: 10,
            downloadSourceConcurrency: 1,
          ),
        );
        expect(settings.downloadConcurrency, 10);
        expect(settings.downloadSourceConcurrency, 1);
        final persisted = await native.resourceSettings();
        expect(persisted.downloadConcurrency, 10);
        expect(persisted.downloadSourceConcurrency, 1);
        report['queueSettings'] = persisted.toJson();
        await windowManager.ensureInitialized();
        await windowManager.setTitle('$appName · 下载目录与队列验证');
        final opener = DownloadFolderOpener(
          launch: (uri) async {
            final accepted = await launchUrl(
              uri,
              mode: LaunchMode.externalApplication,
            );
            report['folderUri'] = uri.toString();
            report['windowsShellAccepted'] = accepted;
            return accepted;
          },
        );
        await tester.pumpWidget(
          MaterialApp(
            home: DownloadsScreen(
              repository: WindowsFolderRepository(existingFile),
              store: store,
              folderOpener: opener,
            ),
          ),
        );
        await tester.pumpAndSettle();
        await tester.tap(find.byTooltip('测试短剧 · 合集操作'));
        await tester.pumpAndSettle();
        await tester.tap(
          find.byKey(
            const ValueKey('download-collection-open-folder-hongguo:100'),
          ),
        );
        for (
          var i = 0;
          i < 100 && report['windowsShellAccepted'] != true;
          i++
        ) {
          await tester.pump();
          await Future<void>.delayed(const Duration(milliseconds: 50));
        }
        expect(report['windowsShellAccepted'], isTrue);
        final opened = Uri.parse(report['folderUri'] as String)
            .toFilePath(windows: true);
        expect(
          path.normalize(opened),
          path.normalize(File(existingFile).parent.path),
        );
        expect(await File(existingFile).exists(), isTrue);
        report['completed'] = true;
      } finally {
        await tester.pumpWidget(const SizedBox.shrink());
        await File('$directory/download-controls-report.json')
            .writeAsString(const JsonEncoder.withIndent('  ').convert(report));
        store.dispose();
        PathProviderPlatform.instance = oldPaths;
        debugDefaultTargetPlatformOverride = oldPlatform;
        binding.reportData = report;
      }
    },
    timeout: const Timeout(Duration(minutes: 3)),
  );
}
