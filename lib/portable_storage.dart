import 'dart:async';
import 'dart:convert';
import 'dart:io';

import 'package:path/path.dart' as p;
import 'package:path_provider_platform_interface/path_provider_platform_interface.dart';
import 'package:shared_preferences_platform_interface/shared_preferences_platform_interface.dart';
import 'package:shared_preferences_platform_interface/types.dart';

class PortableStorageLayout {
  PortableStorageLayout(String executable)
    : root = p.dirname(p.normalize(executable)) {
    if (!p.isAbsolute(executable)) {
      throw ArgumentError.value(executable, 'executable', '软件路径必须是绝对路径');
    }
  }

  final String root;
  String get data => p.join(root, 'app-data');
  String get preferences => p.join(data, 'shared_preferences.json');

  Future<void> checkPath(String path) async {
    var current = p.normalize(p.absolute(path));
    if (!p.equals(root, current) && !p.isWithin(root, current)) {
      throw const FileSystemException('应用数据路径必须位于当前版本目录内');
    }
    while (true) {
      if (await FileSystemEntity.isLink(current)) {
        throw FileSystemException('版本目录及应用数据不能使用链接', current);
      }
      final parent = p.dirname(current);
      if (parent == current) break;
      current = parent;
    }
  }

  Future<String> directory(String path) async {
    await checkPath(path);
    await Directory(path).create(recursive: true);
    await checkPath(path);
    return path;
  }

  Future<void> prepare() async {
    await directory(data);
    final file = File(p.join(data, 'sources.json'));
    await checkPath(file.path);
    if (!await file.exists()) {
      try {
        await file.create(exclusive: true);
        await file.writeAsString('{}\n', flush: true);
      } on FileSystemException {
        if (!await file.exists()) rethrow;
      }
    }
    if (await file.length() > 32 * 1024 * 1024) {
      throw FileSystemException('sources.json 过大，请检查当前版本中的文件', file.path);
    }
    if (jsonDecode(await file.readAsString()) is! Map<String, dynamic>) {
      throw FormatException('sources.json 必须是 JSON 对象；原文件已保留', file.path);
    }
  }
}

class PortablePathProvider extends PathProviderPlatform {
  PortablePathProvider(this.layout);
  final PortableStorageLayout layout;

  @override
  Future<String> getApplicationSupportPath() => layout.directory(layout.data);

  @override
  Future<String> getApplicationDocumentsPath() =>
      layout.directory(p.join(layout.data, 'documents'));

  @override
  Future<String> getLibraryPath() => getApplicationSupportPath();

  @override
  Future<String> getApplicationCachePath() =>
      layout.directory(p.join(layout.data, 'cache'));

  @override
  Future<String> getTemporaryPath() =>
      layout.directory(p.join(layout.data, 'temp'));

  @override
  Future<String> getDownloadsPath() =>
      layout.directory(p.join(layout.root, 'download'));
}

class PortablePreferencesStore extends SharedPreferencesStorePlatform {
  PortablePreferencesStore(this.layout);
  final PortableStorageLayout layout;
  Future<void> _pending = Future<void>.value();

  Future<T> _locked<T>(Future<T> Function() action) {
    final result = _pending.then((_) async {
      await layout.directory(layout.data);
      final lockPath = p.join(layout.data, '.preferences.lock');
      await layout.checkPath(lockPath);
      final lock = await File(lockPath).open(mode: FileMode.append);
      var acquired = false;
      try {
        await lock.lock(FileLock.blockingExclusive);
        acquired = true;
        return await action();
      } finally {
        if (acquired) await lock.unlock();
        await lock.close();
      }
    });
    _pending = result.then<void>((_) {}, onError: (Object _, StackTrace _) {});
    return result;
  }

  Future<Map<String, Object>> _read() async {
    await layout.checkPath(layout.preferences);
    final file = File(layout.preferences);
    if (!await file.exists()) return {};
    if (await file.length() > 32 * 1024 * 1024) {
      throw const FormatException('当前版本设置文件过大，原文件已保留');
    }
    final decoded = jsonDecode(await file.readAsString());
    if (decoded is! Map<String, dynamic>) {
      throw const FormatException('当前版本设置格式错误，原文件已保留');
    }
    return decoded.map((key, value) {
      if (value is List && value.every((item) => item is String)) {
        return MapEntry(key, value.cast<String>().toList());
      }
      if (value is! String && value is! num && value is! bool) {
        throw const FormatException('当前版本设置包含不支持的值，原文件已保留');
      }
      return MapEntry(key, value as Object);
    });
  }

  Future<bool> _write(Map<String, Object> values) async {
    final body = jsonEncode(values);
    if (utf8.encode(body).length > 32 * 1024 * 1024) {
      throw const FormatException('当前版本设置超出容量，原文件已保留');
    }
    final temporary = await File(
      layout.preferences,
    ).parent.createTemp('.prefs-');
    final file = File(p.join(temporary.path, 'preferences.json'));
    try {
      await file.writeAsString(body, flush: true);
      await layout.checkPath(layout.preferences);
      await file.rename(layout.preferences);
    } finally {
      if (await file.exists()) await file.delete();
      await temporary.delete();
    }
    return true;
  }

  @override
  Future<Map<String, Object>> getAll() => getAllWithParameters(
    GetAllParameters(filter: PreferencesFilter(prefix: 'flutter.')),
  );

  @override
  Future<Map<String, Object>> getAllWithPrefix(String prefix) =>
      getAllWithParameters(
        GetAllParameters(filter: PreferencesFilter(prefix: prefix)),
      );

  @override
  Future<Map<String, Object>> getAllWithParameters(
    GetAllParameters parameters,
  ) => _locked(() async {
    final values = await _read();
    final filter = parameters.filter;
    values.removeWhere(
      (key, _) =>
          !key.startsWith(filter.prefix) ||
          (filter.allowList != null && !filter.allowList!.contains(key)),
    );
    return values;
  });

  @override
  Future<bool> setValue(String valueType, String key, Object value) =>
      _locked(() async {
        final values = await _read();
        values[key] = value;
        return _write(values);
      });

  @override
  Future<bool> remove(String key) => _locked(() async {
    final values = await _read();
    values.remove(key);
    return _write(values);
  });

  @override
  Future<bool> clear() => clearWithParameters(
    ClearParameters(filter: PreferencesFilter(prefix: 'flutter.')),
  );

  @override
  Future<bool> clearWithPrefix(String prefix) => clearWithParameters(
    ClearParameters(filter: PreferencesFilter(prefix: prefix)),
  );

  @override
  Future<bool> clearWithParameters(ClearParameters parameters) =>
      _locked(() async {
        final values = await _read();
        final filter = parameters.filter;
        values.removeWhere(
          (key, _) =>
              key.startsWith(filter.prefix) &&
              (filter.allowList == null || filter.allowList!.contains(key)),
        );
        return _write(values);
      });
}
