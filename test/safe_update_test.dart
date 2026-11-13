import 'package:duanju_app/core_bridge.dart';
import 'package:duanju_app/updater.dart';
import 'package:duanju_app/update_activity.dart';
import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';

class FakeUpdates extends Fake implements AppRepository {
  int downloadRequests = 0;
  @override
  Future<bool> updateBusy() async => false;
  @override
  Future<Map<String, dynamic>> downloadUpdate(
    String version,
    String edition,
    String ticket,
  ) async {
    downloadRequests++;
    expect(ticket, 'signed-ticket');
    throw AppFailure('测试包整包哈希不符');
  }
}

void main() {
  testWidgets(
    'immediate update uses the displayed offer, reports failure and does not exit',
    (tester) async {
      final repo = FakeUpdates();
      await tester.pumpWidget(
        MaterialApp(
          home: Builder(
            builder: (context) => TextButton(
              onPressed: () => showUpdateDialog(
                context,
                repo,
                const UpdateInfo(
                  available: true,
                  current: '1.1.27+158',
                  version: '1.1.28+159',
                  notes: '修复',
                  pageUrl: '',
                  assetUrl: 'verified',
                  ticket: 'signed-ticket',
                ),
              ),
              child: const Text('open'),
            ),
          ),
        ),
      );
      await tester.tap(find.text('open'));
      await tester.pumpAndSettle();
      await tester.tap(find.text('立即更新'));
      await tester.pumpAndSettle();
      expect(repo.downloadRequests, 1);
      expect(find.textContaining('原版本保持不变'), findsOneWidget);
    },
  );
  testWidgets('active local or online player prevents download and handoff', (
    tester,
  ) async {
    final repo = FakeUpdates();
    UpdateActivity.enterPlayer();
    addTearDown(UpdateActivity.leavePlayer);
    await tester.pumpWidget(
      MaterialApp(
        home: Builder(
          builder: (context) => TextButton(
            onPressed: () => showUpdateDialog(
              context,
              repo,
              const UpdateInfo(
                available: true,
                current: '1.1.27+158',
                version: '1.1.28+159',
                notes: '',
                pageUrl: '',
                assetUrl: 'verified',
                ticket: 'signed-ticket',
              ),
            ),
            child: const Text('check'),
          ),
        ),
      ),
    );
    await tester.tap(find.text('check'));
    await tester.pumpAndSettle();
    await tester.tap(find.text('立即更新'));
    await tester.pumpAndSettle();
    expect(repo.downloadRequests, 0);
    expect(find.textContaining('暂不更新'), findsOneWidget);
  });
}
