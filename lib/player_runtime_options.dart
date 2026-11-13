import 'dart:io';

Future<void> setPlaybackProperty(
  Future<void> Function(List<String>) command,
  String property,
  String value,
) => command(['set', property, value]);

Future<void> configurePortablePlaybackCache(
  Future<void> Function(String, String) setProperty, {
  bool? windows,
  int? totalMiB,
}) async {
  if (totalMiB != null && (totalMiB < 32 || totalMiB > 256)) {
    throw ArgumentError.value(totalMiB, 'totalMiB', '范围应为32–256 MiB');
  }
  if (windows ?? Platform.isWindows) {
    await setProperty('cache-on-disk', 'no');
  }
  if (totalMiB != null) {
    final bytes = totalMiB * 1024 * 1024;
    final backward = bytes ~/ 4;
    await setProperty('demuxer-max-bytes', '${bytes - backward}');
    await setProperty('demuxer-max-back-bytes', '$backward');
  }
}
