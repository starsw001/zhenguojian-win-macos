import 'package:duanju_app/app_about.dart';
import 'package:duanju_app/app_license.dart';
import 'package:duanju_app/app_build.dart';
import 'package:duanju_app/app_version.g.dart';
import 'package:duanju_app/local_store.dart';
import 'package:duanju_app/settings_screen.dart';
import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:shared_preferences/shared_preferences.dart';
import 'fixtures.dart';

void main() {
  testWidgets('license key visibility toggles without changing entered key', (
    tester,
  ) async {
    final controller = AppLicenseController(
      enabled: true,
      request:
          (
            action, {
            required username,
            required password,
            required key,
          }) async => {'phase': 'activation', 'ready': false},
    );
    await controller.open();
    await tester.pumpWidget(
      MaterialApp(home: AppLicenseScreen(controller: controller)),
    );
    final input = find.byType(TextField);
    await tester.enterText(input, 'AAAA-BBBB-CCCC-DDDD');
    expect(tester.widget<TextField>(input).obscureText, isTrue);
    await tester.tap(find.byKey(const ValueKey('license-key-visibility')));
    await tester.pump();
    expect(tester.widget<TextField>(input).obscureText, isFalse);
    expect(
      tester.widget<TextField>(input).controller!.text,
      'AAAA-BBBB-CCCC-DDDD',
    );
    await tester.tap(find.byTooltip('隐藏许可证'));
    await tester.pump();
    expect(tester.widget<TextField>(input).obscureText, isTrue);
    expect(find.text(appVersion), findsNothing);
    await tester.pumpWidget(const SizedBox());
    controller.dispose();
  });
  testWidgets(
    'about exposes only check for updates, keeps the version and license viewer hidden',
    (tester) async {
      await tester.pumpWidget(
        MaterialApp(home: AppAboutDialog(repository: FixtureRepository())),
      );
      expect(find.text(appName), findsOneWidget);
      expect(find.text('关闭'), findsOneWidget);
      expect(find.text(appVersion), findsNothing);
      expect(find.text('检查更新'), findsOneWidget);
      expect(find.text('查看许可'), findsNothing);
      expect(find.textContaining('仅供个人学习'), findsNothing);
      expect(find.textContaining('版权归各权利人所有'), findsOneWidget);
    },
  );
  testWidgets(
    'settings hide diagnostics entry without deleting its implementation',
    (tester) async {
      SharedPreferences.setMockInitialValues({});
      final store = LocalStore(await SharedPreferences.getInstance());
      await tester.pumpWidget(
        MaterialApp(
          home: SettingsScreen(repository: FixtureRepository(), store: store),
        ),
      );
      expect(find.byKey(const ValueKey('diary-setting')), findsNothing);
      expect(appMaintenanceEntriesVisible, isFalse);
      await tester.pumpWidget(const SizedBox());
      store.dispose();
    },
  );
}
