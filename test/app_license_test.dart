import 'dart:async';

import 'package:duanju_app/app_license.dart';
import 'package:duanju_app/local_store.dart';
import 'package:duanju_app/profiles_screen.dart';
import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:shared_preferences/shared_preferences.dart';

void main() {
  testWidgets(
    'network diagnostics never submit credentials or unlock the app',
    (tester) async {
      final requests = <String>[];
      final controller = AppLicenseController(
        enabled: true,
        request:
            (
              action, {
              required username,
              required password,
              required key,
            }) async {
              requests.add(action);
              if (action == 'licenseNetworkCheck') {
                expect([username, password, key], ['', '', '']);
                return {'reachable': true, 'message': '检测正常，未登录'};
              }
              return {
                'phase': 'login',
                'ready': false,
                'message': '连接超时 [AUTH_TIMEOUT]',
              };
            },
      );
      await tester.pumpWidget(
        LicensedApp(controller: controller, child: const Text('主界面')),
      );
      await tester.pump();
      await tester.tap(find.text('检测授权连接'));
      await tester.pump();
      expect(find.text('检测正常，未登录'), findsOneWidget);
      expect(requests, ['licenseStatus', 'licenseNetworkCheck']);
      expect(find.text('主界面'), findsNothing);
      expect(find.text('登录'), findsOneWidget);
    },
  );

  testWidgets(
    'remembered native session opens directly and retains countdown',
    (tester) async {
      var now = DateTime.utc(2026, 11, 5, 12);
      final expiry = now.add(const Duration(days: 2));
      final requests = <String>[];
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
              requests.add(action);
              expect(username, isEmpty);
              expect(password, isEmpty);
              expect(key, isEmpty);
              return {
                'phase': 'ready',
                'ready': true,
                'username': 'remembered@fixture.com',
                'expiresAt': expiry.millisecondsSinceEpoch ~/ 1000,
                'refreshAt':
                    now.add(const Duration(days: 1)).millisecondsSinceEpoch ~/
                    1000,
                'remainingSeconds': expiry.difference(now).inSeconds,
              };
            },
      );
      await tester.pumpWidget(
        LicensedApp(
          controller: controller,
          child: MaterialApp(
            home: Scaffold(body: LicenseAccountBadge(controller: controller)),
          ),
        ),
      );
      await tester.pump();
      expect(requests, ['licenseStatus']);
      expect(find.byType(TextField), findsNothing);
      expect(find.text('登录'), findsNothing);
      expect(find.text('remembered@fixture.com'), findsOneWidget);
      expect(find.text('即将到期 2天 00:00:00'), findsOneWidget);
      now = now.add(const Duration(seconds: 1));
      await tester.pump(const Duration(seconds: 1));
      expect(find.text('即将到期 1天 23:59:59'), findsOneWidget);
      expect(requests, ['licenseStatus']);
      await tester.pumpWidget(const SizedBox());
    },
  );

  testWidgets('uncached login stays gated until required online verification', (
    tester,
  ) async {
    final verification = Completer<Map<String, dynamic>>();
    final requests = <String>[];
    final controller = AppLicenseController(
      enabled: true,
      request:
          (action, {required username, required password, required key}) async {
            requests.add(action);
            if (action == 'licenseLogin') {
              return {'phase': 'verify', 'ready': false};
            }
            if (action == 'licenseVerify') {
              return {'phase': 'ready', 'ready': true};
            }
            if (requests.length > 1) return verification.future;
            return {'phase': 'login', 'ready': false};
          },
    );
    await tester.pumpWidget(
      LicensedApp(
        controller: controller,
        child: const MaterialApp(home: Scaffold(body: Text('应用主界面'))),
      ),
    );
    await tester.pump();
    await tester.enterText(
      find.byType(TextField).at(0),
      'fixture@example.invalid',
    );
    await tester.enterText(find.byType(TextField).at(1), 'Neutral#1');
    await tester.tap(find.text('登录'));
    await tester.pump();
    expect(requests, ['licenseStatus', 'licenseLogin', 'licenseStatus']);
    expect(find.text('应用主界面'), findsNothing);
    verification.complete({
      'phase': 'revoked',
      'ready': false,
      'message': '许可证已停用或设备授权已解除，请联系管理员',
    });
    await tester.pump();
    expect(find.text('应用主界面'), findsNothing);
    expect(find.text('许可证已停用或设备授权已解除，请联系管理员'), findsOneWidget);
    expect(find.text('联网激活当前电脑'), findsOneWidget);
    await tester.tap(find.text('联网复核许可证'));
    await tester.pump();
    expect(find.text('应用主界面'), findsOneWidget);
    await tester.pumpWidget(const SizedBox());
  });

  testWidgets('expiry never replaces an integrity lock with a login prompt', (
    tester,
  ) async {
    var now = DateTime.utc(2026, 11, 4);
    final expiry = now.add(const Duration(seconds: 5));
    var locked = false;
    final controller = AppLicenseController(
      enabled: true,
      now: () => now,
      request:
          (
            action, {
            required username,
            required password,
            required key,
          }) async => locked
          ? {'phase': 'locked', 'ready': false, 'message': '软件完整性异常，已锁定。'}
          : {
              'phase': 'ready',
              'ready': true,
              'expiresAt': expiry.millisecondsSinceEpoch ~/ 1000,
              'refreshAt': expiry.millisecondsSinceEpoch ~/ 1000,
            },
    );
    await tester.pumpWidget(
      LicensedApp(
        controller: controller,
        child: const MaterialApp(home: Scaffold(body: Text('应用主界面'))),
      ),
    );
    await tester.pump();
    expect(find.text('应用主界面'), findsOneWidget);
    locked = true;
    await controller.perform('licenseStatus');
    await tester.pump();
    now = expiry;
    await tester.pump(const Duration(seconds: 5));
    expect(find.text('软件完整性异常，已锁定。'), findsOneWidget);
    expect(find.text('登录'), findsNothing);
    await tester.pumpWidget(const SizedBox());
  });

  testWidgets(
    'user management replaces the startup switch and mandatory gate returns to login at expiry',
    (tester) async {
      SharedPreferences.setMockInitialValues({});
      final preferences = await SharedPreferences.getInstance();
      final store = LocalStore(preferences);
      final keysBefore = preferences.getKeys();
      var now = DateTime.utc(2026, 11, 4);
      final originalExpiry = now.add(const Duration(seconds: 30));
      var expiry = originalExpiry;
      var state = 'login';
      var renewedByAuthority = false;
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
              if (action == 'licenseLogin') {
                expect(username, 'fixture@example.invalid');
                expect(password, 'Neutral#1');
                state = now.isBefore(originalExpiry) ? 'activation' : 'renewal';
              }
              if (action == 'licenseActivate') state = 'ready';
              if (action == 'licenseVerify' &&
                  state == 'renewal' &&
                  renewedByAuthority) {
                state = 'ready';
                expiry = DateTime.utc(2027, 1, 4);
              }
              if (state == 'ready' && !now.isBefore(expiry)) state = 'login';
              return {
                'phase': state,
                'ready': state == 'ready',
                if (state == 'ready') ...{
                  'username': 'fixture@example.invalid',
                  'licenseKey': 'AAAA-BBBB-CCCC-DDDD',
                  'expiresAt': expiry.millisecondsSinceEpoch ~/ 1000,
                  'refreshAt': expiry.millisecondsSinceEpoch ~/ 1000,
                },
              };
            },
      );
      await tester.pumpWidget(
        LicensedApp(
          controller: controller,
          child: MaterialApp(home: ProfilesScreen(store: store)),
        ),
      );
      await tester.pump();
      expect(find.text('用户管理'), findsNothing);
      expect(find.text('登录'), findsOneWidget);
      Future<void> login() async {
        await tester.enterText(
          find.byType(TextField).at(0),
          'fixture@example.invalid',
        );
        await tester.enterText(find.byType(TextField).at(1), 'Neutral#1');
        await tester.tap(find.text('登录'));
        await tester.pump();
      }

      await login();
      expect(find.text('用户管理'), findsNothing);
      expect(find.text('联网激活当前电脑'), findsOneWidget);
      await tester.enterText(find.byType(TextField), 'AAAA-BBBB-CCCC-DDDD');
      await tester.tap(find.text('联网激活当前电脑'));
      await tester.pump();
      expect(find.text('用户管理'), findsOneWidget);
      expect(find.text('启动时需要登录'), findsNothing);
      expect(find.byType(SwitchListTile), findsNothing);
      expect(find.text('当前用户：管理员'), findsNothing);
      expect(find.text('管理员 · 全部权限'), findsNothing);
      expect(find.text('添加用户'), findsNothing);
      expect(find.byTooltip('编辑用户'), findsNothing);
      expect(find.text('fixture@example.invalid'), findsOneWidget);
      final license = find.byKey(const ValueKey('profile-license-key'));
      expect(
        tester.widget<SelectableText>(license).data,
        '许可证：••••-••••-••••-DDDD',
      );
      expect(
        tester.getTopLeft(license).dy,
        greaterThan(
          tester.getTopLeft(find.text('用户的追剧和观看记录独立保存，下载文件由本机共享。')).dy,
        ),
      );
      expect(find.byKey(const ValueKey('licensed-username')), findsOneWidget);
      expect(
        find.byKey(const ValueKey('license-expiry-countdown')),
        findsOneWidget,
      );
      await tester.tap(find.text('显示完整许可证'));
      await tester.pump();
      expect(
        tester.widget<SelectableText>(license).data,
        '许可证：AAAA-BBBB-CCCC-DDDD',
      );
      now = originalExpiry;
      await tester.pump(const Duration(seconds: 30));
      await tester.pump();
      expect(find.text('用户管理'), findsNothing);
      expect(license, findsNothing);
      expect(find.text('登录'), findsOneWidget);
      await login();
      expect(find.text('续期后联网复核'), findsOneWidget);
      await tester.tap(find.text('续期后联网复核'));
      await tester.pump();
      expect(find.text('用户管理'), findsNothing);
      renewedByAuthority = true;
      await tester.tap(find.text('续期后联网复核'));
      await tester.pump();
      expect(find.text('用户管理'), findsOneWidget);
      expect(
        tester.widget<SelectableText>(license).data,
        '许可证：••••-••••-••••-DDDD',
      );
      expect(preferences.getKeys(), keysBefore);
      await tester.pumpWidget(const SizedBox());
      store.dispose();
    },
  );

  testWidgets(
    'a pending refresh cannot reopen the app after the expiry login gate',
    (tester) async {
      var now = DateTime.utc(2026, 11, 4);
      final expiry = now.add(const Duration(seconds: 2));
      final pending = Completer<Map<String, dynamic>>();
      var calls = 0;
      Map<String, dynamic> status(DateTime end) => {
        'phase': 'ready',
        'ready': true,
        'expiresAt': end.millisecondsSinceEpoch ~/ 1000,
        'refreshAt': end.millisecondsSinceEpoch ~/ 1000,
      };
      final controller = AppLicenseController(
        enabled: true,
        now: () => now,
        request:
            (
              action, {
              required username,
              required password,
              required key,
            }) async => ++calls == 1 ? status(expiry) : pending.future,
      );
      await tester.pumpWidget(
        LicensedApp(
          controller: controller,
          child: const MaterialApp(home: Scaffold(body: Text('应用主界面'))),
        ),
      );
      await tester.pump();
      expect(find.text('应用主界面'), findsOneWidget);
      final refresh = controller.perform('licenseStatus');
      now = expiry;
      await tester.pump(const Duration(seconds: 2));
      expect(find.text('登录'), findsOneWidget);
      pending.complete(status(expiry.add(const Duration(days: 60))));
      await refresh;
      await tester.pump();
      expect(find.text('应用主界面'), findsNothing);
      expect(find.text('登录'), findsOneWidget);
      await tester.pumpWidget(const SizedBox());
    },
  );

  AppLicenseController expiryController(DateTime Function() now, int seconds) =>
      AppLicenseController(
        enabled: true,
        now: now,
        request:
            (
              action, {
              required username,
              required password,
              required key,
            }) async => {
              'phase': 'ready',
              'ready': true,
              'username': 'fixture1@example.invalid',
              'expiresAt': now().millisecondsSinceEpoch ~/ 1000 + seconds,
              'refreshAt':
                  now().millisecondsSinceEpoch ~/ 1000 +
                  (seconds < 86400 ? seconds : 86400),
              'remainingSeconds': seconds,
            },
      );

  test(
    'countdown uses the authoritative expiry, strict 72-hour boundary and no negative values',
    () async {
      var now = DateTime.utc(2026, 11, 1);
      final controller = expiryController(() => now, 259200);
      await controller.open();
      expect(controller.username, 'fixture1@example.invalid');
      expect(controller.expiresSoon, isFalse);
      now = now.add(const Duration(seconds: 1));
      expect(controller.expiresSoon, isTrue);
      expect(licenseCountdown(controller.remaining!), '即将到期 2天 23:59:59');
      now = now.add(const Duration(days: 4));
      expect(controller.remaining, Duration.zero);
      expect(licenseCountdown(controller.remaining!), '许可证已到期');
      controller.dispose();
    },
  );

  testWidgets(
    'username and prominent expiry countdown tick without per-second network requests',
    (tester) async {
      var now = DateTime.utc(2026, 11, 1);
      var calls = 0;
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
              calls++;
              return {
                'phase': 'ready',
                'ready': true,
                'username': 'fixture1@example.invalid',
                'expiresAt': now.millisecondsSinceEpoch ~/ 1000 + 172801,
                'refreshAt': now.millisecondsSinceEpoch ~/ 1000 + 86400,
                'remainingSeconds': 172801,
              };
            },
      );
      await controller.open();
      await tester.pumpWidget(
        MaterialApp(
          home: Scaffold(body: LicenseAccountBadge(controller: controller)),
        ),
      );
      expect(find.byKey(const ValueKey('licensed-username')), findsOneWidget);
      expect(
        find.byKey(const ValueKey('license-expiry-countdown')),
        findsOneWidget,
      );
      expect(find.text('即将到期 2天 00:00:01'), findsOneWidget);
      now = now.add(const Duration(seconds: 2));
      await tester.pump(const Duration(seconds: 2));
      expect(find.text('即将到期 1天 23:59:59'), findsOneWidget);
      expect(calls, 1);
      await tester.pumpWidget(const SizedBox());
      controller.dispose();
    },
  );

  testWidgets(
    'normal expiry is quiet and narrow large-text warning does not overflow',
    (tester) async {
      final now = DateTime.utc(2026, 11, 1);
      final normal = expiryController(() => now, 259200);
      await normal.open();
      await tester.pumpWidget(
        MaterialApp(
          home: Scaffold(body: LicenseAccountBadge(controller: normal)),
        ),
      );
      expect(
        find.byKey(const ValueKey('license-expiry-countdown')),
        findsNothing,
      );
      expect(find.byKey(const ValueKey('license-valid-until')), findsOneWidget);
      final tooltip = tester.widget<Tooltip>(find.byType(Tooltip)).message!;
      expect(tooltip, startsWith('专属用户名：fixture1@example.invalid\n到期时间：'));
      expect(tooltip.split('\n'), hasLength(2));
      expect(tooltip, isNot(contains('有效期以许可证服务端签名为准')));
      final warning = expiryController(() => now, 1);
      await warning.open();
      await tester.pumpWidget(
        MaterialApp(
          home: MediaQuery(
            data: const MediaQueryData(textScaler: TextScaler.linear(2)),
            child: Scaffold(
              body: SizedBox(
                width: 300,
                child: LicenseAccountBadge(controller: warning),
              ),
            ),
          ),
        ),
      );
      expect(find.text('即将到期 0天 00:00:01'), findsOneWidget);
      expect(tester.takeException(), isNull);
      await tester.pumpWidget(const SizedBox());
      normal.dispose();
      warning.dispose();
    },
  );

  testWidgets(
    'expired response gates access instead of showing a renewable countdown',
    (tester) async {
      final now = DateTime.utc(2026, 11, 1);
      final controller = expiryController(() => now, 0);
      await controller.open();
      expect(controller.ready, isFalse);
      await tester.pumpWidget(
        MaterialApp(
          home: Scaffold(body: LicenseAccountBadge(controller: controller)),
        ),
      );
      expect(
        find.byKey(const ValueKey('license-expiry-countdown')),
        findsNothing,
      );
      await tester.pumpWidget(const SizedBox());
      controller.dispose();
    },
  );
  test(
    'expired ready response locks without a zero-delay verification loop',
    () async {
      var calls = 0;
      final now = DateTime.now().millisecondsSinceEpoch ~/ 1000;
      final controller = AppLicenseController(
        enabled: true,
        request:
            (
              action, {
              required username,
              required password,
              required key,
            }) async {
              calls++;
              return {
                'ready': true,
                'phase': 'ready',
                'refreshAt': now - 1,
                'expiresAt': now + 3600,
              };
            },
      );
      await controller.open();
      expect(controller.ready, isFalse);
      expect(controller.phase, 'verify');
      await Future<void>.delayed(const Duration(milliseconds: 10));
      expect(calls, 1);
      controller.dispose();
    },
  );

  test('offline login does not substitute for online activation', () async {
    final calls = <String>[];
    final controller = AppLicenseController(
      enabled: true,
      request:
          (action, {required username, required password, required key}) async {
            calls.add(action);
            return {
              'phase': action == 'licenseLogin' ? 'activation' : 'login',
              'ready': false,
            };
          },
    );
    await controller.open();
    await controller.perform(
      'licenseLogin',
      username: 'fixture1@example.invalid',
      password: 'Abc123@#',
    );
    expect(controller.ready, isFalse);
    expect(controller.phase, 'activation');
    expect(calls, ['licenseStatus', 'licenseLogin']);
    controller.dispose();
  });

  test(
    'verification error locks instead of keeping old ready status',
    () async {
      var fail = false;
      final controller = AppLicenseController(
        enabled: true,
        request:
            (
              action, {
              required username,
              required password,
              required key,
            }) async {
              if (fail) throw StateError('neutral fixture unavailable');
              return {'phase': 'ready', 'ready': true};
            },
      );
      await controller.open();
      expect(controller.ready, isTrue);
      fail = true;
      await controller.perform('licenseStatus');
      expect(controller.ready, isFalse);
      controller.dispose();
    },
  );

  testWidgets(
    'activation screen does not expose the application or download controls',
    (tester) async {
      final controller = AppLicenseController(
        enabled: true,
        request:
            (
              action, {
              required username,
              required password,
              required key,
            }) async => {
              'phase': 'login',
              'ready': false,
              'message': '请输入专属用户名和密码',
            },
      );
      await controller.open();
      await tester.pumpWidget(
        MaterialApp(home: AppLicenseScreen(controller: controller)),
      );
      expect(find.text('登录'), findsOneWidget);
      expect(find.text('全选下载'), findsNothing);
      expect(find.textContaining('密码只在本机验证，不会上传。'), findsNothing);
      expect(find.textContaining('完整性异常会锁定并去重上报必要诊断'), findsNothing);
      final fields = tester
          .widgetList<TextField>(find.byType(TextField))
          .toList();
      expect(fields.last.obscureText, isTrue);
      await tester.pumpWidget(const SizedBox());
      controller.dispose();
    },
  );
}
