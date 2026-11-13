import 'dart:io';

import 'package:duanju_app/app_build.dart';
import 'package:duanju_app/first_start_check.dart';
import 'package:duanju_app/models.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:path/path.dart' as path;

void main() {
  test(
    'first-start checks accept only explicit empty absolute directories',
    () {
      final directory = Directory.systemTemp.createTempSync(
        'first-start-guard-',
      );
      addTearDown(() {
        expect(
          path.isWithin(
            Directory.systemTemp.resolveSymbolicLinksSync(),
            directory.resolveSymbolicLinksSync(),
          ),
          isTrue,
        );
        directory.deleteSync(recursive: true);
      });
      final request = parseFirstStartCheckArguments([
        '--first-start-check',
        path.join(directory.path, 'new'),
        'hongguo',
      ]);
      expect(request.directory.existsSync(), isFalse);
      expect(request.source, SourceSite.hongguo);
      expect(
        () => parseFirstStartCheckArguments([
          '--first-start-check',
          'relative',
          'hongguo',
        ]),
        throwsArgumentError,
      );
      final record = File(path.join(directory.path, 'existing-record.json'))
        ..writeAsStringSync('{"preserved":true}');
      expect(
        () => parseFirstStartCheckArguments([
          '--first-start-check',
          directory.path,
          'hongguo',
        ]),
        throwsArgumentError,
      );
      expect(record.readAsStringSync(), '{"preserved":true}');
    },
  );

  test('first-start checks enforce the compiled source list', () {
    final directory = Directory.systemTemp.createTempSync(
      'first-start-edition-',
    );
    addTearDown(() {
      expect(
        path.isWithin(
          Directory.systemTemp.resolveSymbolicLinksSync(),
          directory.resolveSymbolicLinksSync(),
        ),
        isTrue,
      );
      directory.deleteSync(recursive: true);
    });
    for (final source in SourceSite.values) {
      expect(
        parseFirstStartCheckArguments([
          '--first-start-check',
          directory.path,
          source.id,
        ]).source.id,
        source.id,
      );
    }
    for (final id in ['unknown', if (!allSourcesEnabled) 'huangdou']) {
      expect(
        () => parseFirstStartCheckArguments([
          '--first-start-check',
          directory.path,
          id,
        ]),
        throwsArgumentError,
      );
    }
    expect(() => parseFirstStartCheckArguments([]), throwsArgumentError);
    expect(directory.listSync(), isEmpty);
  });
}
