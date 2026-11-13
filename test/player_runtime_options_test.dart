import 'package:duanju_app/player_runtime_options.dart';
import 'package:flutter_test/flutter_test.dart';

void main() {
  test('Windows playback uses memory without an implicit disk cache', () async {
    final applied = <String, String>{};
    await configurePortablePlaybackCache((name, value) async {
      applied[name] = value;
    }, windows: true);
    expect(applied, {'cache-on-disk': 'no'});
  });

  test('other platforms keep their existing cache policy', () async {
    final applied = <String, String>{};
    await configurePortablePlaybackCache((name, value) async {
      applied[name] = value;
    }, windows: false);
    expect(applied, isEmpty);
  });

  test('cache configuration errors are not silently ignored', () async {
    await expectLater(
      configurePortablePlaybackCache((name, value) async {
        throw StateError('cache setup failed');
      }, windows: true),
      throwsStateError,
    );
  });
}
