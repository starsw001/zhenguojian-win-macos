import 'dart:async';
import 'dart:convert';
import 'dart:io';
import 'dart:ui' as ui;

import 'package:crypto/crypto.dart';
import 'package:path/path.dart' as path;
import 'package:shared_preferences/shared_preferences.dart';

import 'app_build.dart';
import 'app_version.g.dart';
import 'core_bridge.dart';
import 'local_store.dart';
import 'models.dart';
import 'source_playback_check.dart';

({Directory directory, SourceSite source}) parseFirstStartCheckArguments(
  List<String> arguments,
) {
  if (arguments.length != 3 || arguments.first != '--first-start-check') {
    throw ArgumentError(
      '用法：--first-start-check EMPTY_ABSOLUTE_DIRECTORY SOURCE',
    );
  }
  if (!path.isAbsolute(arguments[1])) throw ArgumentError('隔离目录必须使用绝对路径');
  final directory = Directory(path.normalize(arguments[1]));
  final type = FileSystemEntity.typeSync(directory.path, followLinks: false);
  if (type != FileSystemEntityType.notFound &&
      (type != FileSystemEntityType.directory ||
          directory.listSync(followLinks: false).isNotEmpty)) {
    throw ArgumentError('只允许使用新的空目录，不读取或覆盖已有配置');
  }
  final sources = SourceSite.values.where((site) => site.id == arguments[2]);
  if (sources.isEmpty) throw ArgumentError('当前版本不包含指定站源');
  return (directory: directory, source: sources.single);
}

String _safeError(Object error) => '$error'.replaceAllMapped(
  RegExp(r'https?://[^\s<>]+'),
  (match) => Uri.tryParse(match[0]!)?.host ?? 'HOST',
);

Future<void> runFirstStartCheck(List<String> arguments) async {
  late final ({Directory directory, SourceSite source}) request;
  try {
    request = parseFirstStartCheckArguments(arguments);
    await request.directory.create(recursive: true);
  } catch (error) {
    stderr.writeln(_safeError(error));
    exit(2);
  }
  final directory = request.directory;
  final reportFile = File(path.join(directory.path, 'first-start-result.json'));
  final report = <String, dynamic>{
    'edition': appName,
    'slug': appSlug,
    'version': appVersion,
    'source': request.source.id,
    'name': request.source.name,
    'startedAt': DateTime.now().toUtc().toIso8601String(),
    'completed': false,
    'isolatedPreferences': true,
    'nativeDirectoryInitiallyEmpty': true,
    'legacyAppDataMigrationSkipped': true,
    'syntheticMedia': false,
    'remoteImagesDisabled': const bool.fromEnvironment('DISABLE_REMOTE_IMAGES'),
    'currentStage': '首次启动',
  };
  Future<void> writes = Future.value();
  void checkpoint() {
    final content = const JsonEncoder.withIndent('  ').convert(report);
    writes = writes.then((_) async {
      await reportFile.writeAsString(content, flush: true);
    });
  }

  LocalStore? store;
  NativeRepository? repository;
  var code = 1;
  try {
    SharedPreferences.setMockInitialValues({});
    final preferences = await SharedPreferences.getInstance();
    if (preferences.getKeys().isNotEmpty) throw StateError('隔离偏好不是空配置');
    store = LocalStore(preferences);
    final defaults = store.sources.map((site) => site.id).toList();
    if (defaults.length != SourceSite.values.length ||
        (allSourcesEnabled && defaults.length != 23) ||
        !SourceSite.values.every((site) => store!.allowsSource(site.id))) {
      throw StateError('首次启动未开放本版本全部站源');
    }
    report.addAll({
      'defaultVisibleSources': defaults,
      'defaultVisibleSourceCount': defaults.length,
      'sourceGateEnabled': store.sourceGateEnabled,
      'sourceGateOff': store.sourceGateOff,
      'sourceGateConfigured': store.sourceGateConfigured,
      'preferencesWrittenDuringInitialization': preferences
          .getKeys()
          .isNotEmpty,
    });
    final core = File(
      path.join(
        File(Platform.resolvedExecutable).parent.path,
        'duanju_core.dll',
      ),
    );
    report['nativeCoreSHA256'] = (await sha256.bind(core.openRead()).first)
        .toString();
    checkpoint();
    repository = NativeRepository(
      dataDirectory: Directory(path.join(directory.path, 'native-data')),
    )..access = store;
    await repository.initialize();
    final initialCatalogCounts = <String, int>{};
    for (final source in SourceSite.values) {
      initialCatalogCounts[source.id] = (await repository.sourceStatus(
        source.id,
      )).count;
    }
    report['initialCatalogEntryCounts'] = initialCatalogCounts;
    if (initialCatalogCounts.values.any((count) => count != 0)) {
      throw StateError('隔离站源目录包含旧目录缓存，不能作为首次启动结果');
    }
    checkpoint();
    final checker = SourcePlaybackCheck(
      repository,
      renderVideo: false,
      onStage: (stage) {
        report['currentStage'] = stage;
        checkpoint();
      },
    );
    final result = await checker
        .run(request.source)
        .timeout(const Duration(minutes: 6));
    report['result'] = result;
    report['flowPassed'] = (result['health'] as Map)['state'] == 'decoded';
    final raw = result['sampleDrama'];
    if (raw is Map) {
      report['currentStage'] = '真实海报';
      checkpoint();
      try {
        final drama = Drama.fromJson(Map<String, dynamic>.from(raw));
        final file = await repository
            .cover(drama)
            .timeout(const Duration(seconds: 45));
        final bytes = await File(file).readAsBytes();
        final codec = await ui.instantiateImageCodec(bytes, targetWidth: 96);
        try {
          final frame = await codec.getNextFrame();
          try {
            report['poster'] = {
              'ok': frame.image.width > 0 && frame.image.height > 0,
              'width': frame.image.width,
              'height': frame.image.height,
              'bytes': bytes.length,
            };
          } finally {
            frame.image.dispose();
          }
        } finally {
          codec.dispose();
        }
      } catch (error) {
        report['poster'] = {'ok': false, 'error': _safeError(error)};
      }
    } else {
      report['poster'] = {'ok': false, 'error': '目录未返回可验证海报的样本'};
    }
    report['preferencesStillEmpty'] = preferences.getKeys().isEmpty;
    if (report['flowPassed'] == true &&
        (report['poster'] as Map)['ok'] == true &&
        report['preferencesStillEmpty'] == true) {
      code = 0;
    }
  } catch (error) {
    report['error'] = _safeError(error);
  } finally {
    try {
      await repository?.cancelPlayback().timeout(const Duration(seconds: 5));
    } catch (_) {}
    store?.dispose();
    report.addAll({
      'completed': true,
      'completedAt': DateTime.now().toUtc().toIso8601String(),
      'exitCode': code,
    });
    checkpoint();
    await writes;
  }
  exit(code);
}
