import 'dart:convert';
import 'dart:io';

import 'package:duanju_app/local_store.dart';
import 'package:duanju_app/media_library.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:path/path.dart' as path;
import 'package:shared_preferences/shared_preferences.dart';

import 'local_media_actions_test.dart' as f;

Future<(Directory, MediaLibrary, LocalMediaItem, f.ActionExecutor)>
emptyFolderFixture() async {
  SharedPreferences.setMockInitialValues({});
  final store = LocalStore(await SharedPreferences.getInstance());
  final directory = await Directory.systemTemp.createTemp(
    'media-empty-folder-',
  );
  final job = f.actionJob(1), item = f.actionItem(f.actionJob(1));
  final file = File(path.join(directory.path, item.file));
  await file.parent.create(recursive: true);
  await file.writeAsString('test');
  await File(path.join(directory.path, 'media-library.json')).writeAsString(
    jsonEncode({
      'items': [item.toJson()],
    }),
  );
  final executor = f.ActionExecutor();
  final library = MediaLibrary(
    f.ActionRepository(directory, [job]),
    store,
    executor: executor,
    automaticWorker: true,
  );
  addTearDown(() async {
    library.dispose();
    store.dispose();
    if (await directory.exists()) await directory.delete(recursive: true);
  });
  await library.reload();
  return (directory, library, item, executor);
}

void main() {
  TestWidgetsFlutterBinding.ensureInitialized();

  test(
    'deleting last export removes show directory but keeps exports root',
    () async {
      final (directory, library, item, _) = await emptyFolderFixture();
      final file = File(library.fileFor(item));
      await library.remove(item);
      expect(await file.exists(), isFalse);
      expect(await file.parent.exists(), isFalse);
      expect(
        await Directory(path.join(directory.path, 'exports')).exists(),
        isTrue,
      );
      expect(library.items, isEmpty);
    },
  );

  for (final remaining in ['S01E002.mp4', 'notes.txt', '.unfinished.part']) {
    test(
      'deleting an episode retains directory containing $remaining',
      () async {
        final (_, library, item, _) = await emptyFolderFixture();
        final file = File(library.fileFor(item));
        final keep = File(path.join(file.parent.path, remaining));
        await keep.writeAsString('keep');
        await library.remove(item);
        expect(await file.exists(), isFalse);
        expect(await keep.readAsString(), 'keep');
        expect(await keep.parent.exists(), isTrue);
      },
    );
  }

  test(
    'manual deletion is cleaned without redownloading or removing history',
    () async {
      final (directory, library, item, executor) = await emptyFolderFixture();
      final file = File(library.fileFor(item));
      await file.delete();
      final index = File(path.join(directory.path, 'media-library.json'));
      final before = await index.readAsBytes();
      await library.maybeExport();
      expect(await file.parent.exists(), isFalse);
      expect(library.items, hasLength(1));
      expect(await index.readAsBytes(), before);
      expect(executor.commands, isEmpty);
    },
  );

  test('explicit removal cleans an already missing file directory', () async {
    final (_, library, item, _) = await emptyFolderFixture();
    final file = File(library.fileFor(item));
    await file.delete();
    await library.remove(item);
    expect(await file.parent.exists(), isFalse);
  });

  test('empty legacy season and show parents are both removed', () async {
    final (directory, library, item, _) = await emptyFolderFixture();
    final old = File(library.fileFor(item));
    final nested = path.join(
      path.dirname(item.file),
      'Season 01',
      path.basename(item.file),
    );
    final selected = LocalMediaItem.fromJson({
      ...item.toJson(),
      'file': nested,
    });
    final file = File(path.join(directory.path, nested));
    await file.parent.create();
    await old.rename(file.path);
    await File(path.join(directory.path, 'media-library.json')).writeAsString(
      jsonEncode({
        'items': [selected.toJson()],
      }),
    );
    await library.reload();
    await library.remove(selected);
    expect(await file.parent.exists(), isFalse);
    expect(await file.parent.parent.exists(), isFalse);
    expect(
      await Directory(path.join(directory.path, 'exports')).exists(),
      isTrue,
    );
  });

  test('linked show directory cannot delete an outside video', () async {
    final (_, library, item, _) = await emptyFolderFixture();
    final file = File(library.fileFor(item));
    await file.delete();
    await file.parent.delete();
    final outside = await Directory.systemTemp.createTemp('media-outside-');
    addTearDown(() => outside.delete(recursive: true));
    final keep = File(path.join(outside.path, path.basename(file.path)));
    await keep.writeAsString('outside');
    try {
      await Link(file.parent.path).create(outside.path);
    } on FileSystemException {
      markTestSkipped('directory link creation is not available');
      return;
    }
    await expectLater(library.remove(item), throwsA(isA<Exception>()));
    expect(await keep.readAsString(), 'outside');
  });
}
