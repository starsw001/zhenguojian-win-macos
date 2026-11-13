import 'dart:math';

import 'package:flutter/material.dart';

import 'app_layout.dart';
import 'models.dart';
import 'remote_widgets.dart';

class CatalogFilters extends StatefulWidget {
  const CatalogFilters({
    super.key,
    required this.categories,
    required this.category,
    required this.onCategory,
    required this.onRetry,
    this.error,
    this.trailing,
  });

  final List<CatalogCategory> categories;
  final String category;
  final String? error;
  final ValueChanged<String> onCategory;
  final VoidCallback onRetry;
  final Widget? trailing;

  @override
  State<CatalogFilters> createState() => _CatalogFiltersState();
}

class _CatalogFiltersState extends State<CatalogFilters> {
  final _anchors = <String, GlobalKey>{};

  @override
  void didUpdateWidget(covariant CatalogFilters oldWidget) {
    super.didUpdateWidget(oldWidget);
    final ids = widget.categories.map((entry) => entry.id).toSet();
    _anchors.removeWhere((id, _) => !ids.contains(id));
    if (oldWidget.category != widget.category) {
      WidgetsBinding.instance.addPostFrameCallback((_) {
        final anchor = _anchors[widget.category]?.currentContext;
        if (mounted && anchor != null) {
          Scrollable.ensureVisible(
            anchor,
            alignment: .4,
            duration: const Duration(milliseconds: 180),
          );
        }
      });
    }
  }

  @override
  Widget build(BuildContext context) {
    final television = AppLayout.isTelevision(context);
    final tags = widget.categories.where((entry) => entry.tag).toList();
    final selectedTag = tags.any((entry) => entry.id == widget.category);
    Widget chip(CatalogCategory entry) => Padding(
      key: _anchors.putIfAbsent(entry.id, GlobalKey.new),
      padding: EdgeInsets.zero,
      child: television
          ? RemoteButton(
              key: ValueKey('category-${entry.id}'),
              label: entry.name,
              selected:
                  entry.id == widget.category ||
                  entry.id.isEmpty && selectedTag,
              onPressed: () => widget.onCategory(
                entry.tag && entry.id == widget.category ? '' : entry.id,
              ),
            )
          : ChoiceChip(
              key: ValueKey('category-${entry.id}'),
              label: Text(entry.name),
              selected:
                  entry.id == widget.category ||
                  entry.id.isEmpty && selectedTag,
              showCheckmark: false,
              onSelected: (_) => widget.onCategory(
                entry.tag && entry.id == widget.category ? '' : entry.id,
              ),
            ),
    );
    final maximumHeight = max(
      television ? 72.0 : 52.0,
      min(320.0, MediaQuery.sizeOf(context).height * .32),
    );
    return Row(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        Expanded(
          child: ConstrainedBox(
            constraints: BoxConstraints(maxHeight: maximumHeight),
            child: SingleChildScrollView(
              key: const ValueKey('catalog-categories'),
              padding: const EdgeInsets.symmetric(horizontal: 12, vertical: 4),
              child: Column(
                crossAxisAlignment: CrossAxisAlignment.start,
                children: [
                  Wrap(
                    spacing: 6,
                    runSpacing: 6,
                    children: [
                      for (final entry in widget.categories)
                        if (!entry.tag) chip(entry),
                    ],
                  ),
                  if (tags.isNotEmpty) ...[
                    Padding(
                      padding: const EdgeInsets.only(top: 6, bottom: 2),
                      child: Text(
                        '热门标签',
                        style: Theme.of(context).textTheme.labelMedium,
                      ),
                    ),
                    Wrap(
                      key: const ValueKey('catalog-popular-tags'),
                      spacing: 6,
                      runSpacing: 6,
                      children: [for (final entry in tags) chip(entry)],
                    ),
                  ],
                ],
              ),
            ),
          ),
        ),
        if (widget.error != null)
          IconButton(
            tooltip: widget.error,
            onPressed: widget.onRetry,
            icon: Icon(
              Icons.refresh_rounded,
              color: Theme.of(context).colorScheme.error,
            ),
          ),
        if (widget.trailing != null) widget.trailing!,
      ],
    );
  }
}
