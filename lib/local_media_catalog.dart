import 'dart:io';
import 'dart:isolate';

import 'package:path/path.dart' as path;

import 'media_library.dart';
import 'models.dart';

class LocalMediaFile {
  const LocalMediaFile({required this.file, required this.bytes, this.item});

  final String file;
  final int bytes;
  final LocalMediaItem? item;
  String get name => path.basename(file);
  String get title {
    final media = item;
    if (media == null || media.episodes.isEmpty) return name;
    if (media.special) return '特别篇 ${media.specialNumber}';
    return '第 ${media.episodes.first} 集';
  }
}

class LocalMediaFolder {
  const LocalMediaFolder({
    required this.directory,
    required this.name,
    required this.files,
    required this.bytes,
    this.error = '',
  });

  final String directory, name, error;
  final List<LocalMediaFile> files;
  final int bytes;
}

class LocalMediaCatalog {
  const LocalMediaCatalog({this.folders = const [], this.merged = const []});

  final List<LocalMediaFolder> folders;
  final List<LocalMediaItem> merged;
}

Future<LocalMediaCatalog> scanLocalMediaCatalog(
  String root,
  List<LocalMediaItem> items,
  Set<String> allowedSources,
) => Isolate.run(() => _scanCatalog(root, items, allowedSources));

String _fileKey(String value) {
  final normalized = path.normalize(value).replaceAll('\\', '/');
  return Platform.isWindows ? normalized.toLowerCase() : normalized;
}

int compareMediaNames(String first, String second) {
  final pattern = RegExp(r'\d+|\D+');
  final left = pattern.allMatches(first.toLowerCase()).toList();
  final right = pattern.allMatches(second.toLowerCase()).toList();
  for (var i = 0; i < left.length && i < right.length; i++) {
    final a = left[i][0]!, b = right[i][0]!;
    final numberA = int.tryParse(a), numberB = int.tryParse(b);
    final order = numberA != null && numberB != null
        ? numberA.compareTo(numberB)
        : a.compareTo(b);
    if (order != 0) return order;
  }
  final order = left.length.compareTo(right.length);
  return order == 0 ? first.compareTo(second) : order;
}

LocalMediaCatalog _scanCatalog(
  String root,
  List<LocalMediaItem> items,
  Set<String> allowedSources,
) {
  final exports = path.join(root, 'exports');
  final indexed = {
    for (final item in items)
      if (allowedSources.contains(item.drama.source)) _fileKey(item.file): item,
  };
  final merged = <LocalMediaItem>[];
  for (final item in indexed.values.where((item) => item.merged)) {
    final full = path.normalize(path.join(root, item.file));
    if (!path.isWithin(root, full)) continue;
    try {
      if (FileSystemEntity.typeSync(full, followLinks: false) ==
              FileSystemEntityType.file &&
          File(full).lengthSync() > 0) {
        merged.add(item);
      }
    } on FileSystemException {
      continue;
    }
  }
  merged.sort((a, b) => b.created.compareTo(a.created));
  if (FileSystemEntity.typeSync(exports, followLinks: false) !=
      FileSystemEntityType.directory) {
    return LocalMediaCatalog(merged: List.unmodifiable(merged));
  }
  final knownSources = SourceSite.knownValues.map((site) => site.id).toSet();
  final folders = <LocalMediaFolder>[];
  final loose = <LocalMediaFile>[];
  final marker = RegExp(
    r'\[([a-z][a-z0-9-]*)-[0-9a-f]{12}\]$',
    caseSensitive: false,
  );
  bool allowedDirectory(String name) {
    final source = marker.firstMatch(name)?[1]?.toLowerCase();
    return source == null ||
        !knownSources.contains(source) ||
        allowedSources.contains(source);
  }

  LocalMediaFile? readFile(FileSystemEntity entity) {
    if (entity is! File ||
        !{
          '.mp4',
          '.mkv',
          '.m4v',
          '.webm',
          '.mov',
          '.avi',
        }.contains(path.extension(entity.path).toLowerCase())) {
      return null;
    }
    final relative = path.relative(entity.path, from: root);
    final media = indexed[_fileKey(relative)];
    if (media?.merged == true) return null;
    try {
      final stat = entity.statSync();
      if (stat.type != FileSystemEntityType.file || stat.size <= 0) return null;
      return LocalMediaFile(file: relative, bytes: stat.size, item: media);
    } on FileSystemException {
      return null;
    }
  }

  void sortFiles(List<LocalMediaFile> files) {
    files.sort((a, b) => compareMediaNames(a.file, b.file));
  }

  for (final entity in Directory(exports).listSync(followLinks: false)) {
    final name = path.basename(entity.path);
    if (name.startsWith('.')) continue;
    if (entity is Directory && allowedDirectory(name)) {
      final files = <LocalMediaFile>[];
      var error = '';
      try {
        for (final child in entity.listSync(
          recursive: true,
          followLinks: false,
        )) {
          final file = readFile(child);
          if (file != null) files.add(file);
        }
      } on FileSystemException catch (failure) {
        error = '目录读取未完成：${failure.osError?.message ?? failure.message}';
      }
      sortFiles(files);
      folders.add(
        LocalMediaFolder(
          directory: path.relative(entity.path, from: root),
          name: name,
          files: List.unmodifiable(files),
          bytes: files.fold(0, (sum, file) => sum + file.bytes),
          error: error,
        ),
      );
    } else {
      final file = readFile(entity);
      if (file != null) loose.add(file);
    }
  }
  folders.sort((a, b) => compareMediaNames(a.name, b.name));
  if (loose.isNotEmpty) {
    sortFiles(loose);
    folders.insert(
      0,
      LocalMediaFolder(
        directory: 'exports',
        name: 'exports 根目录文件',
        files: List.unmodifiable(loose),
        bytes: loose.fold(0, (sum, file) => sum + file.bytes),
      ),
    );
  }
  return LocalMediaCatalog(
    folders: List.unmodifiable(folders),
    merged: List.unmodifiable(merged),
  );
}
