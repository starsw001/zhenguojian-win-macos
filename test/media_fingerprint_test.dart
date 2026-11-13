import 'dart:io';

import 'package:crypto/crypto.dart';
import 'package:duanju_app/media_fingerprint.dart';
import 'package:flutter_test/flutter_test.dart';

void main() {
  test(
    'media fingerprint matches SHA256 without changing content or timestamps',
    () async {
      final directory = await Directory.systemTemp.createTemp(
        'media-fingerprint-',
      );
      addTearDown(() => directory.delete(recursive: true));
      final file = File('${directory.path}/独立分集 [普通目录].mp4');
      final bytes = List<int>.generate(
        2 * 1024 * 1024 + 23,
        (index) => index % 251,
      );
      await file.writeAsBytes(bytes);
      final before = await file.stat();
      expect(await mediaFileFingerprint(file), sha256.convert(bytes));
      final after = await file.stat();
      expect(after.modified, before.modified);
      expect(after.mode, before.mode);
      expect(await file.readAsBytes(), bytes);
      await file.delete();
      expect(await file.exists(), isFalse);
    },
  );

  test('empty media files have the standard empty SHA256', () async {
    final directory = await Directory.systemTemp.createTemp(
      'empty-media-fingerprint-',
    );
    addTearDown(() => directory.delete(recursive: true));
    final file = File('${directory.path}/empty.mp4');
    await file.writeAsBytes([]);
    expect(await mediaFileFingerprint(file), sha256.convert([]));
  });

  test('missing or invalid media paths fail without creating files', () async {
    final directory = await Directory.systemTemp.createTemp(
      'missing-media-fingerprint-',
    );
    addTearDown(() => directory.delete(recursive: true));
    final file = File('${directory.path}/missing.mp4');
    await expectLater(
      mediaFileFingerprint(file),
      throwsA(isA<FileSystemException>()),
    );
    expect(await directory.list().isEmpty, isTrue);
  });
}
