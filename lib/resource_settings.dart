import 'dart:async';
import 'dart:io';

import 'package:flutter/services.dart';
import 'package:flutter/widgets.dart';

import 'models.dart';
import 'cache_settings.dart';

class ResourceSettings {
  const ResourceSettings({
    this.logDirectory = '',
    this.logPath = '',
    this.defaultLogDirectory = '',
    this.proxyMode = 'auto',
    this.proxyUrl = '',
    this.catalogConcurrency = 3,
    this.catalogIntervalMs = 250,
    this.downloadConcurrency = 2,
    this.downloadSourceConcurrency = 1,
    this.downloadBySource = false,
    this.warning = '',
    this.systemProxyStatus = '',
    this.cacheLimits = const CacheLimits(),
    this.cacheUsage = const {},
  });
  final String proxyMode, proxyUrl, warning, systemProxyStatus;
  final String logDirectory, logPath, defaultLogDirectory;
  final int catalogConcurrency,
      catalogIntervalMs,
      downloadConcurrency,
      downloadSourceConcurrency;
  final bool downloadBySource;
  final CacheLimits cacheLimits;
  final Map<String, CacheUsage> cacheUsage;

  factory ResourceSettings.fromJson(Map<String, dynamic> data) =>
      ResourceSettings(
        logDirectory: data['logDirectory'] as String? ?? '',
        logPath: data['logPath'] as String? ?? '',
        defaultLogDirectory: data['defaultLogDirectory'] as String? ?? '',
        proxyMode: data['proxyMode'] as String? ?? 'auto',
        proxyUrl: data['proxyUrl'] as String? ?? '',
        catalogConcurrency: intValue(data['catalogConcurrency'] ?? 3),
        catalogIntervalMs: intValue(data['catalogIntervalMs'] ?? 250),
        downloadConcurrency: intValue(data['downloadConcurrency'] ?? 2),
        downloadSourceConcurrency: intValue(
          data['downloadSourceConcurrency'] ?? 1,
        ),
        downloadBySource: data['downloadBySource'] == true,
        warning: data['warning'] as String? ?? '',
        systemProxyStatus: data['systemProxyStatus'] as String? ?? '',
        cacheLimits: CacheLimits.fromJson(data['cacheLimits']),
        cacheUsage: {
          if (data['cacheUsage'] case final Map usage)
            for (final entry in usage.entries)
              if (entry.value is Map)
                entry.key.toString(): CacheUsage.fromJson(entry.value as Map),
        },
      );

  Map<String, dynamic> toJson() => {
    'logDirectory': logDirectory,
    'proxyMode': proxyMode,
    'proxyUrl': proxyUrl,
    'catalogConcurrency': catalogConcurrency,
    'catalogIntervalMs': catalogIntervalMs,
    'downloadConcurrency': downloadConcurrency,
    'downloadSourceConcurrency': downloadSourceConcurrency,
    'downloadBySource': downloadBySource,
    'cacheLimits': cacheLimits.toJson(),
  };
}

class SystemProxyMonitor with WidgetsBindingObserver {
  SystemProxyMonitor._(this.update);
  final Future<void> Function(Map<String, dynamic>) update;
  static SystemProxyMonitor? _current;
  Timer? _timer;
  bool _reading = false;

  static void start(Future<void> Function(Map<String, dynamic>) update) {
    if (!Platform.isAndroid && !Platform.isIOS) return;
    _current?._timer?.cancel();
    if (_current != null) WidgetsBinding.instance.removeObserver(_current!);
    final monitor = SystemProxyMonitor._(update);
    _current = monitor;
    WidgetsBinding.instance.addObserver(monitor);
    monitor._timer = Timer.periodic(
      const Duration(seconds: 30),
      (_) => monitor.refresh(),
    );
    unawaited(monitor.refresh());
  }

  Future<void> refresh() async {
    if (_reading) return;
    _reading = true;
    try {
      final result = await const MethodChannel(
        'duanju/device',
      ).invokeMapMethod<String, dynamic>('systemProxy');
      if (result != null) await update(result);
    } catch (_) {
    } finally {
      _reading = false;
    }
  }

  @override
  void didChangeAppLifecycleState(AppLifecycleState state) {
    if (state == AppLifecycleState.resumed) unawaited(refresh());
  }
}
