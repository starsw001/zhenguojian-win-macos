import 'dart:async';
import 'dart:convert';
import 'dart:io';

import 'package:duanju_app/local_store.dart';
import 'package:duanju_app/media_library.dart';
import 'package:duanju_app/media_pipeline.dart';
import 'package:duanju_app/models.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:path/path.dart' as path;
import 'package:shared_preferences/shared_preferences.dart';

import 'fixtures.dart';

class ProcessMediaExecutor implements MediaExecutor {
  final commands = <List<String>>[];
  Process? _process;

  @override
  Future<MediaProbe> probe(String file) async {
    final result = await Process.run('ffprobe', [
      '-v',
      'error',
      '-show_format',
      '-show_streams',
      '-show_data_hash',
      'sha256',
      '-of',
      'json',
      file,
    ]);
    if (result.exitCode != 0) throw StateError(result.stderr.toString());
    return MediaProbe(
      jsonDecode(result.stdout as String) as Map<String, dynamic>,
    );
  }

  @override
  Future<void> run(
    List<String> arguments, {
    double duration = 0,
    void Function(double)? progress,
  }) async {
    commands.add(List.from(arguments));
    final process = await Process.start('ffmpeg', [
      '-v',
      'error',
      '-y',
      '-protocol_whitelist',
      'file,crypto,data',
      ...arguments,
    ]);
    _process = process;
    final stdout = utf8.decoder.bind(process.stdout).join();
    final stderr = utf8.decoder.bind(process.stderr).join();
    final code = await process.exitCode;
    await stdout;
    final errors = await stderr;
    _process = null;
    if (code != 0) throw StateError(errors);
    progress?.call(1);
  }

  @override
  Future<void> cancel() async {
    _process?.kill();
  }
}

class FileRepository extends FixtureRepository {
  FileRepository(this.root, this.files, this.jobs);
  final String root;
  final List<String> files;
  final List<DownloadJob> jobs;
  bool leased = false;
  void Function()? onLocalPlayback;
  @override
  Future<String> downloadDirectory() async => root;
  @override
  Future<int> workLease(String id, String command) async {
    if (command == 'start') {
      if (leased) throw StateError('leased');
      leased = true;
    } else if (command == 'end') {
      leased = false;
    }
    return leased ? 1 : 0;
  }

  @override
  Future<List<DownloadJob>> downloads() async => jobs;
  @override
  Future<PlaybackPlan?> localPlayback(Drama drama, Episode episode) async {
    onLocalPlayback?.call();
    return PlaybackPlan(
      url: files[episode.number - 1],
      local: true,
      decryptionKey: episode.number == 3
          ? '00112233445566778899aabbccddeeff'
          : '',
    );
  }
}

MediaProbe fixtureProbe({
  String codec = 'h264',
  int width = 320,
  String audio = 'aac',
  int rate = 44100,
}) => MediaProbe({
  'format': {'duration': '4'},
  'streams': [
    {
      'codec_type': 'video',
      'codec_name': codec,
      'width': width,
      'height': 180,
      'pix_fmt': 'yuv420p',
      'sample_aspect_ratio': '1:1',
    },
    if (audio.isNotEmpty)
      {
        'codec_type': 'audio',
        'codec_name': audio,
        'sample_rate': '$rate',
        'channels': 2,
        'channel_layout': 'stereo',
      },
  ],
});

class RecordingExportExecutor implements MediaExecutor {
  final commands = <List<String>>[];

  @override
  Future<void> run(
    List<String> arguments, {
    double duration = 0,
    void Function(double)? progress,
  }) async {
    commands.add(List.from(arguments));
    await File(arguments.last).writeAsString('synthetic-mp4-fixture');
  }

  @override
  Future<MediaProbe> probe(String file) async => fixtureProbe();

  @override
  Future<void> cancel() async {}
}

void main() {
  TestWidgetsFlutterBinding.ensureInitialized();
  test('exports independent MP4 episodes without a series merge', () async {
    final directory = await Directory.systemTemp.createTemp('zgj-mp4-');
    SharedPreferences.setMockInitialValues({});
    final store = LocalStore(await SharedPreferences.getInstance());
    const drama = Drama(id: 'hongguo:mp4', source: 'hongguo', title: 'MP4 测试');
    final jobs = [
      for (var i = 1; i <= 2; i++)
        DownloadJob(
          id: 'mp4-$i',
          drama: drama,
          episode: Episode({'id': '$i', 'currentEpisode': i}, i),
          state: 'completed',
          created: i,
        ),
    ];
    final repository = FileRepository(directory.path, [
      'one.mp4',
      'two.mp4',
    ], jobs);
    final executor = RecordingExportExecutor();
    final library = MediaLibrary(repository, store, executor: executor);
    try {
      await library.exportJobs(jobs);
      expect(library.items, hasLength(2));
      expect(
        library.items.every(
          (item) => item.kind == 'export' && item.episodes.length == 1,
        ),
        isTrue,
      );
      expect(library.items.map((item) => path.basename(item.file)).toSet(), {
        'S01E001.mp4',
        'S01E002.mp4',
      });
      expect(
        library.items.every(
          (item) =>
              path.split(item.file).length == 3 &&
              !item.file.contains('Season'),
        ),
        isTrue,
      );
      expect(
        directory
            .listSync(recursive: true)
            .whereType<File>()
            .where((file) => file.path.endsWith('.nfo')),
        isEmpty,
      );
      expect(executor.commands, hasLength(2));
      expect(
        executor.commands.every(
          (arguments) =>
              arguments.contains('+faststart') &&
              arguments.last.endsWith('.mp4'),
        ),
        isTrue,
      );
      expect(
        executor.commands.any((arguments) => arguments.contains('concat')),
        isFalse,
      );
      await library.exportJobs(jobs);
      expect(executor.commands, hasLength(2));
      final selected = library.items.first;
      final flatFile = File(library.fileFor(selected));
      final season = Directory(path.join(flatFile.parent.path, 'Season 01'));
      await season.create();
      final legacyFile = await flatFile.rename(
        path.join(season.path, path.basename(flatFile.path)),
      );
      await File(
        path.setExtension(legacyFile.path, '.nfo'),
      ).writeAsString('legacy episode');
      await File(
        path.join(season.parent.path, 'tvshow.nfo'),
      ).writeAsString('legacy show');
      final index = File(path.join(directory.path, 'media-library.json'));
      final saved =
          jsonDecode(await index.readAsString()) as Map<String, dynamic>;
      for (final row in saved['items'] as List) {
        if (row['id'] == selected.id) {
          row['file'] = path.relative(legacyFile.path, from: directory.path);
        }
      }
      await index.writeAsString(jsonEncode(saved));
      await library.exportJobs(jobs);
      expect(executor.commands, hasLength(2));
      expect(await flatFile.readAsString(), 'synthetic-mp4-fixture');
      expect(await season.exists(), isFalse);
      final removed = library.items.first;
      final jobIndex = jobs.indexWhere((job) => job.id == removed.jobId);
      final oldJob = jobs[jobIndex];
      await library.remove(removed);
      await library.maybeExport();
      expect(executor.commands, hasLength(2));
      expect(await File(library.fileFor(removed)).exists(), isFalse);
      jobs[jobIndex] = DownloadJob(
        id: oldJob.id,
        drama: oldJob.drama,
        episode: oldJob.episode,
        state: 'completed',
        created: oldJob.created,
        revision: 1,
      );
      await library.maybeExport();
      expect(executor.commands, hasLength(3));
      expect(
        library.items.any(
          (item) =>
              item.jobId == removed.jobId && item.sourceVersion.endsWith('-1'),
        ),
        isTrue,
      );
      expect(
        directory
            .listSync(recursive: true)
            .whereType<File>()
            .where((f) => f.path.endsWith('.nfo')),
        isEmpty,
      );
    } finally {
      library.dispose();
      store.dispose();
      await directory.delete(recursive: true);
    }
  });
  test(
    'merge retains every matching video and normalizes only the minority',
    () {
      final plan = MergePlan.create([
        fixtureProbe(),
        fixtureProbe(),
        fixtureProbe(codec: 'hevc', width: 640),
      ]);
      expect(plan.videoTranscodes, 1);
      expect(plan.audioTranscodes, 0);
      expect(plan.video.videoCodec, 'h264');
      final audioOnly = MergePlan.create([
        fixtureProbe(),
        fixtureProbe(),
        fixtureProbe(rate: 48000),
      ]);
      expect(audioOnly.videoTranscodes, 0);
      expect(audioOnly.audioTranscodes, 3);
      final identical = MergePlan.create([fixtureProbe(), fixtureProbe()]);
      expect(identical.videoTranscodes, 0);
      expect(identical.audioTranscodes, 0);
    },
  );

  test('missing audio gets silence without discarding existing sound', () {
    final plan = MergePlan.create([
      fixtureProbe(audio: ''),
      fixtureProbe(audio: ''),
      fixtureProbe(),
    ]);
    expect(plan.audio, isNotNull);
    expect(plan.videoTranscodes, 0);
    expect(plan.audioTranscodes, 3);
  });

  test(
    'NFO escapes metadata and includes an HTTP poster URL without fetching it',
    () {
      const drama = Drama(
        id: 'hongguo:1',
        source: 'hongguo',
        title: '甲 & <乙>',
        cover: 'https://example.invalid/poster.jpg?a=1&b=2',
      );
      final body = embyShowNfo(drama);
      expect(body, contains('甲 &amp; &lt;乙&gt;'));
      expect(body, contains('https://example.invalid/poster.jpg?a=1&amp;b=2'));
      expect(body, contains('<thumb aspect="poster">'));
      expect(
        embyShowNfo(drama, localPoster: true),
        contains('>poster.jpg</thumb>'),
      );
      expect(
        embyShowNfo(
          const Drama(
            id: '1',
            source: 'hongguo',
            title: 'x',
            cover: 'file:///secret',
          ),
        ),
        isNot(contains('<thumb')),
      );
      expect(
        () => LocalMediaItem.fromJson({'file': '../elsewhere'}),
        throwsFormatException,
      );
    },
  );

  test(
    'cancelling while resolving local media releases the lease without starting FFmpeg',
    () async {
      final directory = await Directory.systemTemp.createTemp('zgj-cancel-');
      SharedPreferences.setMockInitialValues({});
      final store = LocalStore(await SharedPreferences.getInstance());
      final executor = ProcessMediaExecutor();
      const drama = Drama(
        id: 'hongguo:cancel',
        source: 'hongguo',
        title: '取消测试',
      );
      final jobs = [
        for (var i = 0; i < 2; i++)
          DownloadJob(
            id: 'cancel-$i',
            drama: drama,
            episode: Episode({'id': '$i', 'currentEpisode': i + 1}, i + 1),
            state: 'completed',
            created: i + 1,
          ),
      ];
      final repository = FileRepository(directory.path, [
        'unused-1',
        'unused-2',
      ], jobs);
      final library = MediaLibrary(repository, store, executor: executor);
      repository.onLocalPlayback = () => unawaited(library.cancel());
      try {
        await expectLater(
          library.merge(jobs),
          throwsA(
            predicate((Object error) => error.toString().contains('已停止')),
          ),
        );
        expect(executor.commands, isEmpty);
        expect(repository.leased, isFalse);
        expect(library.busy, isFalse);
        expect(directory.listSync(), isEmpty);
      } finally {
        library.dispose();
        store.dispose();
        await directory.delete(recursive: true);
      }
    },
  );

  bool available;
  try {
    available = Process.runSync('ffmpeg', ['-version']).exitCode == 0;
  } catch (_) {
    available = false;
  }

  test(
    'real offline merge, CENC remux and Emby export remain playable after deleting inputs',
    () async {
      final directory = await Directory.systemTemp.createTemp("真果赏 '合成-");
      SharedPreferences.setMockInitialValues({});
      final store = LocalStore(await SharedPreferences.getInstance());
      final executor = ProcessMediaExecutor();
      MediaLibrary? library;
      try {
        final files = <String>[];
        for (var i = 0; i < 3; i++) {
          final file = path.join(directory.path, 'input-$i.mp4');
          final result = await Process.run('ffmpeg', [
            '-v',
            'error',
            '-y',
            '-f',
            'lavfi',
            '-i',
            'testsrc2=size=${i == 2 ? '320x180' : '160x90'}:rate=12',
            '-f',
            'lavfi',
            '-i',
            'sine=frequency=440:sample_rate=44100',
            '-t',
            '2',
            '-c:v',
            'libx264',
            '-threads',
            '1',
            '-g',
            '12',
            '-pix_fmt',
            'yuv420p',
            '-c:a',
            'aac',
            '-ac',
            '2',
            if (i == 2) ...[
              '-encryption_scheme',
              'cenc-aes-ctr',
              '-encryption_key',
              '00112233445566778899aabbccddeeff',
              '-encryption_kid',
              '11223344556677889900aabbccddeeff',
            ],
            file,
          ]);
          expect(result.exitCode, 0, reason: result.stderr.toString());
          files.add(file);
        }
        const drama = Drama(
          id: 'hongguo:100',
          source: 'hongguo',
          title: '合成 <&> 测试',
          episodes: 3,
          cover: 'https://example.invalid/poster.jpg',
        );
        final jobs = [
          for (var i = 0; i < 3; i++)
            DownloadJob(
              id: 'job$i',
              drama: drama,
              episode: Episode({'id': '$i', 'currentEpisode': i + 1}, i + 1),
              state: 'completed',
              created: i + 1,
            ),
        ];
        final repository = FileRepository(directory.path, files, jobs);
        library = MediaLibrary(repository, store, executor: executor);
        final merged = await library.merge(jobs);
        expect(merged.videoTranscodes, 1);
        expect(merged.audioTranscodes, 0);
        expect(repository.leased, isFalse);
        expect(merged.episodes, [1, 2, 3]);
        await library.exportJobs(jobs);
        final exported = library.items.where((i) => !i.merged).toList();
        expect(exported, hasLength(3));
        expect(exported.every((item) => item.file.endsWith('.mp4')), isTrue);
        final show = File(library.fileFor(exported.first)).parent;
        final commandsBefore = executor.commands.length;
        await library.exportJobs(jobs);
        expect(
          executor.commands.length,
          commandsBefore,
          reason: 'unchanged exports should not be remuxed again',
        );
        expect(
          await File(path.join(show.path, 'tvshow.nfo')).exists(),
          isFalse,
        );
        expect(
          show.listSync().every(
            (file) => file is File && file.path.endsWith('.mp4'),
          ),
          isTrue,
        );
        for (final file in files) {
          await File(file).delete();
        }
        for (final item in library.items) {
          final decoded = await Process.run('ffmpeg', [
            '-v',
            'error',
            '-xerror',
            '-protocol_whitelist',
            'file,crypto,data',
            '-i',
            library.fileFor(item),
            '-f',
            'null',
            '-',
          ]);
          expect(
            decoded.exitCode,
            0,
            reason: '${item.file}: ${decoded.stderr}',
          );
          verifyMediaDuration(
            await executor.probe(library.fileFor(item)),
            item.merged ? 6 : 2,
          );
        }
        expect(
          directory.listSync().where(
            (entry) => path.basename(entry.path).startsWith('.media-work-'),
          ),
          isEmpty,
        );
        await library.remove(exported.first);
        expect(await File(library.fileFor(exported.first)).exists(), isFalse);
        expect(await File(library.fileFor(merged)).exists(), isTrue);
        await library.reload();
        expect(library.items, hasLength(3));
      } finally {
        library?.dispose();
        store.dispose();
        await directory.delete(recursive: true);
      }
    },
    skip: available ? false : 'requires ffmpeg and ffprobe',
    timeout: const Timeout(Duration(minutes: 3)),
  );
}
