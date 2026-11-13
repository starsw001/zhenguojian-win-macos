import 'dart:math';

import 'core_bridge.dart';
import 'models.dart';
import 'ordinary_catalog.dart';

String categoryName(String name) {
  final compact = name.trim().replaceAll(RegExp(r'\s+'), '');
  return switch (compact) {
    'AI成人短剧' || 'AI短剧' => 'AI 短剧',
    'AI成人漫剧' || 'AI漫剧' => 'AI 漫剧',
    'AI换脸' => 'AI 换脸',
    'AI魔改' => 'AI 魔改',
    'AI剧' => 'AI 剧',
    '' => '未分类',
    _ => name.trim(),
  };
}

List<String> _categoryParts(String name, {int maximumLength = 48}) => name
    .split(RegExp(r'[,，、;；|]'))
    .map((part) => part.trim())
    .where(
      (part) =>
          part.isNotEmpty &&
          part.runes.length <= maximumLength &&
          !RegExp(r'[\x00-\x1f\x7f]').hasMatch(part),
    )
    .toSet()
    .take(24)
    .toList();

List<String> dramaCategoryNames(Drama drama) {
  final names = _categoryParts(drama.category)
      .map(categoryName)
      .toSet()
      .toList();
  return names.isEmpty ? ['未分类'] : names;
}

class _CatalogChoice {
  _CatalogChoice(this.category);
  final CatalogCategory category;
  final requests = <String, String>{};
}

class _CatalogEntry {
  List<Drama> items = [];
  int page = 0;
  int nextPage = 1;
  bool hasMore = true;
  bool fresh = false;
}

class _CatalogSession {
  final entries = <String, _CatalogEntry>{};
  int generation = 0;
}

class CatalogBrowser {
  CatalogBrowser(this.repository);
  final AppRepository repository;
  final _menus = <String, List<CatalogCategory>>{};
  final _library = <String, Map<String, Drama>>{};
  final _sessions = <String, _CatalogSession>{};
  int _generation = 0;

  Future<void> cancel() {
    _generation++;
    for (final session in _sessions.values) {
      session.generation++;
    }
    return repository.cancelCatalog();
  }

  String _itemKey(Drama drama) => SourceSite.ordinaryIds.contains(drama.source)
      ? drama.siteIdentity
      : drama.id;

  void _remember(String source, Iterable<Drama> items) {
    repository.catalogUpdates.rememberOrdinary(items);
    final library = _library.putIfAbsent(source, () => {});
    for (final drama in items) {
      if (drama.source == source) {
        library[_itemKey(drama)] =
            library[_itemKey(drama)]?.merge(drama) ?? drama;
      }
    }
  }

  void updateDrama(Drama drama) {
    updateDramas([drama]);
  }

  void updateDramas(Iterable<Drama> dramas) {
    final updates = {for (final drama in dramas) _itemKey(drama): drama};
    for (final drama in updates.values) {
      final library = _library[drama.source];
      if (library?.containsKey(_itemKey(drama)) == true) {
        library![_itemKey(drama)] = library[_itemKey(drama)]!.merge(drama);
      }
    }
    for (final session in _sessions.values) {
      for (final entry in session.entries.values) {
        entry.items = [
          for (final item in entry.items)
            updates[_itemKey(item)] == null
                ? item
                : item.merge(updates[_itemKey(item)]!),
        ];
      }
    }
  }

  Future<void> _each<T>(List<T> entries, Future<void> Function(T) visit) async {
    var index = 0;
    Future<void> worker() async {
      while (index < entries.length) {
        final entry = entries[index++];
        await visit(entry);
      }
    }

    await Future.wait(List.generate(min(2, entries.length), (_) => worker()));
  }

  Future<String?> loadCategories(
    SourceGroup group, {
    bool force = false,
    bool cacheOnly = false,
  }) async {
    final failures = <String>[];
    await _each(group.sources, (source) async {
      try {
        final cached = await repository.cached(source.id);
        _remember(source.id, cached.items);
      } catch (_) {}
      if (cacheOnly) {
        try {
          final categories = await repository.cachedCategories(source.id);
          if (categories.isNotEmpty) {
            _menus[source.id] = categories;
          }
        } catch (_) {}
        return;
      }
      try {
        _menus[source.id] = await repository.categories(
          source.id,
          force: force || source.ordinary,
        );
      } catch (_) {
        failures.add(source.groupName);
      }
    });
    return failures.isEmpty ? null : '部分分类暂未加载，点击重试';
  }

  List<_CatalogChoice> _choices(SourceGroup group) {
    final choices = <String, _CatalogChoice>{};
    for (final source in group.sources) {
      for (final category in _menus[source.id] ?? const <CatalogCategory>[]) {
        if (category.id.isEmpty) continue;
        if (source.id == 'huangju') {
          final parts = _categoryParts(category.name, maximumLength: 24);
          if (parts.length != 1 || parts.single != category.name.trim()) {
            continue;
          }
        }
        final hongguoWeb =
            source.id == 'hongguo' && category.id.startsWith('web:');
        final name = category.tag || hongguoWeb
            ? category.name.trim()
            : categoryName(category.name);
        final key = hongguoWeb
            ? category.id
            : category.tag
            ? 'tag:${source.id}:${category.id}'
            : name;
        final choice = choices.putIfAbsent(
          key,
          () => _CatalogChoice(
            CatalogCategory(
              hongguoWeb || category.tag ? key : 'category:$name',
              name,
              tag: category.tag,
            ),
          ),
        );
        choice.requests[source.id] = category.id;
      }
    }
    final names = {
      for (final source in group.sources)
        for (final item in _library[source.id]?.values ?? const <Drama>[])
          ...dramaCategoryNames(item),
    }.toList()..sort();
    for (final name in names) {
      choices.putIfAbsent(
        name,
        () => _CatalogChoice(CatalogCategory('local:$name', name, local: true)),
      );
    }
    return choices.values.toList();
  }

  List<CatalogCategory> categories(SourceGroup group) => [
    CatalogCategory.all,
    for (final choice in _choices(group)) choice.category,
  ];

  _CatalogChoice? _choice(SourceGroup group, String category) =>
      _choices(group)
          .where((choice) => choice.category.id == category)
          .firstOrNull;

  Future<CatalogPage> homeFallback(
    SourceGroup selected,
    Iterable<SourceSite> visible, {
    bool cacheOnly = false,
  }) async {
    final generation = _generation;
    final selectedIds = selected.sources.map((source) => source.id).toSet();
    final candidates = visible
        .where(
          (source) =>
              !selectedIds.contains(source.id) &&
              source.ordinary &&
              !SourceSite.isRestricted(source.id),
        )
        .take(2)
        .toList();
    final rows = <String, List<Drama>>{};
    Future<void> collect(bool cached) async {
      await _each(candidates, (source) async {
        if (generation != _generation) return;
        try {
          final result = cached
              ? await repository.cached(source.id)
              : await repository.catalog(source.id, page: 1);
          if (generation != _generation) return;
          final items = result.items
              .where((item) => item.source == source.id)
              .take(12)
              .toList();
          if (items.isNotEmpty) rows[source.id] = items;
        } catch (_) {}
      });
    }

    await collect(true);
    if (rows.isEmpty && !cacheOnly && generation == _generation) {
      await collect(false);
    }
    if (generation != _generation || rows.isEmpty) return CatalogPage([]);
    final items = <String, Drama>{};
    for (final source in candidates) {
      for (final item in rows[source.id] ?? const <Drama>[]) {
        items.putIfAbsent(_itemKey(item), () => item);
      }
    }
    final names = candidates
        .where((source) => rows.containsKey(source.id))
        .map((source) => source.name)
        .join('、');
    return CatalogPage(
      items.values.toList(),
      warning: '当前频道暂未加载内容，暂展示 $names 的剧集',
    );
  }

  Future<CatalogPage> load(
    SourceGroup group, {
    String category = '',
    String query = '',
    bool more = false,
    bool force = false,
    bool useCache = false,
    bool cacheOnly = false,
    void Function(CatalogPage)? onCached,
  }) async {
    if (cacheOnly && (more || force || query.trim().isNotEmpty)) {
      throw AppFailure('缓存读取不能同时请求搜索或续页');
    }
    final request = ++_generation;
    for (final session in _sessions.values) {
      session.generation++;
    }
    await repository.cancelCatalog();
    if (request != _generation) throw AppFailure('已取消加载');
    final choice = query.isEmpty ? _choice(group, category) : null;
    final hongguoWeb =
        group.id == 'hongguo' && query.isEmpty && category.startsWith('web:');
    final requests = hongguoWeb
        ? {'hongguo': category}
        : choice != null && !choice.category.local
        ? choice.requests
        : {for (final source in group.sources) source.id: ''};
    final key =
        '${group.sources.map((s) => s.id).join(',')}|'
        '${hongguoWeb || choice?.category.local == false ? category : ''}|$query';
    final session = _sessions.putIfAbsent(key, _CatalogSession.new);
    final generation = ++session.generation;
    final failures = <String, String>{};
    final sourceOrder = requests.keys.toList();
    final refreshHead =
        !cacheOnly && group.sources.any((source) => source.refreshHeadOnOpen);
    for (final source in sourceOrder) {
      session.entries.putIfAbsent(source, _CatalogEntry.new);
    }

    CatalogPage snapshot() {
      final rows = sourceOrder
          .map((source) => session.entries[source]!.items)
          .toList();
      final items = <String, Drama>{};
      final count = rows.fold<int>(0, (count, row) => max(count, row.length));
      for (var index = 0; index < count; index++) {
        for (final row in rows) {
          if (index < row.length) items[_itemKey(row[index])] = row[index];
        }
      }
      if (choice?.category.local == true) {
        for (final source in group.sources) {
          for (final item in _library[source.id]?.values ?? const <Drama>[]) {
            if (dramaCategoryNames(item).contains(choice!.category.name)) {
              items.putIfAbsent(_itemKey(item), () => item);
            }
          }
        }
      }
      return CatalogPage(
        groupOrdinaryTitles(
          items.values
              .map(repository.catalogUpdates.current)
              .where(
                (item) =>
                    choice?.category.local != true ||
                    dramaCategoryNames(item).contains(choice!.category.name),
              )
              .toList(),
        ),
        hasMore: sourceOrder.any((source) => session.entries[source]!.hasMore),
        page: sourceOrder.fold<int>(
          1,
          (page, source) => max(page, session.entries[source]!.page),
        ),
        fresh: sourceOrder.every((source) => session.entries[source]!.fresh),
        warning: failures.values.toSet().join('；'),
      );
    }

    if ((useCache || cacheOnly) &&
        (!force || refreshHead) &&
        !more &&
        query.isEmpty) {
      await _each(sourceOrder, (source) async {
        try {
          final cached = await repository.cached(
            source,
            category: requests[source]!,
          );
          if (generation != session.generation || cached.items.isEmpty) return;
          final entry = session.entries[source]!;
          entry.items = cached.items;
          entry.page = cached.page;
          entry.nextPage = cached.page + 1;
          entry.hasMore = cached.hasMore || cached.warning.isNotEmpty;
          entry.fresh = cached.fresh && cached.warning.isEmpty;
          if (cached.warning.isNotEmpty) failures[source] = cached.warning;
          _remember(source, cached.items);
        } catch (error) {
          if (cacheOnly) failures[source] = error.toString();
        }
      });
      if (generation != session.generation) return snapshot();
      final cached = snapshot();
      if (cached.items.isNotEmpty) {
        onCached?.call(
          CatalogPage(
            cached.items,
            hasMore: cached.hasMore,
            page: cached.page,
            warning: cached.warning,
            fresh: cached.fresh && !refreshHead,
          ),
        );
      }
      if (cacheOnly) return cached;
      if (cached.fresh && cached.items.isNotEmpty && !refreshHead) {
        return cached;
      }
    }

    await _each(sourceOrder, (source) async {
      if (generation != session.generation) return;
      final entry = session.entries[source]!;
      if (more && !entry.hasMore) return;
      final refreshSourceHead =
          !more && query.isEmpty && SourceSite.byId(source).refreshHeadOnOpen;
      if (useCache && !force && !more && entry.fresh && !refreshSourceHead) {
        return;
      }
      final page = more ? entry.nextPage : 1;
      try {
        final result = await repository.catalog(
          source,
          category: requests[source]!,
          query: query,
          page: page,
          force: force || refreshSourceHead,
        );
        if (generation != session.generation) return;
        final items = <String, Drama>{
          if (more)
            for (final item in entry.items) _itemKey(item): item,
          for (final item in result.items) _itemKey(item): item,
        };
        final oldPage = entry.page, oldNext = entry.nextPage;
        final oldHasMore = entry.hasMore;
        final hadOldItems = entry.items.isNotEmpty;
        if (refreshSourceHead && !result.authoritative) {
          for (final item in entry.items) {
            items.putIfAbsent(_itemKey(item), () => item);
          }
        }
        entry.items = items.values.toList();
        entry.page = result.page;
        final indexedSearch = query.isNotEmpty && result.localSearch;
        entry.nextPage = result.warning.isEmpty || indexedSearch
            ? (result.nextPage > result.page
                  ? result.nextPage
                  : result.page + 1)
            : page;
        if (refreshSourceHead &&
            !result.authoritative &&
            oldPage > result.page) {
          entry.page = oldPage;
          entry.nextPage = oldNext;
        }
        entry.hasMore =
            (query.isEmpty || SourceSite.byId(source).pagedSearch) &&
            (result.hasMore ||
                result.warning.isNotEmpty &&
                    !indexedSearch &&
                    (query.isEmpty || source == 'huangguoai') ||
                refreshSourceHead &&
                    !result.authoritative &&
                    hadOldItems &&
                    oldHasMore);
        entry.fresh = result.fresh && result.warning.isEmpty;
        if (query.isEmpty) _remember(source, result.items);
        if (result.warning.isNotEmpty) {
          failures[source] = result.warning;
        } else {
          failures.remove(source);
        }
      } catch (error) {
        if (generation != session.generation) return;
        entry.nextPage = page;
        entry.hasMore = query.isEmpty || SourceSite.byId(source).pagedSearch;
        entry.fresh = false;
        failures[source] = error.toString();
      }
    });
    final result = snapshot();
    if (result.items.isEmpty && failures.isNotEmpty) {
      throw AppFailure(result.warning);
    }
    return result;
  }
}
