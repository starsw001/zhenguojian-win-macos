import 'package:flutter/material.dart';

import 'catalog_filters.dart';
import 'models.dart';

const huangguoWebsiteCategories = [
  CatalogCategory.all,
  CatalogCategory('original', '黄果原创'),
  CatalogCategory('ai-duanju', '短剧'),
  CatalogCategory('ai-manju', '漫剧'),
  CatalogCategory('topics', '专题'),
  CatalogCategory('ranks-hot', '排行榜'),
];

String huangguoWebsiteRoot(String value) {
  if (value == 'original' || value.startsWith('original-')) return 'original';
  if (value == 'ai-duanju' || value.startsWith('ai-duanju-')) {
    return 'ai-duanju';
  }
  if (value == 'ai-manju' || value.startsWith('ai-manju-')) {
    return 'ai-manju';
  }
  if (value == 'topics' || value.startsWith('topic:')) return 'topics';
  if (value.startsWith('ranks-')) return 'ranks-hot';
  return '';
}

class HuangguoCatalogFilters extends StatelessWidget {
  const HuangguoCatalogFilters({
    super.key,
    required this.categories,
    required this.category,
    required this.onCategory,
    required this.onRetry,
    this.error,
  });

  final List<CatalogCategory> categories;
  final String category;
  final ValueChanged<String> onCategory;
  final VoidCallback onRetry;
  final String? error;

  List<CatalogCategory> _children(String root) {
    final defaults = switch (root) {
      'original' => const [CatalogCategory('original', '全部原创')],
      'ai-duanju' => const [CatalogCategory('ai-duanju', '最近更新')],
      'ai-manju' => const [CatalogCategory('ai-manju', '最近更新')],
      'topics' => const [CatalogCategory('topics', '全部专题')],
      'ranks-hot' => const [CatalogCategory('ranks-hot', '热播榜')],
      _ => const <CatalogCategory>[],
    };
    bool belongs(CatalogCategory entry) => switch (root) {
      'original' => entry.id.startsWith('original-'),
      'ai-duanju' => entry.id.startsWith('ai-duanju-'),
      'ai-manju' => entry.id.startsWith('ai-manju-'),
      'topics' => entry.id.startsWith('topic:'),
      'ranks-hot' => entry.id.startsWith('ranks-') && entry.id != 'ranks-hot',
      _ => false,
    };
    return [...defaults, ...categories.where(belongs)];
  }

  @override
  Widget build(BuildContext context) {
    final root = huangguoWebsiteRoot(category);
    final tags = categories.where((entry) => entry.tag).toList();
    final details = [..._children(root), ...tags];
    return Column(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        CatalogFilters(
          key: const ValueKey('huangguo-sections'),
          categories: huangguoWebsiteCategories,
          category: root,
          onCategory: onCategory,
          onRetry: onRetry,
          error: error,
        ),
        if (details.isNotEmpty)
          CatalogFilters(
            key: ValueKey('huangguo-details-$root'),
            categories: details,
            category: category,
            onCategory: onCategory,
            onRetry: onRetry,
          ),
      ],
    );
  }
}
