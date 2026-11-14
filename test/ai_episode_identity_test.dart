import 'package:duanju_app/catalog_updates.dart';
import 'package:duanju_app/core_bridge.dart';
import 'package:duanju_app/models.dart';
import 'package:duanju_app/ordinary_catalog.dart';
import 'package:flutter_test/flutter_test.dart';

Drama work(String host, int count, {bool verified = true}) {
  final uri = 'https://$host/detail/89/';
  return Drama(
    id: 'huangguoai:$uri',
    source: 'huangguoai',
    sourceId: uri,
    title: '普通作品',
    episodes: count,
    episodeCountVerified: verified,
  );
}

List<Episode> episodes(Drama drama, int count, {String? host}) => [
  for (var number = 1; number <= count; number++)
    Episode({
      'id': '${drama.id}:ep-$number',
      'source': 'huangguoai',
      'title': '第$number集',
      'currentEpisode': number,
      'pageUrl':
          'https://${host ?? Uri.parse(drama.sourceId).host}/video/89/ep-$number/',
    }, number),
];

void main() {
  test('same title and numeric ID stay independent across domains', () {
    final a = work('catalog.example.org', 8);
    final b = work('fallback.example.org', 25);
    final updates = CatalogUpdates()..publish(b);
    expect(a.id, isNot(b.id));
    expect(updates.current(a).episodes, 8);
    expect(a.merge(b).episodes, 25);
    expect(a.merge(b).id, b.id);
    updates.dispose();
  });

  test(
    'fresh eight-episode detail overrides stale count without adding episodes',
    () {
      final selected = work('catalog.example.org', 25, verified: false);
      final detail = validateIsolatedSiteDetail(
        selected,
        DramaDetail(work('catalog.example.org', 8), episodes(selected, 8)),
      );
      expect(detail.drama.episodes, 8);
      expect(detail.episodes.length, 8);
      expect(detail.drama.episodeCountVerified, isTrue);
    },
  );

  test('foreign same-name site cannot supply the requested work detail', () {
    final selected = work('catalog.example.org', 8);
    final other = work('fallback.example.org', 25);
    expect(
      () => validateIsolatedSiteDetail(
        selected,
        DramaDetail(other, episodes(other, 25)),
      ),
      throwsA(isA<AppFailure>()),
    );
  });

  test(
    'foreign episode page fails even with a forged selected-work prefix',
    () {
      final selected = work('catalog.example.org', 8);
      expect(
        () => validateIsolatedSiteDetail(
          selected,
          DramaDetail(
            selected,
            episodes(selected, 8, host: 'fallback.example.org'),
          ),
        ),
        throwsA(isA<AppFailure>()),
      );
    },
  );

  test(
    'legacy count remains in records but is not labelled as current data',
    () {
      const old = Drama(
        id: 'huangguoai:89',
        source: 'huangguoai',
        sourceId: '89',
        title: '普通作品',
        episodes: 25,
      );
      expect(old.episodes, 25);
      expect(old.episodeCountLabel, contains('需刷新'));
      expect(
        () => validateIsolatedSiteDetail(
          old,
          DramaDetail(old, episodes(work('catalog.example.org', 8), 8)),
        ),
        throwsA(isA<AppFailure>()),
      );
      final unknown = work('catalog.example.org', 0, verified: false);
      expect(work('catalog.example.org', 25).merge(unknown).episodes, 0);
      expect(unknown.episodeCountLabel, '集数待获取');
    },
  );

  test('stale first episode title is replaced by the work title', () {
    final base = work('catalog.example.org', 21);
    final selected = Drama.fromJson({
      ...base.toJson(),
      'title': '神瞳觉醒 第一季 第1集',
    });
    final fresh = Drama.fromJson({...base.toJson(), 'title': '神瞳觉醒 第一季'});
    final detail = validateIsolatedSiteDetail(
      selected,
      DramaDetail(fresh, episodes(fresh, 21)),
    );
    expect(detail.drama.title, '神瞳觉醒 第一季');
  });
}
