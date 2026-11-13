import 'dart:convert';
import 'dart:io';

import 'package:path/path.dart' as path;

import 'core_bridge.dart';
import 'local_media_catalog.dart';
import 'media_library.dart';
import 'models.dart';

class LocalMergeCandidate {
  LocalMergeCandidate(List<DownloadJob> input, Map<String, String> files)
    : jobs = List.unmodifiable(
        List<DownloadJob>.of(input)
          ..sort((a, b) => a.episode.number.compareTo(b.episode.number)),
      ),
      files = Map.unmodifiable(files) {
    try {
      continuousMergeJobs(jobs);
    } catch (failure) {
      error = failure.toString();
    }
  }

  final List<DownloadJob> jobs;
  final Map<String, String> files;
  String? error;
  Drama get drama => jobs.first.drama;
  String get id => drama.id;
  bool get available => error == null;
  String get fingerprint => jsonEncode([
    for (final job in jobs) [job.id, mediaSourceVersion(job), files[job.id]],
  ]);
  String get label =>
      '已下载 ${jobs.length} 集 · 第 ${jobs.first.episode.number}–${jobs.last.episode.number} 集';
}

class LocalMergeSnapshot {
  const LocalMergeSnapshot({
    this.candidates = const [],
    this.missing = 0,
    this.unregistered = 0,
  });

  final List<LocalMergeCandidate> candidates;
  final int missing, unregistered;
}

Future<LocalMergeSnapshot> readLocalMergeSnapshot(
  MediaLibrary library,
  AppRepository repository,
  Set<String> allowedSources,
) async {
  await library.reload();
  final root = library.root;
  if (root == null) throw AppFailure('下载目录尚未就绪');
  final catalog = await scanLocalMediaCatalog(
    root,
    library.items,
    allowedSources,
  );
  final files = <String, LocalMediaFile>{};
  var unregistered = 0;
  for (final folder in catalog.folders) {
    if (folder.error.isNotEmpty) throw AppFailure(folder.error);
    for (final file in folder.files) {
      final item = file.item;
      if (item == null) {
        unregistered++;
      } else if (item.kind == 'export') {
        final id = item.jobId.isNotEmpty
            ? item.jobId
            : item.id.startsWith('export-')
            ? item.id.substring(7)
            : '';
        if (id.isNotEmpty) files[id] = file;
      }
    }
  }
  final groups = <String, List<DownloadJob>>{};
  final paths = <String, String>{};
  var missing = 0;
  for (final job in (await repository.downloads()).where(
    (job) => job.completed && allowedSources.contains(job.drama.source),
  )) {
    final file = files[job.id], item = file?.item;
    String? local;
    if (file != null &&
        item != null &&
        item.drama.id == job.drama.id &&
        item.sourceVersion == mediaSourceVersion(job) &&
        item.episodes.length == 1 &&
        item.episodes.single == job.episode.number &&
        item.bytes == file.bytes) {
      local = file.file;
    } else if (!job.published) {
      try {
        final plan = await repository.localPlayback(job.drama, job.episode);
        if (plan != null && plan.local && path.isAbsolute(plan.url)) {
          final realRoot = await Directory(root).resolveSymbolicLinks();
          final realFile = await File(plan.url).resolveSymbolicLinks();
          final stat = await File(realFile).stat();
          if (path.isWithin(realRoot, realFile) &&
              stat.type == FileSystemEntityType.file &&
              stat.size > 0) {
            local = path.relative(realFile, from: realRoot);
          }
        }
      } catch (_) {}
    }
    if (local == null) {
      missing++;
      continue;
    }
    (groups[job.drama.id] ??= []).add(job);
    paths[job.id] = local;
  }
  final rows = [
    for (final jobs in groups.values)
      LocalMergeCandidate(jobs, {
        for (final job in jobs) job.id: paths[job.id]!,
      }),
  ]..sort((a, b) => compareMediaNames(a.drama.title, b.drama.title));
  return LocalMergeSnapshot(
    candidates: List.unmodifiable(rows),
    missing: missing,
    unregistered: unregistered,
  );
}
