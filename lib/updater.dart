import 'dart:async';
import 'dart:io';

import 'package:flutter/material.dart';
import 'package:shared_preferences/shared_preferences.dart';
import 'package:url_launcher/url_launcher.dart';

import 'app_layout.dart';
import 'update_activity.dart';
import 'core_bridge.dart';

class UpdateInfo {
  const UpdateInfo({
    required this.available,
    required this.current,
    required this.version,
    required this.notes,
    required this.pageUrl,
    required this.assetUrl,
    this.error,
    this.noRelease = false,
    this.ticket = '',
  });

  final bool available;
  final String current;
  final String version;
  final String notes;
  final String pageUrl;
  final String assetUrl;
  final String? error;
  final bool noRelease;
  final String ticket;

  static UpdateInfo fromJson(Map<String, dynamic> json) => UpdateInfo(
    available: json['available'] == true,
    noRelease: json['noRelease'] == true,
    current: '${json['current'] ?? appVersion}',
    version: '${json['version'] ?? ''}',
    notes: '${json['notes'] ?? ''}',
    pageUrl: '${json['pageUrl'] ?? ''}',
    ticket: '${json['ticket'] ?? ''}',
    assetUrl: '${json['assetUrl'] ?? ''}',
    error: json['error'] == null ? null : '${json['error']}',
  );
}

String get _edition => allSourcesEnabled ? 'zhenguoshang' : 'hongguoshang';

const _checkInterval = Duration(hours: 6);
String get _lastCheckKey => 'signedUpdateCheck.$_edition';

Future<UpdateInfo?> checkForUpdate(AppRepository repository) async {
  try {
    final raw = await repository.checkForUpdate(appVersion, _edition);
    final info = UpdateInfo.fromJson(raw);
    return info;
  } on AppFailure {
    rethrow;
  } catch (_) {
    return null;
  }
}

/// 启动后静默检查：有新版且未被跳过时弹窗。
Future<void> maybeAutoCheckUpdate(
  BuildContext context,
  AppRepository repository,
) async {
  if (!context.mounted) return;
  try {
    final prefs = await SharedPreferences.getInstance();
    final last = DateTime.tryParse(prefs.getString(_lastCheckKey) ?? '');
    if (last != null && DateTime.now().difference(last) < _checkInterval) {
      return;
    }
    final info = await checkForUpdate(repository);
    if (info == null || info.error != null) return;
    await prefs.setString(_lastCheckKey, DateTime.now().toIso8601String());
    if (!info.available) return;
    while (true) {
      if (!context.mounted) return;
      final pageInFront = ModalRoute.of(context)?.isCurrent != false;
      if (pageInFront &&
          !UpdateActivity.playing &&
          !await repository.updateBusy()) {
        break;
      }
      await Future<void>.delayed(const Duration(seconds: 20));
    }
    if (!context.mounted) return;
    await showUpdateDialog(context, repository, info);
  } catch (_) {}
}

Future<void> showUpdateDialog(
  BuildContext context,
  AppRepository repository,
  UpdateInfo info, {
  bool manual = false,
}) async {
  final action = await showDialog<String>(
    context: context,
    builder: (context) {
      final width = MediaQuery.sizeOf(context).width;
      return AlertDialog(
        title: Text(
          info.error != null
              ? '更新检查未完成'
              : info.noRelease
              ? '暂无正式发布'
              : info.available
              ? '发现新版本 ${info.version}'
              : '已是最新版本',
        ),
        content: SizedBox(
          width: width < 520 ? width * 0.8 : 460,
          child: SingleChildScrollView(
            child: Column(
              crossAxisAlignment: CrossAxisAlignment.start,
              mainAxisSize: MainAxisSize.min,
              children: [
                Text(
                  '当前版本 ${info.current}',
                  style: const TextStyle(fontSize: 12, color: Colors.grey),
                ),
                const SizedBox(height: 12),
                if (info.error != null)
                  Text(info.error!, style: const TextStyle(fontSize: 13))
                else if (info.noRelease)
                  const Text('更新仓库尚未发布可识别的版本，当前软件可以继续使用。')
                else if (!info.available)
                  Text(
                    '当前已是最新版本（${info.current}），无需更新。',
                    style: const TextStyle(fontSize: 13, height: 1.5),
                  )
                else if (info.notes.trim().isNotEmpty)
                  Text(
                    info.notes.trim(),
                    style: const TextStyle(fontSize: 13, height: 1.5),
                  ),
              ],
            ),
          ),
        ),
        actions: [
          if (manual && info.error != null)
            TextButton(
              onPressed: () => Navigator.pop(context, 'release-page'),
              child: const Text('打开发布页'),
            ),
          if (info.available) ...[
            TextButton(
              onPressed: () => Navigator.pop(context, 'later'),
              child: const Text('稍后'),
            ),
            if (!Platform.isWindows)
              TextButton(
                onPressed: () => Navigator.pop(context, 'github'),
                child: const Text('去 GitHub 下载'),
              ),
            if (Platform.isWindows && info.assetUrl.isNotEmpty)
              FilledButton(
                onPressed: () => Navigator.pop(context, 'update'),
                child: const Text('立即更新'),
              ),
          ] else
            FilledButton(
              onPressed: () => Navigator.pop(context, 'close'),
              child: const Text('知道了'),
            ),
        ],
      );
    },
  );
  if (!context.mounted) return;
  switch (action) {
    case 'release-page':
      final messenger = ScaffoldMessenger.maybeOf(context);
      const address =
          'https://github.com/starsw001/hongguo-win-public-package/releases';
      try {
        if (await launchUrl(
          Uri.parse(address),
          mode: LaunchMode.externalApplication,
        )) {
          return;
        }
      } catch (_) {}
      messenger?.showSnackBar(const SnackBar(content: SelectableText(address)));
      return;
    case 'github':
      await launchUrl(
        Uri.parse(info.pageUrl),
        mode: LaunchMode.externalApplication,
      );
    case 'update':
      await _runDownload(context, repository, info);
    case 'later':
      return;
  }
}

Future<void> _runDownload(
  BuildContext context,
  AppRepository repository,
  UpdateInfo info,
) async {
  final navigator = Navigator.of(context, rootNavigator: true);
  final progress = ValueNotifier<double?>(null);
  final statusText = ValueNotifier<String>('正在下载 ${info.version}…');
  unawaited(
    showDialog<void>(
      context: context,
      barrierDismissible: false,
      builder: (context) => PopScope(
        canPop: false,
        child: AlertDialog(
          title: const Text('正在更新'),
          content: ValueListenableBuilder<double?>(
            valueListenable: progress,
            builder: (context, value, _) => Column(
              crossAxisAlignment: CrossAxisAlignment.start,
              mainAxisSize: MainAxisSize.min,
              children: [
                LinearProgressIndicator(value: value),
                const SizedBox(height: 12),
                ValueListenableBuilder<String>(
                  valueListenable: statusText,
                  builder: (context, text, _) =>
                      Text(text, style: const TextStyle(fontSize: 12)),
                ),
              ],
            ),
          ),
        ),
      ),
    ),
  );

  var closed = false;
  void closeDialog() {
    if (closed) return;
    closed = true;
    navigator.pop();
  }

  try {
    if (UpdateActivity.playing || await repository.updateBusy()) {
      throw AppFailure('播放或下载中，暂不更新。');
    }
    await repository.downloadUpdate(appVersion, _edition, info.ticket);
    while (true) {
      await Future<void>.delayed(const Duration(milliseconds: 500));
      final status = await repository.updateStatus();
      final state = '${status['state']}';
      if (state == 'downloading') {
        final received = (status['received'] as num?)?.toDouble() ?? 0;
        final total = (status['total'] as num?)?.toDouble() ?? 0;
        progress.value = total > 0 ? (received / total).clamp(0.0, 1.0) : null;
        statusText.value = total > 0
            ? '正在下载 ${_size(received)} / ${_size(total)}'
            : '正在下载…';
        continue;
      }
      if (state == 'extracting') {
        progress.value = null;
        statusText.value = '正在解压安装包…';
        continue;
      }
      if (state == 'ready') {
        progress.value = 1;
        statusText.value = '更新包已验证完成。';
        break;
      }
      throw AppFailure('${status['message'] ?? '更新失败，请稍后重试'}');
    }
    if (UpdateActivity.playing) throw AppFailure('播放中，暂不更新。');
    closeDialog();
    await Future<void>.delayed(const Duration(milliseconds: 150));
    if (!context.mounted) return;
    final startUpdate = await showDialog<bool>(
      context: context,
      barrierDismissible: false,
      builder: (context) => AlertDialog(
        title: const Text('更新包已准备好'),
        content: const Text(
          '点击“开始更新”后，软件会先完整退出，再把新版覆盖到原安装目录并重新打开。'
          '已下载剧集、设置、日志和其他用户文件都会保留。',
        ),
        actions: [
          TextButton(
            onPressed: () => Navigator.pop(context, false),
            child: const Text('稍后安装'),
          ),
          FilledButton(
            onPressed: () => Navigator.pop(context, true),
            child: const Text('开始更新'),
          ),
        ],
      ),
    );
    if (startUpdate != true) return;
    if (UpdateActivity.playing || await repository.updateBusy()) {
      throw AppFailure('播放或下载中，暂不更新。');
    }
    final applied = await repository.applyUpdate();
    if (applied['restart'] != true) throw AppFailure('版本切换未就绪，继续使用原版。');
    exit(74);
  } catch (error) {
    closeDialog();
    if (context.mounted) {
      await showDialog<void>(
        context: context,
        builder: (context) => AlertDialog(
          title: const Text('更新未完成'),
          content: Text('$error\n\n原版本保持不变。'),
          actions: [
            TextButton(
              onPressed: () => Navigator.pop(context),
              child: const Text('知道了'),
            ),
          ],
        ),
      );
    }
  }
}

String _size(double bytes) {
  if (bytes >= 1024 * 1024) return '${(bytes / 1048576).toStringAsFixed(1)} MB';
  if (bytes >= 1024) return '${(bytes / 1024).toStringAsFixed(0)} KB';
  return '${bytes.toInt()} B';
}
