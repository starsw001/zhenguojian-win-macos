import 'dart:io';
import 'dart:ui' as ui;

import 'package:duanju_app/app_license.dart';
import 'package:flutter/material.dart';
import 'package:flutter/rendering.dart';
import 'package:flutter/services.dart';
import 'package:flutter_test/flutter_test.dart';

void main() {
  final output = Platform.environment['LICENSE_PREVIEW_OUTPUT'];
  testWidgets(
    'render isolated expiry badge at desktop, dark and narrow large-text sizes',
    (tester) async {
      final font = Platform.environment['LICENSE_PREVIEW_FONT'];
      if (font != null) {
        await tester.runAsync(() async {
          final bytes = await File(font).readAsBytes();
          final loader = FontLoader('LicensePreview')
            ..addFont(Future.value(ByteData.sublistView(bytes)));
          await loader.load();
          final icons = Platform.environment['LICENSE_PREVIEW_ICON_FONT'];
          if (icons != null) {
            final iconBytes = await File(icons).readAsBytes();
            final iconLoader = FontLoader('MaterialIcons')
              ..addFont(Future.value(ByteData.sublistView(iconBytes)));
            await iconLoader.load();
          }
        });
      }
      final now = DateTime.utc(2026, 11, 1, 12);
      final controller = AppLicenseController(
        enabled: true,
        now: () => now,
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
              'expiresAt': now.millisecondsSinceEpoch ~/ 1000 + 172799,
              'refreshAt': now.millisecondsSinceEpoch ~/ 1000 + 86400,
              'remainingSeconds': 172799,
            },
      );
      await controller.open();
      var disposed = false;
      addTearDown(() {
        if (!disposed) controller.dispose();
      });
      addTearDown(tester.view.resetPhysicalSize);
      addTearDown(tester.view.resetDevicePixelRatio);
      for (final item in [
        ('desktop-light', const Size(1040, 280), Brightness.light, 1.0),
        ('desktop-dark', const Size(1040, 280), Brightness.dark, 1.0),
        ('narrow-large-text', const Size(390, 360), Brightness.light, 1.7),
      ]) {
        tester.view.physicalSize = item.$2;
        tester.view.devicePixelRatio = 1;
        final boundary = GlobalKey();
        await tester.pumpWidget(
          MaterialApp(
            key: ValueKey(item.$1),
            themeAnimationDuration: Duration.zero,
            theme: ThemeData(
              fontFamily: font == null ? null : 'LicensePreview',
              brightness: item.$3,
            ),
            home: MediaQuery(
              data: MediaQueryData(
                size: item.$2,
                textScaler: TextScaler.linear(item.$4),
              ),
              child: RepaintBoundary(
                key: boundary,
                child: Scaffold(
                  appBar: AppBar(
                    title: const Text('授权信息布局核对'),
                    bottom: PreferredSize(
                      preferredSize: const Size.fromHeight(110),
                      child: Padding(
                        padding: const EdgeInsets.all(12),
                        child: Align(
                          alignment: Alignment.centerRight,
                          child: LicenseAccountBadge(controller: controller),
                        ),
                      ),
                    ),
                  ),
                  body: const Padding(
                    padding: EdgeInsets.all(16),
                    child: Text('隔离组件布局样本，不代表真实激活或全客户端界面验收。'),
                  ),
                ),
              ),
            ),
          ),
        );
        await tester.pump();
        expect(
          Theme.of(tester.element(find.byType(LicenseAccountBadge))).brightness,
          item.$3,
        );
        expect(tester.takeException(), isNull);
        expect(find.text('fixture1@example.invalid'), findsOneWidget);
        expect(find.text('即将到期 1天 23:59:59'), findsOneWidget);
        final render =
            boundary.currentContext!.findRenderObject()!
                as RenderRepaintBoundary;
        await tester.runAsync(() async {
          final image = await render.toImage(pixelRatio: 1);
          final bytes = await image.toByteData(format: ui.ImageByteFormat.png);
          final file = File('$output/${item.$1}.png');
          await file.parent.create(recursive: true);
          await file.writeAsBytes(bytes!.buffer.asUint8List());
          image.dispose();
        });
      }
      await tester.pumpWidget(const SizedBox());
      controller.dispose();
      disposed = true;
    },
    skip: output == null,
  );
}
