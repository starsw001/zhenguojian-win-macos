import 'dart:convert';
import 'dart:io';

import 'package:integration_test/integration_test_driver_extended.dart';

Future<void> main() async {
  final output = Directory('build/device-test/results');
  await output.create(recursive: true);
  await integrationDriver(
    onScreenshot: (name, bytes, [args]) async {
      await File('${output.path}/$name.png').writeAsBytes(bytes);
      return true;
    },
    writeResponseOnFailure: true,
    responseDataCallback: (data) async {
      final report = Map<String, dynamic>.from(data ?? {})
        ..remove('screenshots');
      final windows = report['windowsFlow'];
      final real = report['windowsRealSources'];
      final downloads = report['windowsRealDownloads'];
      final name = downloads is Map
          ? 'windows-real-downloads'
          : real is Map
          ? 'windows-real-sources-${real['networkMode']}'
          : windows is Map
          ? 'windows-${windows['allSources'] == true ? 'zhenguoshang' : 'hongguoshang'}'
          : 'playback';
      await File(
        '${output.path}/$name.json',
      ).writeAsString(const JsonEncoder.withIndent('  ').convert(report));
    },
  );
}
