import 'models.dart';

class RankingBoard {
  const RankingBoard({
    required this.id,
    required this.source,
    required this.name,
    this.description = '',
    this.categoryId = '',
    this.categoryName = '',
  });
  final String id;
  final String source;
  final String name;
  final String description;
  final String categoryId, categoryName;
  String get groupId => SourceSite.byId(source).groupId;
  static String sourceForID(String id) => switch (id) {
    'hongguo-hot' ||
    'hongguo-real' ||
    'hongguo-comic' ||
    'hongguo-ai' => 'hongguo',
    'huangdou-all' ||
    'huangdou-mogai' ||
    'huangdou-search' ||
    'huangdou-favorite' ||
    'huangdou-finish' => 'huangdou',
    'huangguo-hot' ||
    'huangguo-recommend' ||
    'huangguo-potential' => 'huangguoai',
    'huangju-hot' || 'huangju-new' => 'huangju',
    'yeguo-recommend' => 'yeguo',
    'dsd-catalog' => 'dsd',
    _ =>
      RegExp(r'^hongguo-web-[a-z][a-z-]{1,64}$').hasMatch(id) ? 'hongguo' : '',
  };
  factory RankingBoard.fromJson(Map<String, dynamic> json) => RankingBoard(
    id: json['id'] as String? ?? '',
    source:
        json['source'] as String? ?? sourceForID(json['id'] as String? ?? ''),
    name: json['name'] as String? ?? '',
    description: json['description'] as String? ?? '',
    categoryId: json['categoryId'] as String? ?? '',
    categoryName: json['categoryName'] as String? ?? '',
  );
}

class RankingItem {
  const RankingItem(
    this.rank,
    this.drama, {
    this.metric = '',
    this.status = '',
    this.reservationOnly = false,
  });
  final int rank;
  final Drama drama;
  final String metric, status;
  final bool reservationOnly;
  factory RankingItem.fromJson(Map<String, dynamic> json) => RankingItem(
    intValue(json['rank']),
    Drama.fromJson(Map<String, dynamic>.from(json['drama'] as Map)),
    metric: json['metric'] as String? ?? '',
    status: json['status'] as String? ?? '',
    reservationOnly: json['reservationOnly'] == true,
  );
}

class RankingPage {
  const RankingPage({
    required this.items,
    this.page = 1,
    this.hasMore = false,
    this.warning = '',
    this.updatedText = '',
    this.stale = false,
  });
  final List<RankingItem> items;
  final int page;
  final bool hasMore;
  final String warning;
  final String updatedText;
  final bool stale;
  factory RankingPage.fromJson(Map<String, dynamic> json) => RankingPage(
    items: [
      for (final row in json['items'] as List? ?? [])
        RankingItem.fromJson(Map<String, dynamic>.from(row as Map)),
    ],
    page: intValue(json['page']),
    hasMore: json['hasMore'] == true,
    warning: json['warning'] as String? ?? '',
    updatedText: json['updatedText'] as String? ?? '',
    stale: json['stale'] == true,
  );
}
