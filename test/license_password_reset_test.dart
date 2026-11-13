import 'package:duanju_app/app_license.dart';
import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';

void main() {
  testWidgets(
    'password reset removes ready UI and requires new explicit login',
    (tester) async {
      var reset = false;
      final now = DateTime.utc(2026, 11, 6);
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
              if (action == 'licenseLogin' && password == 'new-secret') {
                reset = false;
              }
              return reset
                  ? {
                      'phase': 'login',
                      'ready': false,
                      'message': '账号密码已被重置，已退出。请重新输入用户名和新密码。',
                    }
                  : {
                      'phase': 'ready',
                      'ready': true,
                      'username': 'fixture1@fixture.com',
                      'licenseKey': 'ABCD-EFGH-JKLM-NPQR',
                      'expiresAt':
                          now
                              .add(const Duration(days: 20))
                              .millisecondsSinceEpoch ~/
                          1000,
                      'refreshAt':
                          now
                              .add(const Duration(days: 1))
                              .millisecondsSinceEpoch ~/
                          1000,
                    };
            },
      );
      await tester.pumpWidget(
        LicensedApp(
          controller: controller,
          child: const MaterialApp(home: Text('protected-content')),
        ),
      );
      await tester.pump();
      expect(find.text('protected-content'), findsOneWidget);
      reset = true;
      await controller.perform('licenseStatus');
      await tester.pump();
      expect(controller.ready, isFalse);
      expect(controller.licenseKey, isEmpty);
      expect(find.text('protected-content'), findsNothing);
      expect(find.textContaining('密码已被重置'), findsOneWidget);
      expect(find.byType(TextField), findsNWidgets(2));
      await controller.perform(
        'licenseLogin',
        username: 'fixture1@fixture.com',
        password: 'old-secret',
      );
      expect(controller.ready, isFalse);
      await controller.perform(
        'licenseLogin',
        username: 'fixture1@fixture.com',
        password: 'new-secret',
      );
      await tester.pump();
      expect(controller.ready, isTrue);
      expect(find.text('protected-content'), findsOneWidget);
      await tester.pumpWidget(const SizedBox());
    },
  );
}
