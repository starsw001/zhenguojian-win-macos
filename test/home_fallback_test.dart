import 'package:duanju_app/catalog_browser.dart';
import 'package:duanju_app/models.dart';
import 'package:flutter_test/flutter_test.dart';

import 'fixtures.dart';

void main() {
  const selected = SourceGroup('hongguo', '红果', [SourceSite.hongguo]);
  test(
    'home fallback prefers known local rows and ignores other origins',
    () async {
      final repo = FixtureRepository();
      const known = Drama(
        id: 'hanxiaoquan:ok',
        source: 'hanxiaoquan',
        title: '隔离目录',
      );
      repo.cachedPages['hanxiaoquan'] = CatalogPage([
        FixtureRepository.vip,
        known,
      ]);
      final page = await CatalogBrowser(repo).homeFallback(selected, [
        SourceSite.hongguo,
        SourceSite.hanxiaoquan,
        SourceSite.guipian,
        SourceSite.dsd,
      ]);
      expect(page.items.map((row) => row.id), [known.id]);
      expect(page.warning, contains('韩小圈'));
      expect(page.hasMore, isFalse);
      expect(repo.requests, isEmpty);
    },
  );

  test(
    'home fallback bounds network calls and never opens a restricted source',
    () async {
      final repo = FixtureRepository();
      final browser = CatalogBrowser(repo);
      final visible = [
        SourceSite.hongguo,
        SourceSite.duanjuValues.first,
        SourceSite.hanxiaoquan,
        SourceSite.guipian,
        SourceSite.sorani,
      ];
      expect(
        (await browser.homeFallback(selected, visible, cacheOnly: true)).items,
        isEmpty,
      );
      expect(repo.requests, isEmpty);
      final page = await browser.homeFallback(selected, visible);
      expect(repo.requests, ['hanxiaoquan', 'guipian']);
      expect(page.items.map((row) => row.source), ['hanxiaoquan', 'guipian']);
      expect(page.warning, contains('韩小圈、鬼片'));
      expect(
        (await browser.homeFallback(selected, [SourceSite.hongguo])).items,
        isEmpty,
      );
    },
  );
}
