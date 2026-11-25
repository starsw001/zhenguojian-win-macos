import 'package:duanju_app/source_status.dart';
import 'package:flutter_test/flutter_test.dart';

void main() {
  test(
    'update progress hides bare hosts but keeps page counts and HTTP errors',
    () {
      final raw =
          '单地址暂存 1/2 · 短剧 · 第12页；完成后替换目录缓存 · 已保存 1944 部 · thu.example.cc HTTP 404';
      final status = SourceStatus.fromJson({
        'source': 'huangguoai',
        'stage': raw,
      });
      expect(status.displayStage, isNot(contains('thu.example.cc')));
      for (final text in ['1/2', '第12页', '1944 部', 'HTTP 404']) {
        expect(status.displayStage, contains(text));
      }
      expect(status.stage, raw);
      expect(
        sourceProgressText('失败 203.0.113.12:8443/path HTTP 503'),
        '失败  HTTP 503',
      );
      expect(
        sourceProgressText('诊断文件 app.log 和 app.log.1 保留'),
        '诊断文件 app.log 和 app.log.1 保留',
      );
    },
  );
  test(
    'cached progress hides address but retains count and raw source identity',
    () {
      final status = SourceStatus.fromJson({
        'source': 'huangguoai',
        'operation': 'refreshAll',
        'stage':
            '单地址暂存 1/4 · 普通分类 · 第2页 · https://metadata.example.org；完成后替换目录缓存',
        'catalogOrigin': 'https://metadata.example.org',
        'count': 3047,
      });
      expect(status.displayStage, '单地址暂存 1/4 · 普通分类 · 第2页；完成后替换目录缓存');
      expect(status.stage, contains('https://metadata.example.org'));
      expect(status.catalogOrigin, 'https://metadata.example.org');
      expect(status.count, 3047);
    },
  );

  test('progress errors hide all URLs without changing the retained error', () {
    final status = SourceStatus.fromJson({
      'error':
          'https://one.example.org/a?token=test HTTP 503；HTTPS://two.example.org:8443 重试失败',
    });
    expect(status.displayError, isNot(contains('example.org')));
    expect(status.displayError, contains('HTTP 503'));
    expect(status.displayError, contains('重试失败'));
    expect(status.error, contains('https://one.example.org/a?token=test'));
  });

  test('plain progress and empty values stay unchanged', () {
    expect(
      sourceProgressText('同一地址目录获取完成，正在原子替换该源目录缓存'),
      '同一地址目录获取完成，正在原子替换该源目录缓存',
    );
    expect(sourceProgressText(''), '');
  });
}
