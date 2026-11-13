import 'dart:convert';
import 'dart:io';

import 'package:duanju_app/core_bridge.dart';
import 'package:duanju_app/local_store.dart';
import 'package:duanju_app/media_library.dart';
import 'package:duanju_app/media_pipeline.dart';
import 'package:duanju_app/models.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:path/path.dart' as path;
import 'package:shared_preferences/shared_preferences.dart';

import 'fixtures.dart';

class ManualDeletionRepository extends FixtureRepository {
  ManualDeletionRepository(this.directory, this.job);
  final Directory directory;
  final DownloadJob job;
  int localReads = 0;

  @override
  Future<String> downloadDirectory() async => directory.path;
  @override
  Future<List<DownloadJob>> downloads() async => [job];
  @override
  Future<PlaybackPlan?> localPlayback(Drama drama, Episode episode) async {
    localReads++;
    throw AppFailure('must not regenerate a manually deleted export');
  }
}

class NoAutomaticMediaWork implements MediaExecutor {
  int runs = 0;
  @override
  Future<void> run(
    List<String> arguments, {
    double duration = 0,
    void Function(double)? progress,
  }) async {
    runs++;
    throw StateError('unexpected automatic export');
  }

  @override
  Future<MediaProbe> probe(String file) async =>
      throw StateError('unexpected media probe');
  @override
  Future<void> cancel() async {}
}

void main() {
  TestWidgetsFlutterBinding.ensureInitialized();
  for (final published in [false, true]) {
    test(
      'deleted published=$published export is not regenerated and records stay unchanged',
      () async {
        SharedPreferences.setMockInitialValues({});
        final store = LocalStore(await SharedPreferences.getInstance());
        final directory = await Directory.systemTemp.createTemp(
          'manual-export-delete-',
        );
        addTearDown(store.dispose);
        addTearDown(() => directory.delete(recursive: true));
        final episode = Episode({'id': '1', 'title': '第1集'}, 1);
        final job = DownloadJob(
          id: 'job1',
          drama: FixtureRepository.free,
          episode: episode,
          state: 'completed',
          bytes: 4,
          created: 3,
          actualQuality: 720,
          revision: 1,
          published: published,
        );
        final item = LocalMediaItem(
          id: 'export-job1',
          drama: job.drama,
          file: path.join('exports', 'ordinary [hongguo-test]', 'S01E001.mp4'),
          kind: 'export',
          episodes: [1],
          duration: 60,
          bytes: 4,
          created: DateTime(2026, 10, 26),
          jobId: job.id,
          sourceVersion: '3-4-720-1',
        );
        final index = File(path.join(directory.path, 'media-library.json'));
        final contents = jsonEncode({
          'items': [item.toJson()],
          'showFolders': {job.drama.id: path.dirname(item.file)},
          'skipped': [],
          'skippedVersions': {},
          'specialNumbers': {},
        });
        await index.writeAsString(contents);
        final repository = ManualDeletionRepository(directory, job);
        final executor = NoAutomaticMediaWork();
        final library = MediaLibrary(
          repository,
          store,
          executor: executor,
          automaticWorker: true,
        );
        addTearDown(library.dispose);
        await library.maybeExport();
        await library.maybeExport();
        expect(repository.localReads, 0);
        expect(executor.runs, 0);
        expect(await index.readAsString(), contents);
        expect(library.items.single.id, item.id);
        expect(
          await File(library.fileFor(library.items.single)).exists(),
          isFalse,
        );
        expect(store.preferences.getKeys(), isEmpty);
      },
    );
  }
}
