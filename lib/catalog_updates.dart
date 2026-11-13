import 'package:flutter/foundation.dart';

import 'models.dart';

class CatalogUpdates extends ChangeNotifier {
  final _items = <String, Drama>{};
  final _ordinary = <String, Drama>{};
  final _coverRetries = <String, int>{};
  Drama? latest;

  String _key(Drama drama) =>
      drama.isolatedDetails ? drama.siteIdentity : drama.id;

  Drama current(Drama drama) =>
      _items[_key(drama)] == null ? drama : drama.merge(_items[_key(drama)]!);

  int coverRevision(String id) => _coverRetries[id] ?? 0;

  Iterable<Drama> get knownOrdinarySites => _ordinary.values;

  void rememberOrdinary(Iterable<Drama> dramas) {
    for (final drama in dramas) {
      if (!SourceSite.ordinaryIds.contains(drama.source)) continue;
      for (final site in drama.sites.isEmpty ? [drama] : drama.sites) {
        if (!SourceSite.ordinaryIds.contains(site.source)) continue;
        final previous = _ordinary.remove(site.siteIdentity);
        _ordinary[site.siteIdentity] = previous?.merge(site) ?? site;
      }
    }
    while (_ordinary.length > 4096) {
      _ordinary.remove(_ordinary.keys.first);
    }
  }

  void publish(Drama drama, {bool retryCover = false}) {
    final previous = _items[_key(drama)];
    latest = previous?.merge(drama) ?? drama;
    _items[_key(drama)] = latest!;
    rememberOrdinary([latest!]);
    if (retryCover) _coverRetries[drama.id] = coverRevision(drama.id) + 1;
    notifyListeners();
  }
}
