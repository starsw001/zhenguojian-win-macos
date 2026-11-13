import 'dart:io';

import 'package:duanju_app/core_bridge.dart';
import 'package:duanju_app/download_folders.dart';
import 'package:duanju_app/models.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:path/path.dart' as path;

import 'fixtures.dart';

class FolderRepository extends FixtureRepository {
  String? directory;
  final folderRequests = <List<String>>[];

  @override
  Future<String?> downloadedFolder(Iterable<DownloadJob> jobs) async {
    folderRequests.add(jobs.map((job) => job.id).toList());
    return directory;
  }

  @override
  Future<PlaybackPlan?> localPlayback(Drama drama, Episode episode) {
    throw StateError('Opening a folder must not invoke playback');
  }
}

DownloadJob folderJob(int number, {String state = 'completed'}) => DownloadJob(
  id: '$number',
  drama: FixtureRepository.free,
  episode: Episode({'currentEpisode': number}, number),
  state: state,
);

void main() {
  test(
    'one folder request for the collection; safely launches a directory URI',
    () async {
      final directory = path.join(Directory.systemTemp.path, '剧集 有空格 & #100%');
      final repository = FolderRepository()..directory = directory;
      Uri? launched;
      final opener = DownloadFolderOpener(
        desktopSupported: true,
        launch: (uri) async {
          launched = uri;
          return true;
        },
      );
      final result = await opener.resolve(repository, [
        folderJob(1),
        folderJob(2, state: 'queued'),
        folderJob(3),
      ]);
      await opener.open(result);
      expect(repository.folderRequests, [
        ['1', '3'],
      ]);
      expect(launched!.scheme, 'file');
      expect(launched!.hasFragment, isFalse);
      expect(
        path.normalize(launched!.toFilePath(windows: Platform.isWindows)),
        path.normalize(directory),
      );
    },
  );

  test(
    'no completed jobs do not call native; missing and remote results fail',
    () async {
      final repository = FolderRepository();
      const opener = DownloadFolderOpener(desktopSupported: true);
      await expectLater(
        opener.resolve(repository, [folderJob(1, state: 'queued')]),
        throwsA(isA<AppFailure>()),
      );
      expect(repository.folderRequests, isEmpty);
      for (final directory in [
        null,
        'https://fixture.invalid/video',
        'relative-folder',
      ]) {
        repository.directory = directory;
        await expectLater(
          opener.resolve(repository, [folderJob(1)]),
          throwsA(isA<AppFailure>()),
        );
      }
      expect(repository.folderRequests, [
        ['1'],
        ['1'],
        ['1'],
      ]);
    },
  );

  test(
    'unsupported platform and launcher failure report actionable errors',
    () async {
      final repository = FolderRepository();
      await expectLater(
        const DownloadFolderOpener(desktopSupported: false)
            .resolve(repository, [folderJob(1)]),
        throwsA(isA<AppFailure>()),
      );
      expect(repository.folderRequests, isEmpty);
      final opener = DownloadFolderOpener(
        desktopSupported: true,
        launch: (_) async => false,
      );
      await expectLater(
        opener.open(Directory.systemTemp.path),
        throwsA(isA<AppFailure>()),
      );
    },
  );
}
