import 'package:duanju_app/program_storage.dart';
import 'package:duanju_app/resource_settings.dart';
import 'package:flutter_test/flutter_test.dart';

void main() {
  test(
    'Windows defaults follow actual executable without a developer path',
    () {
      expect(
        programStorageDefaults(
          windows: true,
          executable: r'E:\Apps\真果赏\zhenguoshang.exe',
        ),
        {
          'downloadDirectory': r'E:\Apps\真果赏\download',
          'defaultLogDirectory': r'E:\Apps\真果赏\logs',
        },
      );
      expect(
        programStorageDefaults(
          windows: true,
          executable: r'Z:\Moved\hongguoshang.exe',
        )['defaultLogDirectory'],
        r'Z:\Moved\logs',
      );
    },
  );
  test('isolated workspaces and non-Windows storage are not redirected', () {
    expect(
      programStorageDefaults(
        windows: true,
        executable: r'E:\Apps\app.exe',
        isolated: true,
      ),
      isEmpty,
    );
    expect(
      programStorageDefaults(windows: false, executable: '/app/program'),
      isEmpty,
    );
    expect(
      () => programStorageDefaults(windows: true, executable: 'relative.exe'),
      throwsArgumentError,
    );
  });
  test(
    'selected log location roundtrips without persisting computed paths',
    () {
      final settings = ResourceSettings.fromJson({
        'logDirectory': r'F:\User Logs',
        'logPath': r'F:\User Logs\app.log',
        'defaultLogDirectory': r'E:\App\logs',
      });
      expect(settings.toJson()['logDirectory'], r'F:\User Logs');
      expect(settings.logPath, r'F:\User Logs\app.log');
      expect(settings.toJson().containsKey('logPath'), isFalse);
      expect(settings.toJson().containsKey('defaultLogDirectory'), isFalse);
      expect(const ResourceSettings().toJson()['logDirectory'], '');
    },
  );
}
