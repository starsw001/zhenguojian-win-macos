import 'dart:async';
import 'package:duanju_app/app_about.dart';
import 'package:duanju_app/app_version.g.dart';
import 'package:duanju_app/app_build.dart';
import 'package:duanju_app/core_bridge.dart';
import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';

class AboutRepo extends Fake implements AppRepository {
  final reply = Completer<Map<String, dynamic>>();
  final calls = <String>[];
  @override
  Future<Map<String, dynamic>> checkForUpdate(String version, String edition) {
    calls.add('$version/$edition');
    if (calls.length == 2) {
      return Future.value({
        'available': true,
        'current': version,
        'version': '1.1.29+160',
        'notes': '测试发布',
        'assetUrl': 'verified-fixture',
        'ticket': 'fixture',
      });
    }
    return reply.future;
  }
}

void main() {
  testWidgets(
    'empty public release channel does not report current or show install',
    (tester) async {
      final repo = AboutRepo();
      repo.reply.complete({
        'available': false,
        'current': appVersion,
        'noRelease': true,
      });
      await tester.pumpWidget(
        MaterialApp(
          home: Scaffold(body: AppAboutDialog(repository: repo)),
        ),
      );
      await tester.tap(find.text('检查更新'));
      await tester.pumpAndSettle();
      expect(find.text('暂无正式发布'), findsOneWidget);
      expect(find.text('已是最新版本'), findsNothing);
      expect(find.text('立即更新'), findsNothing);
    },
  );

  testWidgets(
    'About checks immediately once, reports errors honestly, then can check again',
    (tester) async {
      final repo = AboutRepo();
      await tester.pumpWidget(
        MaterialApp(
          home: Scaffold(body: AppAboutDialog(repository: repo)),
        ),
      );
      expect(find.text('查看许可'), findsNothing);
      final check = find.byKey(const ValueKey('about-check-updates'));
      expect(check, findsOneWidget);
      await tester.tap(check);
      await tester.pump();
      expect(find.text('正在检查…'), findsOneWidget);
      await tester.tap(check);
      await tester.pump();
      expect(repo.calls, [
        '$appVersion/${allSourcesEnabled ? "zhenguoshang" : "hongguoshang"}',
      ]);
      repo.reply.complete({
        'available': false,
        'current': appVersion,
        'error': '尚未找到可读取的正式发布（GitHub HTTP 404），请稍后再检查。',
      });
      await tester.pumpAndSettle();
      expect(find.text('更新检查未完成'), findsOneWidget);
      expect(find.textContaining('HTTP 404'), findsOneWidget);
      expect(find.text('打开发布页'), findsOneWidget);
      expect(find.text('已是最新版本'), findsNothing);
      expect(find.text('立即更新'), findsNothing);
      await tester.tap(find.text('知道了'));
      await tester.pumpAndSettle();
      await tester.tap(check);
      await tester.pumpAndSettle();
      expect(repo.calls.length, 2);
      expect(find.text('发现新版本 1.1.29+160'), findsOneWidget);
      expect(find.text('立即更新'), findsOneWidget);
    },
  );
}
