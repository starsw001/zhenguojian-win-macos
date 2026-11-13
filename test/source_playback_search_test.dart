import 'package:duanju_app/core_bridge.dart';
import 'package:duanju_app/models.dart';
import 'package:duanju_app/resource_settings.dart';
import 'package:duanju_app/source_playback_check.dart';
import 'package:duanju_app/source_status.dart';
import 'package:flutter_test/flutter_test.dart';

import 'fixtures.dart';

class _SearchCheckRepository extends FixtureRepository {
  _SearchCheckRepository(this.mode);
  final String mode;
  final queries = <String>[];
  static const sample = Drama(
    id: 'hongguo:synthetic',
    source: 'hongguo',
    title: '重回1995完整合成剧名',
  );

  @override
  Future<ResourceSettings> resourceSettings() async => const ResourceSettings();

  @override
  Future<CatalogPage> catalog(
    String source, {
    int page = 1,
    String query = '',
    String category = '',
    bool force = false,
  }) async {
    if (query.isEmpty) return CatalogPage([sample]);
    queries.add(query);
    if (mode == 'blocked') throw AppFailure('HTTP 403，需人工验证');
    if (mode == 'local') {
      return CatalogPage([sample], localSearch: true, warning: '在线服务 503');
    }
    if (mode == 'warning') return CatalogPage([], warning: '上游返回异常');
    return CatalogPage(query == sample.title ? [sample] : []);
  }

  @override
  Future<DramaDetail> detail(Drama drama) async =>
      throw AppFailure('该隔离用例不调用播放器');

  @override
  Future<SourceStatus> saveSourceHealth(
    String source,
    Map<String, dynamic> health,
  ) async => SourceStatus.fromJson({'source': source, 'health': health});
}

void main() {
  TestWidgetsFlutterBinding.ensureInitialized();

  test(
    'an empty short query may be verified using the full real title',
    () async {
      final repository = _SearchCheckRepository('empty');
      final result = await SourcePlaybackCheck(
        repository,
      ).run(SourceSite.hongguo);
      expect(repository.queries, ['重回19', _SearchCheckRepository.sample.title]);
      final search = ((result['health'] as Map)['steps'] as List)[1] as Map;
      expect(search['state'], 'ok');
      expect(search['message'], contains('短关键词无结果'));
      expect(search['message'], contains(_SearchCheckRepository.sample.title));
      expect(result['decodedSamples'], isEmpty);
    },
  );

  for (final mode in ['blocked', 'local', 'warning']) {
    test('search $mode is not retried or reported as online success', () async {
      final repository = _SearchCheckRepository(mode);
      final result = await SourcePlaybackCheck(
        repository,
      ).run(SourceSite.hongguo);
      expect(repository.queries, ['重回19']);
      final search = ((result['health'] as Map)['steps'] as List)[1] as Map;
      expect(search['state'], 'failed');
      expect(result['decodedSamples'], isEmpty);
    });
  }
}
