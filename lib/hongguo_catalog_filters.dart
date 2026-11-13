import 'package:flutter/material.dart';

import 'catalog_filters.dart';
import 'models.dart';

const hongguoWebCategories = [
  CatalogCategory('web:real-drama', '真人剧'),
  CatalogCategory('web:comic-drama', '漫剧'),
  CatalogCategory('web:ai-drama', 'AI剧'),
  CatalogCategory('web:comic', '漫画'),
];

String hongguoWebSelection(String? value) {
  if (value != null &&
      hongguoWebCategories.any(
        (entry) => value == entry.id || value.startsWith('${entry.id}:'),
      )) {
    return value;
  }
  return switch (value) {
    'comic_series' || 'category:漫剧' => 'web:comic-drama',
    'ai_series' || 'category:AI 剧' || 'category:AI剧' => 'web:ai-drama',
    'category:漫画' => 'web:comic',
    _ => 'web:real-drama',
  };
}

class HongguoCatalogFilters extends StatelessWidget {
  const HongguoCatalogFilters({
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

  @override
  Widget build(BuildContext context) {
    final selected = hongguoWebSelection(category);
    final root = hongguoWebCategories.firstWhere(
      (entry) => selected == entry.id || selected.startsWith('${entry.id}:'),
    );
    final topics = categories
        .where((entry) => entry.id.startsWith('${root.id}:'))
        .toList();
    return Column(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        CatalogFilters(
          key: const ValueKey('hongguo-types'),
          categories: hongguoWebCategories,
          category: root.id,
          onCategory: onCategory,
          onRetry: onRetry,
          error: error,
        ),
        CatalogFilters(
          key: ValueKey('hongguo-topics-${root.id}'),
          categories: [CatalogCategory(root.id, '全部'), ...topics],
          category: selected,
          onCategory: onCategory,
          onRetry: onRetry,
        ),
      ],
    );
  }
}
