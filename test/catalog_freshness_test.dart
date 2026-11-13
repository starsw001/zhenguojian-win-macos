import 'package:duanju_app/catalog_browser.dart';
import 'package:duanju_app/models.dart';
import 'package:flutter_test/flutter_test.dart';
import 'fixtures.dart';

class FreshProbeRepository extends FixtureRepository {
  int reads = 0;
  bool wasForced = false;
  @override
  Future<CatalogPage> cached(String source, {String category = ''}) async =>
      CatalogPage(
        const [
          Drama(id: 'huangguoai:old', source: 'huangguoai', title: '普通旧条目'),
        ],
        fresh: true,
        hasMore: true,
      );
  @override
  Future<CatalogPage> catalog(
    String source, {
    int page = 1,
    String query = '',
    String category = '',
    bool force = false,
  }) async {
    reads++;
    wasForced = force;
    return CatalogPage(const [
      Drama(id: 'huangguoai:new', source: 'huangguoai', title: '普通新条目'),
    ], fresh: true);
  }
}

void main() {
  test('fresh cached directory is shown then newest head is merged', () async {
    final repo = FreshProbeRepository();
    final browser = CatalogBrowser(repo);
    final page = await browser.load(
      SourceGroup('huangguoai', 'entry', [SourceSite.byId('huangguoai')]),
      useCache: true,
    );
    final data = {
      'networkReads': repo.reads,
      'forcedHead': repo.wasForced,
      'newVisible': page.items.any((d) => d.id == 'huangguoai:new'),
      'oldRetained': page.items.any((d) => d.id == 'huangguoai:old'),
    };
    expect(data['networkReads'], 1);
    expect(data['forcedHead'], true);
    expect(data['newVisible'], true);
    expect(data['oldRetained'], true);
  });
  test('cache only remains offline', () async {
    final repo = FreshProbeRepository();
    final browser = CatalogBrowser(repo);
    await browser.load(
      SourceGroup('huangguoai', 'entry', [SourceSite.byId('huangguoai')]),
      cacheOnly: true,
    );
    expect(repo.reads, 0);
  });
  test(
    'ordinary source refreshes the current head even with fresh cache',
    () async {
      final repo = FreshProbeRepository();
      final browser = CatalogBrowser(repo);
      await browser.load(
        const SourceGroup('hongguo', 'entry', [SourceSite.hongguo]),
        useCache: true,
      );
      expect(repo.reads, 1);
      expect(repo.wasForced, true);
    },
  );
  test('three independent entries refresh heads', () {
    for (final id in ['huangguo-video', 'huangguoai', 'cloudfront']) {
      expect(SourceSite.byId(id).refreshHeadOnOpen, true);
    }
  });
}
