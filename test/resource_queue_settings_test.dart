import 'dart:io';

import 'package:duanju_app/local_store.dart';
import 'package:duanju_app/resource_settings.dart';
import 'package:duanju_app/resource_settings_screen.dart';
import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:shared_preferences/shared_preferences.dart';

import 'fixtures.dart';

class QueueSettingsRepository extends FixtureRepository {
  ResourceSettings? saved;
  @override
  Future<ResourceSettings> resourceSettings() async =>
      const ResourceSettings(downloadConcurrency: 6);
  @override
  Future<ResourceSettings> saveResourceSettings(
    ResourceSettings settings,
  ) async => saved = settings;
}

class PortableLogSettingsRepository extends QueueSettingsRepository {
  @override
  Future<ResourceSettings> resourceSettings() async => ResourceSettings(
    downloadConcurrency: 6,
    logDirectory: saved?.logDirectory ?? '',
    logPath: saved?.logDirectory.isNotEmpty == true
        ? '${saved!.logDirectory}\\app.log'
        : r'E:\Software\logs\app.log',
    defaultLogDirectory: r'E:\Software\logs',
  );
}

void main() {
  testWidgets(
    'log directory can be selected saved and restored to software default',
    (tester) async {
      tester.view.physicalSize = const Size(1000, 1600);
      tester.view.devicePixelRatio = 1;
      addTearDown(tester.view.resetPhysicalSize);
      addTearDown(tester.view.resetDevicePixelRatio);
      SharedPreferences.setMockInitialValues({});
      final store = LocalStore(await SharedPreferences.getInstance());
      addTearDown(store.dispose);
      final repository = PortableLogSettingsRepository();
      await tester.pumpWidget(
        MaterialApp(
          home: ResourceSettingsScreen(
            repository: repository,
            store: store,
            logDirectoryPicker: () async => r'F:\Chosen Logs',
          ),
        ),
      );
      await tester.pumpAndSettle();
      expect(find.text(r'当前日志文件：E:\Software\logs\app.log'), findsOneWidget);
      await tester.tap(find.text('选择日志目录'));
      await tester.pumpAndSettle();
      expect(
        tester
            .widget<TextFormField>(
              find.byKey(const ValueKey('resource-log-directory')),
            )
            .controller!
            .text,
        r'F:\Chosen Logs',
      );
      await tester.tap(find.text('保存'));
      await tester.pumpAndSettle();
      expect(repository.saved!.logDirectory, r'F:\Chosen Logs');
      expect(find.text(r'当前日志文件：F:\Chosen Logs\app.log'), findsOneWidget);
      await tester.tap(find.text('使用软件默认目录'));
      await tester.pumpAndSettle();
      await tester.tap(find.text('保存'));
      await tester.pumpAndSettle();
      expect(repository.saved!.logDirectory, '');
      await tester.pumpWidget(const SizedBox.shrink());
    },
    skip: !Platform.isWindows,
  );

  test('legacy settings retain total and default only new per-source cap', () {
    final settings = ResourceSettings.fromJson({
      'downloadConcurrency': 6,
      'downloadBySource': true,
      'proxyMode': 'direct',
    });
    expect(settings.downloadConcurrency, 6);
    expect(settings.downloadSourceConcurrency, 1);
    expect(settings.downloadBySource, isTrue);
    expect(settings.proxyMode, 'direct');
    expect(settings.hongguoProxyMode, 'auto');
    expect(settings.toJson()['downloadSourceConcurrency'], 1);
    expect(settings.toJson()['hongguoProxyMode'], 'auto');
    for (final old in ['direct', 'system']) {
      expect(
        ResourceSettings.fromJson({'hongguoProxyMode': old}).hongguoProxyMode,
        'auto',
      );
    }
  });

  testWidgets(
    'Hongguo network defaults to direct-first and saves strict direct',
    (tester) async {
      tester.view.physicalSize = const Size(1000, 1600);
      tester.view.devicePixelRatio = 1;
      addTearDown(tester.view.resetPhysicalSize);
      addTearDown(tester.view.resetDevicePixelRatio);
      SharedPreferences.setMockInitialValues({});
      final store = LocalStore(await SharedPreferences.getInstance());
      addTearDown(store.dispose);
      final repository = QueueSettingsRepository();
      await tester.pumpWidget(
        MaterialApp(
          home: ResourceSettingsScreen(repository: repository, store: store),
        ),
      );
      await tester.pumpAndSettle();
      final control = find.byKey(const ValueKey('hongguo-proxy-mode'));
      expect(control, findsOneWidget);
      expect(tester.widget<SegmentedButton<String>>(control).selected, const {
        'auto',
      });
      await tester.tap(find.text('仅直连'));
      await tester.pumpAndSettle();
      await tester.tap(find.text('保存'));
      await tester.pumpAndSettle();
      expect(repository.saved!.hongguoProxyMode, 'directOnly');
      await tester.pumpWidget(const SizedBox.shrink());
    },
  );

  testWidgets(
    'download controls offer1-10 and persist per-source cap with total',
    (tester) async {
      tester.view.physicalSize = const Size(1000, 1600);
      tester.view.devicePixelRatio = 1;
      addTearDown(tester.view.resetPhysicalSize);
      addTearDown(tester.view.resetDevicePixelRatio);
      SharedPreferences.setMockInitialValues({});
      final store = LocalStore(await SharedPreferences.getInstance());
      addTearDown(store.dispose);
      final repository = QueueSettingsRepository();
      await tester.pumpWidget(
        MaterialApp(
          home: ResourceSettingsScreen(repository: repository, store: store),
        ),
      );
      await tester.pumpAndSettle();
      final downloadCounts = find.byWidgetPredicate(
        (w) => w is DropdownButton<int> && w.items!.length == 10,
      );
      expect(downloadCounts, findsNWidgets(2));
      final total = downloadCounts.at(0);
      final source = downloadCounts.at(1);
      expect(
        tester.widget<DropdownButton<int>>(total).items!.map((i) => i.value),
        List.generate(10, (i) => i + 1),
      );
      expect(
        tester.widget<DropdownButton<int>>(source).items!.map((i) => i.value),
        List.generate(10, (i) => i + 1),
      );
      await tester.tap(source);
      await tester.pumpAndSettle();
      await tester.tap(find.text('10').last);
      await tester.pumpAndSettle();
      await tester.tap(find.text('保存'));
      await tester.pumpAndSettle();
      expect(repository.saved!.downloadConcurrency, 6);
      expect(repository.saved!.downloadSourceConcurrency, 10);
      expect(repository.saved!.catalogConcurrency, 3);
      await tester.pumpWidget(const SizedBox.shrink());
    },
  );
}
