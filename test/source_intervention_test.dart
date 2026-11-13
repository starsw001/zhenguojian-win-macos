import 'package:duanju_app/source_intervention.dart';
import 'package:duanju_app/source_status.dart';
import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';

void main() {
  test('rate limit remains blocked until native retry time', () {
    final retry = DateTime.utc(2026, 10, 28, 6);
    final intervention = SourceIntervention(
      SourceStatus.fromJson({
        'source': 'wangguo',
        'error': 'ordinary.example.org HTTP 429，请在60秒后重试',
        'retryAt': retry.toIso8601String(),
      }),
    );
    expect(intervention.required, isTrue);
    expect(intervention.rateLimited, isTrue);
    expect(
      intervention.canResumeAt(retry.subtract(const Duration(seconds: 1))),
      isFalse,
    );
    expect(intervention.canResumeAt(retry), isTrue);
    expect(
      intervention.verificationUri.toString(),
      'https://ordinary.example.org/',
    );
  });

  test('manual verification address excludes credentials and token query', () {
    final intervention = SourceIntervention(
      SourceStatus.fromJson({
        'error': '需要人工验证码',
        'catalogOrigin':
            'https://ordinary.example.org/catalog?token=private#data',
      }),
    );
    expect(intervention.challenge, isTrue);
    expect(
      intervention.verificationUri.toString(),
      'https://ordinary.example.org/',
    );
    final unsafe = SourceIntervention(
      SourceStatus.fromJson({
        'error': '需要人工验证码',
        'catalogOrigin': 'https://name:password@ordinary.example.org/',
      }),
    );
    expect(unsafe.verificationUri, isNull);
  });

  test('ordinary errors do not pretend to be captchas', () {
    expect(
      SourceIntervention(SourceStatus.fromJson({'error': 'HTTP 404'})).required,
      isFalse,
    );
    expect(
      SourceIntervention(SourceStatus.fromJson({'error': '目录接口为空'})).required,
      isFalse,
    );
  });

  testWidgets(
    'dialog requests manual action and never opens browser automatically',
    (tester) async {
      await tester.pumpWidget(
        MaterialApp(
          home: Scaffold(
            body: SourceInterventionDialog(
              name: '普通站源',
              status: SourceStatus.fromJson({
                'error': 'ordinary.example.org HTTP 403：站点要求浏览器验证',
              }),
            ),
          ),
        ),
      );
      expect(find.text('普通站源 · 需要人工验证'), findsOneWidget);
      expect(find.text('在浏览器打开验证页'), findsOneWidget);
      expect(find.text('已手动验证，继续'), findsOneWidget);
      expect(find.textContaining('浏览器登录态不会自动复制'), findsOneWidget);
      await tester.pumpWidget(const SizedBox());
    },
  );

  testWidgets('rate-limit resume button respects cooldown', (tester) async {
    await tester.pumpWidget(
      MaterialApp(
        home: Scaffold(
          body: SourceInterventionDialog(
            name: '普通站源',
            status: SourceStatus.fromJson({
              'error': 'HTTP 429',
              'retryAt': DateTime.now()
                  .add(const Duration(minutes: 1))
                  .toIso8601String(),
            }),
          ),
        ),
      ),
    );
    expect(
      tester
          .widget<FilledButton>(
            find.byKey(const ValueKey('source-human-resume')),
          )
          .onPressed,
      isNull,
    );
    await tester.pumpWidget(const SizedBox());
  });
}
