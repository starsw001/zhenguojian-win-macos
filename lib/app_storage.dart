import 'dart:convert';
import 'dart:io';

import 'package:crypto/crypto.dart';
import 'package:path/path.dart' as p;
import 'package:path_provider_platform_interface/path_provider_platform_interface.dart';
import 'package:shared_preferences_platform_interface/shared_preferences_platform_interface.dart';

import 'portable_storage.dart';

Future<void>? _preparedStorage;

Future<void> prepareAppStorage() async {
  if (!Platform.isWindows) return;
  await (_preparedStorage ??= _preparePortableStorage());
}

Future<void> _preparePortableStorage() async {
  final layout = PortableStorageLayout(
    Platform.resolvedExecutable,
    updateRoot: _trustedUpdateRoot(),
  );
  await layout.prepare();
  PathProviderPlatform.instance = PortablePathProvider(layout);
  SharedPreferencesStorePlatform.instance = PortablePreferencesStore(layout);
}

String? _trustedUpdateRoot() {
  final environment = Platform.environment;
  final root = environment['GUO_UPDATE_ROOT'];
  final token = environment['GUO_UPDATE_SESSION'] ?? '';
  final parent = int.tryParse(environment['GUO_UPDATE_PARENT_PID'] ?? '');
  if (root == null ||
      !p.isAbsolute(root) ||
      !RegExp(r'^[a-f0-9]{40}$').hasMatch(token) ||
      parent == null ||
      parent <= 0) {
    return null;
  }
  final executable = p.normalize(Platform.resolvedExecutable);
  final normalizedRoot = p.normalize(root);
  if (!p.equals(p.dirname(executable), normalizedRoot) &&
      !p.isWithin(normalizedRoot, executable)) {
    return null;
  }
  return normalizedRoot;
}

Future<Map<String, String>> _storageFiles(Directory root) async {
  final result = <String, String>{};
  Future<void> visit(Directory directory) async {
    await for (final entry in directory.list(followLinks: false)) {
      final relative = p.relative(entry.path, from: root.path);
      if (relative == 'downloads') continue;
      if (entry is Link) throw const FileSystemException('旧配置包含链接，已保留原数据并停止迁移');
      if (entry is Directory) {
        await visit(entry);
      } else if (entry is File) {
        result[relative] = (await sha256.bind(entry.openRead()).first)
            .toString();
      }
    }
  }

  await visit(root);
  return result;
}

Future<bool> migrateLegacyAppStorage({
  required Directory legacy,
  required Directory target,
}) async {
  if (p.equals(p.absolute(legacy.path), p.absolute(target.path))) return false;
  if (!await legacy.exists()) return false;
  if (await FileSystemEntity.isLink(legacy.path) ||
      await FileSystemEntity.isLink(target.path)) {
    throw const FileSystemException('配置目录不能是链接，已保留原数据并停止迁移');
  }
  await target.parent.create(recursive: true);
  final lock = await File(
    p.join(target.parent.path, '.storage-migration.lock'),
  ).open(mode: FileMode.append);
  try {
    await lock.lock(FileLock.blockingExclusive);
    if (await target.exists() && !await target.list().isEmpty) return false;
    final before = await _storageFiles(legacy);
    if (before.isEmpty) return false;
    final staging = await target.parent.createTemp('.storage-migration-');
    for (final entry in before.entries) {
      final input = File(p.join(legacy.path, entry.key));
      final output = File(p.join(staging.path, entry.key));
      await output.parent.create(recursive: true);
      await input.copy(output.path);
      final digest = (await sha256.bind(output.openRead()).first).toString();
      if (digest != entry.value) {
        throw const FileSystemException('配置复制校验失败，原数据已保留，请退出旧客户端后重试');
      }
    }
    final after = await _storageFiles(legacy);
    if (before.length != after.length ||
        before.entries.any((entry) => after[entry.key] != entry.value)) {
      throw const FileSystemException('旧配置仍在变化，原数据已保留，请退出旧客户端后重试');
    }
    if (await FileSystemEntity.type(
          p.join(legacy.path, 'downloads'),
          followLinks: false,
        ) ==
        FileSystemEntityType.link) {
      throw const FileSystemException('旧下载目录包含链接，已停止迁移');
    }
    final oldDownloads = Directory(p.join(legacy.path, 'downloads'));
    final location = File(p.join(staging.path, 'download-location.json'));
    if (!await location.exists() && await oldDownloads.exists()) {
      await location.writeAsString(
        jsonEncode({'directory': oldDownloads.absolute.path}),
        flush: true,
      );
    }
    await File(
      p.join(staging.path, 'app-storage-migration.json'),
    ).writeAsString(
      jsonEncode({'schema': 1, 'source': legacy.absolute.path}),
      flush: true,
    );
    if (await target.exists()) {
      if (!await target.list().isEmpty) {
        throw const FileSystemException('新配置目录已被使用，原数据已保留，未覆盖任何记录');
      }
      await target.delete();
    }
    await staging.rename(target.path);
    return true;
  } finally {
    await lock.close();
  }
}
