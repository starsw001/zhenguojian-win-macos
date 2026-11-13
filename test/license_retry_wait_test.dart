import 'package:duanju_app/app_license.dart';
import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';

void main() {
  for (final waitSeconds in [60, 180]) {
    testWidgets(
      'activation wait $waitSeconds counts down without resubmitting',
      (tester) async {
        var now = DateTime.utc(2026, 11, 5, 12);
        DateTime? retryAt;
        var attempts = 0;
        final submitted = <String>[];
        final controller = AppLicenseController(
          enabled: true,
          now: () => now,
          request:
              (
                action, {
                required username,
                required password,
                required key,
              }) async {
                if (action == 'licenseActivate') {
                  attempts++;
                  submitted.add(key);
                  if (attempts == 2) return {'phase': 'ready', 'ready': true};
                  retryAt = now.add(Duration(seconds: waitSeconds));
                }
                final remaining = retryAt?.difference(now).inSeconds ?? 0;
                return {
                  'phase': 'activation',
                  'ready': false,
                  'message': retryAt == null ? '请输入许可证' : '此许可证不适用于当前软件',
                  if (retryAt != null) ...{
                    'retryNetworkAt': retryAt!.millisecondsSinceEpoch ~/ 1000,
                    'retryNetworkRemainingSeconds': remaining > 0
                        ? remaining
                        : 0,
                  },
                };
              },
        );
        await controller.open();
        await tester.pumpWidget(
          MaterialApp(home: AppLicenseScreen(controller: controller)),
        );
        await tester.enterText(find.byType(TextField), 'WRONG-EDITION');
        await tester.tap(find.text('联网激活当前电脑'));
        await tester.pump();
        expect(attempts, 1);
        expect(find.text('此许可证不适用于当前软件'), findsOneWidget);
        expect(
          find.textContaining('${waitSeconds ~/ 60} 分 00 秒后再次提交'),
          findsOneWidget,
        );
        expect(
          tester.widget<FilledButton>(find.byType(FilledButton)).onPressed,
          isNull,
        );
        expect(
          tester.widget<TextField>(find.byType(TextField)).controller!.text,
          'WRONG-EDITION',
        );
        await tester.enterText(find.byType(TextField), 'CORRECT-EDITION');
        await controller.perform('licenseActivate', key: 'CORRECT-EDITION');
        expect(attempts, 1);
        now = now.add(Duration(seconds: waitSeconds - 1));
        await tester.pump(Duration(seconds: waitSeconds - 1));
        await tester.pump();
        expect(find.textContaining('0 分 01 秒后再次提交'), findsOneWidget);
        expect(attempts, 1);
        now = now.add(const Duration(seconds: 1));
        await tester.pump(const Duration(seconds: 1));
        await tester.pump();
        expect(
          find.byKey(const ValueKey('license-retry-countdown')),
          findsNothing,
        );
        expect(
          tester.widget<FilledButton>(find.byType(FilledButton)).onPressed,
          isNotNull,
        );
        expect(attempts, 1);
        await tester.tap(find.text('联网激活当前电脑'));
        await tester.pump();
        expect(submitted, ['WRONG-EDITION', 'CORRECT-EDITION']);
        expect(controller.ready, isTrue);
        await tester.pumpWidget(const SizedBox());
        controller.dispose();
      },
    );
  }

  testWidgets('login respects longer login wait and does not send requests', (
    tester,
  ) async {
    var now = DateTime.utc(2026, 11, 5, 12);
    var requests = 0;
    final controller = AppLicenseController(
      enabled: true,
      now: () => now,
      request:
          (action, {required username, required password, required key}) async {
            requests++;
            return {
              'phase': 'login',
              'ready': false,
              'retryNetworkRemainingSeconds': 60,
              'retryLoginRemainingSeconds': 300,
            };
          },
    );
    await controller.open();
    expect(controller.retryRemainingSeconds, 300);
    await controller.perform(
      'licenseLogin',
      username: 'fixture1@example.com',
      password: 'Abc123@#',
    );
    expect(requests, 1);
    now = now.add(const Duration(microseconds: 500000));
    expect(controller.retryRemainingSeconds, 300);
    now = now.add(const Duration(microseconds: 500000));
    expect(controller.retryRemainingSeconds, 299);
    controller.dispose();
  });

  testWidgets('expired wait message updates without a request', (tester) async {
    var now = DateTime.utc(2026, 11, 5, 12);
    var requests = 0;
    final controller = AppLicenseController(
      enabled: true,
      now: () => now,
      request:
          (action, {required username, required password, required key}) async {
            requests++;
            return {
              'phase': 'verify',
              'ready': false,
              'message': '授权请求等待中，请在 0 分 01 秒后重试',
              'retryNetworkRemainingSeconds': 1,
            };
          },
    );
    await controller.open();
    await tester.pumpWidget(
      MaterialApp(home: AppLicenseScreen(controller: controller)),
    );
    expect(
      tester.widget<FilledButton>(find.byType(FilledButton)).onPressed,
      isNull,
    );
    now = now.add(const Duration(seconds: 1));
    await tester.pump(const Duration(seconds: 1));
    expect(find.text('等待已结束，可以再次提交授权请求。'), findsOneWidget);
    expect(
      tester.widget<FilledButton>(find.byType(FilledButton)).onPressed,
      isNotNull,
    );
    expect(requests, 1);
    await tester.pumpWidget(const SizedBox());
    controller.dispose();
  });
}
