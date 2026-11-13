import 'package:path/path.dart' as path;

import 'local_media_catalog.dart';
import 'media_library.dart';
import 'models.dart';

class LocalExportGroup {
  const LocalExportGroup({
    required this.drama,
    required this.jobs,
    required this.pending,
    this.directory,
  });

  final Drama drama;
  final List<DownloadJob> jobs, pending;
  final String? directory;
  int get saved => jobs.length - pending.length;
  bool get complete => jobs.isNotEmpty && pending.isEmpty;
  String get label => complete
      ? '已保存 $saved/${jobs.length} 集 · 无需重复导出'
      : '已保存 $saved/${jobs.length} 集 · 待导出 ${pending.length} 集';
}

List<LocalExportGroup> localExportGroups(
  List<DownloadJob> jobs,
  LocalMediaCatalog catalog,
) {
  final files = <String, LocalMediaFile>{
    for (final folder in catalog.folders)
      for (final file in folder.files)
        if (file.item?.kind == 'export' && file.item!.jobId.isNotEmpty)
          file.item!.jobId: file,
  };
  final groups = <String, List<DownloadJob>>{};
  for (final job in jobs.where((job) => job.completed)) {
    (groups[job.drama.id] ??= []).add(job);
  }
  return [
    for (final group in groups.values)
      () {
        group.sort((a, b) => a.episode.number.compareTo(b.episode.number));
        String? directory;
        final pending = <DownloadJob>[];
        for (final job in group) {
          final file = files[job.id], item = file?.item;
          if (file == null ||
              item == null ||
              item.drama.id != job.drama.id ||
              item.sourceVersion != mediaSourceVersion(job) ||
              file.bytes != item.bytes ||
              item.episodes.length != 1 ||
              item.episodes.single != job.episode.number) {
            pending.add(job);
          } else {
            directory ??= path.joinAll(path.split(file.file).take(2));
          }
        }
        return LocalExportGroup(
          drama: group.first.drama,
          jobs: List.unmodifiable(group),
          pending: List.unmodifiable(pending),
          directory: directory,
        );
      }(),
  ];
}
