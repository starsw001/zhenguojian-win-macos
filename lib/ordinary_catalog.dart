import 'core_bridge.dart';
import 'models.dart';

String ordinaryTitleKey(String title) =>
    title.trim().replaceAll(RegExp(r'\s+'), ' ').toLowerCase();
String _withoutFirstEpisodeSuffix(String title) =>
    title.replaceFirst(RegExp(r'\s*第\s*(?:0*1|一)\s*集\s*$'), '').trim();
String _knownYear(Drama drama) {
  final date = RegExp(
    r'^(?:19|20)\d{2}',
  ).firstMatch(drama.onlineDate)?.group(0);
  if (date != null) return date;
  return drama.tags
          .where((tag) => RegExp(r'^(?:19|20)\d{2}年?$').hasMatch(tag))
          .map((tag) => tag.substring(0, 4))
          .firstOrNull ??
      '';
}

String _knownKind(Drama drama) {
  if (RegExp(r'动漫|动画|漫剧').hasMatch(drama.category)) return 'animation';
  if (RegExp(r'电影|片$').hasMatch(drama.category)) return 'movie';
  if (RegExp(r'电视剧|真人剧|短剧').hasMatch(drama.category)) return 'drama';
  return '';
}

bool ordinaryTitlesCompatible(Drama first, Drama second) {
  if (ordinaryTitleKey(first.title) != ordinaryTitleKey(second.title)) {
    return false;
  }
  final a = _knownYear(first), b = _knownYear(second);
  final x = _knownKind(first), y = _knownKind(second);
  return (a.isEmpty || b.isEmpty || a == b) &&
      (x.isEmpty || y.isEmpty || x == y);
}

bool ordinaryDetailTitlesCompatible(Drama selected, Drama detail) {
  final selectedTitle = ordinaryTitleKey(selected.title);
  final detailTitle = ordinaryTitleKey(detail.title);
  if (selectedTitle.isNotEmpty && detailTitle.contains(selectedTitle)) {
    return true;
  }
  return selected.source == 'huangguoai' &&
      detailTitle.isNotEmpty &&
      ordinaryTitleKey(_withoutFirstEpisodeSuffix(selected.title)) ==
          detailTitle;
}

List<Drama> ordinarySiteOptions(Drama drama) {
  if (!SourceSite.ordinaryIds.contains(drama.source)) return [drama];
  final choices = <String, Drama>{};
  for (final option in drama.sites.isEmpty ? [drama] : drama.sites) {
    if (SourceSite.ordinaryIds.contains(option.source) &&
        ordinaryTitlesCompatible(drama, option)) {
      choices[option.siteIdentity] = option;
    }
  }
  choices.putIfAbsent(drama.siteIdentity, () => Drama.fromJson(drama.toJson()));
  final order = SourceSite.knownValues.map((site) => site.id).toList();
  return choices.values.toList()..sort((a, b) {
    final source = order.indexOf(a.source).compareTo(order.indexOf(b.source));
    return source != 0 ? source : a.siteIdentity.compareTo(b.siteIdentity);
  });
}

List<Drama> groupOrdinaryTitles(Iterable<Drama> input) {
  final result = <List<Drama>>[];
  final titles = <String, List<List<Drama>>>{};
  for (final item in input) {
    if (!SourceSite.ordinaryIds.contains(item.source)) {
      result.add([item]);
      continue;
    }
    final title = ordinaryTitleKey(item.title);
    if (title.isEmpty || const {'短剧', '电影', '剧集', '未命名'}.contains(title)) {
      result.add([item]);
      continue;
    }
    final groups = titles.putIfAbsent(title, () => []);
    var group = groups
        .where(
          (group) =>
              group.every((member) => ordinaryTitlesCompatible(member, item)),
        )
        .firstOrNull;
    if (group == null) {
      group = <Drama>[];
      groups.add(group);
      result.add(group);
    }
    for (final candidate in ordinarySiteOptions(item)) {
      final at = group.indexWhere(
        (member) => member.siteIdentity == candidate.siteIdentity,
      );
      if (at < 0) {
        group.add(candidate);
      } else {
        group[at] = group[at].merge(candidate);
      }
    }
  }
  return result.map((group) {
    if (!SourceSite.ordinaryIds.contains(group.first.source)) {
      return group.first;
    }
    final choices = ordinarySiteOptions(group.first.withSites(group));
    return choices.first.withSites(choices);
  }).toList();
}

Future<DramaDetail> loadIsolatedSiteDetail(
  AppRepository repository,
  Drama selected,
) async {
  return validateIsolatedSiteDetail(
    selected,
    await repository.detail(selected),
  );
}

DramaDetail validateIsolatedSiteDetail(Drama selected, DramaDetail detail) {
  if (!selected.isolatedDetails) return detail;
  if (selected.legacyUnscopedAI) {
    throw AppFailure('旧目录条目缺少站点身份，请重新更新目录；历史和本地视频保留。', code: 'site_identity');
  }
  final differences = <String>[
    if (detail.drama.id != selected.id) 'id',
    if (detail.drama.source != selected.source) 'source',
    if (detail.drama.siteIdentity != selected.siteIdentity) 'siteIdentity',
    if (!ordinaryDetailTitlesCompatible(selected, detail.drama)) 'title',
  ];
  if (differences.isNotEmpty) {
    throw AppFailure(
      '站点返回的作品与所选剧集不一致（${differences.join('、')}），已阻止混入分集，请刷新目录或切换站点。',
      code: 'site_identity',
    );
  }
  for (final episode in detail.rawEpisodes) {
    if (selected.source == 'huangguoai') {
      final expected = Uri.tryParse(selected.sourceId);
      final actual = Uri.tryParse('${episode.raw['pageUrl'] ?? ''}');
      if (expected == null ||
          actual == null ||
          !expected.hasAuthority ||
          !actual.hasAuthority ||
          expected.pathSegments.length < 2 ||
          actual.pathSegments.length < 2 ||
          !const ['http', 'https'].contains(expected.scheme) ||
          actual.scheme != expected.scheme ||
          actual.host != expected.host ||
          actual.port != expected.port ||
          actual.pathSegments.first != 'video' ||
          actual.pathSegments[1] != expected.pathSegments[1]) {
        throw AppFailure('分集地址不属于所选域名和作品，已阻止混入。', code: 'site_identity');
      }
    }
    final source = episode.raw['source'];
    if (source is String && source.isNotEmpty && source != selected.source) {
      throw AppFailure('详情包含其他站源的分集，已停止加载，请刷新该站点。', code: 'site_identity');
    }
    final prefix = episode.id.split(':').first;
    if (SourceSite.knownValues.any((site) => site.id == prefix) &&
        !episode.id.startsWith('${selected.id}:')) {
      throw AppFailure('详情分集不属于所选作品，已停止加载，请刷新该站点。', code: 'site_identity');
    }
  }
  if (detail.episodes.isEmpty) {
    throw AppFailure('暂时没有可播放的集数', code: 'site_empty');
  }
  final merged = selected.merge(detail.drama);
  final displayTitle = selected.source == 'huangguoai'
      ? _withoutFirstEpisodeSuffix(
          detail.drama.title.isEmpty ? selected.title : detail.drama.title,
        )
      : merged.title;
  final fresh = Drama.fromJson({
    ...merged.toJson(),
    'title': displayTitle,
    'episodes': detail.episodes.length,
    'episodeCountVerified': true,
  }, sites: selected.sites);
  return DramaDetail(fresh, detail.episodes, warning: detail.warning);
}
