import 'dart:convert';
import 'dart:io';

import 'package:duanju_app/local_media_actions.dart';
import 'package:duanju_app/local_media_catalog.dart';
import 'package:duanju_app/media_library.dart';
import 'package:duanju_app/media_pipeline.dart';
import 'package:duanju_app/models.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:path/path.dart' as path;
import 'package:shared_preferences/shared_preferences.dart';

import 'fixtures.dart';

DownloadJob actionJob(int number, {int revision = 1}) => DownloadJob(
  id: 'job$number',
  drama: FixtureRepository.free,
  episode: Episode({'id': '$number', 'currentEpisode': number}, number),
  state: 'completed',
  bytes: 4,
  created: number,
  actualQuality: 720,
  revision: revision,
  published: true,
);

LocalMediaItem actionItem(DownloadJob job, {String? version, int bytes = 4}) =>
    LocalMediaItem(
      id: 'export-${job.id}',
      drama: job.drama,
      file: path.join(
        'exports',
        '测试 [hongguo-111111111111]',
        'S01E${job.episode.number.toString().padLeft(3, '0')}.mp4',
      ),
      kind: 'export',
      episodes: [job.episode.number],
      duration: 60,
      bytes: bytes,
      created: DateTime(2026, 10, 26),
      jobId: job.id,
      sourceVersion: version ?? mediaSourceVersion(job),
    );

LocalMediaCatalog actionCatalog(
  List<LocalMediaItem> items, {
  int? actualBytes,
}) => LocalMediaCatalog(
  folders: [
    LocalMediaFolder(
      directory: path.join('exports', '测试 [hongguo-111111111111]'),
      name: '测试',
      files: [
        for (final item in items)
          LocalMediaFile(
            file: item.file,
            bytes: actualBytes ?? item.bytes,
            item: item,
          ),
      ],
      bytes: items.fold(0, (sum, item) => sum + item.bytes),
    ),
  ],
);

MediaProbe actionProbe({
  String video = 'h264',
  String audio = 'aac',
  double duration = 60,
}) => MediaProbe({
  'format': {'duration': '$duration'},
  'streams': [
    {
      'codec_type': 'video',
      'codec_name': video,
      'width': 1280,
      'height': 720,
      'pix_fmt': 'yuv420p',
      'extradata_hash': 'same',
    },
    if (audio.isNotEmpty)
      {
        'codec_type': 'audio',
        'codec_name': audio,
        'sample_rate': 48000,
        'channels': 2,
        'channel_layout': 'stereo',
        'extradata_hash': 'same',
      },
  ],
});

class ActionRepository extends FixtureRepository {
  ActionRepository(this.directory, this.jobs);
  final Directory directory;
  final List<DownloadJob> jobs;
  @override
  Future<String> downloadDirectory() async => directory.path;
  @override
  Future<List<DownloadJob>> downloads() async => jobs;
  @override
  Future<PlaybackPlan?> localPlayback(Drama drama, Episode episode) async =>
      PlaybackPlan(
        url: path.join(
          directory.path,
          actionItem(
            jobs.firstWhere((job) => job.episode.number == episode.number),
          ).file,
        ),
        local: true,
      );
}

class ActionExecutor implements MediaExecutor {
  final commands = <List<String>>[];
  @override
  Future<void> run(
    List<String> arguments, {
    double duration = 0,
    void Function(double)? progress,
  }) async {
    commands.add(List.of(arguments));
    if (arguments.last != '-') {
      await File(arguments.last).writeAsString('synthetic media fixture');
    }
    progress?.call(1);
  }

  @override
  Future<MediaProbe> probe(String file) async => actionProbe(
    duration:
        path.basename(file).startsWith('full.') ||
            path.basename(file).startsWith('special')
        ? 120
        : 60,
  );
  @override
  Future<void> cancel() async {}
}

void main() {
  TestWidgetsFlutterBinding.ensureInitialized();
  test(
    'ten existing exact-version MP4s are all saved with no export work pending',
    () {
      final jobs = [for (var i = 1; i <= 10; i++) actionJob(i)];
      final group = localExportGroups(
        jobs,
        actionCatalog(jobs.map(actionItem).toList()),
      ).single;
      expect(group.complete, isTrue);
      expect(group.saved, 10);
      expect(group.pending, isEmpty);
      expect(group.label, '已保存 10/10 集 · 无需重复导出');
      expect(
        group.directory,
        path.join('exports', '测试 [hongguo-111111111111]'),
      );
    },
  );

  test('missing wrong-size and changed-version exports remain pending', () {
    final jobs = [actionJob(1), actionJob(2), actionJob(3)];
    final catalog = actionCatalog([
      actionItem(jobs[0]),
      actionItem(jobs[1], version: 'old'),
    ]);
    final group = localExportGroups(jobs, catalog).single;
    expect(group.pending.map((job) => job.id), ['job2', 'job3']);
    expect(group.saved, 1);
    expect(
      localExportGroups(
        [jobs.first],
        actionCatalog([actionItem(jobs.first)], actualBytes: 9),
      ).single.pending,
      hasLength(1),
    );
  });

  test(
    'groups keep numeric episode order and saved state does not mutate input',
    () {
      final jobs = [actionJob(10), actionJob(2), actionJob(1)];
      final before = jobs.map((job) => job.id).toList();
      final group = localExportGroups(jobs, const LocalMediaCatalog()).single;
      expect(group.jobs.map((job) => job.episode.number), [1, 2, 10]);
      expect(jobs.map((job) => job.id), before);
      expect(localExportGroups([], const LocalMediaCatalog()), isEmpty);
    },
  );

  test('source version preserves the persisted revision convention', () {
    expect(mediaSourceVersion(actionJob(1)), '1-4-720-1');
    expect(mediaSourceVersion(actionJob(1, revision: 0)), '1-4-720');
  });

  test(
    'compatible merge plans prefer real MP4 and incompatible combinations keep MKV',
    () {
      expect(
        MergePlan.create([actionProbe(), actionProbe()]).outputExtension,
        'mp4',
      );
      expect(
        MergePlan.create([
          actionProbe(video: 'hevc'),
          actionProbe(video: 'hevc'),
        ]).outputExtension,
        'mp4',
      );
      expect(
        MergePlan.create([
          actionProbe(video: 'vp9', audio: 'opus'),
          actionProbe(video: 'vp9', audio: 'opus'),
        ]).outputExtension,
        'mkv',
      );
    },
  );

  for (final present in [true, false]) {
    test(
      'completed merge with product present=$present is reused only while intact',
      () async {
        final directory = await Directory.systemTemp.createTemp('merge-dedup-');
        addTearDown(() => directory.delete(recursive: true));
        SharedPreferences.setMockInitialValues({});
        final store = testStore(await SharedPreferences.getInstance());
        addTearDown(store.dispose);
        final jobs = [actionJob(1), actionJob(2)];
        final task = MergeQueueJob(
          id: 'existing',
          owner: 'default',
          drama: jobs.first.drama,
          jobIds: jobs.map((job) => job.id).toList(),
          episodes: [1, 2],
          versions: {for (final job in jobs) job.id: mediaSourceVersion(job)},
          state: 'completed',
          created: DateTime(2026, 10, 26),
        );
        final merged = LocalMediaItem(
          id: task.outputId,
          drama: task.drama,
          file: path.join('library', task.outputId, 'full.mp4'),
          kind: 'merged',
          episodes: [1, 2],
          duration: 120,
          bytes: 4,
          created: task.created,
          decodeVerified: true,
        );
        if (present) {
          final file = File(path.join(directory.path, merged.file));
          await file.parent.create(recursive: true);
          await file.writeAsString('keep');
        }
        await File(
          path.join(directory.path, 'media-library.json'),
        ).writeAsString(
          jsonEncode({
            'items': [merged.toJson()],
          }),
        );
        await File(path.join(directory.path, 'merge-queue.json')).writeAsString(
          jsonEncode({
            'schema': 1,
            'jobs': [task.toJson()],
          }),
        );
        final executor = ActionExecutor();
        final library = MediaLibrary(
          ActionRepository(directory, jobs),
          store,
          executor: executor,
        )..suspended = true;
        addTearDown(library.dispose);
        final added = await library.merges.enqueue([jobs]);
        expect(added, present ? 0 : 1);
        expect(library.merges.items, hasLength(present ? 1 : 2));
        expect(executor.commands, isEmpty);
        expect(library.merges.items.every((job) => !job.cleanup), isTrue);
      },
    );
  }

  test(
    'local merge builds MP4, fully checks it and special export keeps the true extension',
    () async {
      final directory = await Directory.systemTemp.createTemp('merge-mp4-');
      addTearDown(() => directory.delete(recursive: true));
      SharedPreferences.setMockInitialValues({});
      final store = testStore(await SharedPreferences.getInstance());
      addTearDown(store.dispose);
      final jobs = [actionJob(1), actionJob(2)];
      final items = jobs.map(actionItem).toList();
      for (final item in items) {
        final file = File(path.join(directory.path, item.file));
        await file.parent.create(recursive: true);
        await file.writeAsString('keep');
      }
      await File(path.join(directory.path, 'media-library.json')).writeAsString(
        jsonEncode({'items': items.map((item) => item.toJson()).toList()}),
      );
      final executor = ActionExecutor();
      final library = MediaLibrary(
        ActionRepository(directory, jobs),
        store,
        executor: executor,
      );
      addTearDown(library.dispose);
      final output = await library.merge(jobs);
      expect(output.file.endsWith('full.mp4'), isTrue);
      expect(output.decodeVerified, isTrue);
      expect(output.episodes, [1, 2]);
      expect(
        executor.commands.any(
          (command) =>
              command.contains('+faststart') &&
              command.last.endsWith('full.mp4'),
        ),
        isTrue,
      );
      expect(
        executor.commands.any(
          (command) =>
              command.contains('-xerror') &&
              command.contains(library.fileFor(output)) == false &&
              command.last == '-',
        ),
        isTrue,
      );
      final special = await library.exportMerged(output);
      expect(special.file.endsWith('S00E001.mp4'), isTrue);
      expect(await File(library.fileFor(special)).exists(), isTrue);
      for (final item in items) {
        expect(
          await File(path.join(directory.path, item.file)).readAsString(),
          'keep',
        );
      }
      expect(
        directory
            .listSync(recursive: true)
            .whereType<File>()
            .any((file) => file.path.endsWith('.nfo')),
        isFalse,
      );
    },
  );
}
