import 'dart:io';

import 'package:path/path.dart' as path;
import 'package:url_launcher/url_launcher.dart';

import 'core_bridge.dart';
import 'models.dart';

class DownloadFolderOpener {
  const DownloadFolderOpener({this.launch, this.desktopSupported});

  final Future<bool> Function(Uri)? launch;
  final bool? desktopSupported;

  bool get supported =>
      desktopSupported ??
      (Platform.isWindows || Platform.isMacOS || Platform.isLinux);

  Future<String> resolve(
    AppRepository repository,
    Iterable<DownloadJob> jobs,
  ) async {
    if (!supported) throw AppFailure('当前平台不支持打开文件夹');
    final completed = jobs.where((job) => job.completed).toList();
    if (completed.isNotEmpty) {
      final directory = await repository.downloadedFolder(completed);
      if (directory != null && path.isAbsolute(directory)) return directory;
    }
    throw AppFailure('找不到已下载的 MP4，可能正在保存成品，或文件已被移动、删除');
  }

  Future<void> open(String directory) async {
    if (!supported) throw AppFailure('当前平台不支持打开文件夹');
    final uri = Uri.directory(directory, windows: Platform.isWindows);
    final opened =
        await (launch?.call(uri) ??
            launchUrl(uri, mode: LaunchMode.externalApplication));
    if (!opened) throw AppFailure('无法打开文件夹，请检查目录权限或系统文件管理器');
  }
}
