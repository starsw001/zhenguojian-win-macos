import 'package:flutter/material.dart';

import 'core_bridge.dart';
import 'local_store.dart';
import 'models.dart';
import 'ordinary_catalog.dart';

List<Drama> visibleOrdinarySites(
  Drama group,
  AppRepository repository,
  LocalStore store,
) {
  if (!SourceSite.ordinaryIds.contains(group.source)) return [];
  final visible = store.visibleSources.map((site) => site.id).toSet();
  final candidates = ordinarySiteOptions(group);
  for (final site in repository.catalogUpdates.knownOrdinarySites) {
    if (candidates.every((item) => ordinaryTitlesCompatible(item, site))) {
      candidates.add(site);
    }
  }
  return ordinarySiteOptions(group.withSites(candidates))
      .where(
        (site) =>
            SourceSite.ordinaryIds.contains(site.source) &&
            visible.contains(site.source) &&
            store.allowsSource(site.source),
      )
      .map(repository.catalogUpdates.current)
      .toList();
}

class OrdinarySiteSelector extends StatelessWidget {
  const OrdinarySiteSelector({
    super.key,
    required this.sites,
    required this.selected,
    required this.onSelected,
  });
  final List<Drama> sites;
  final Drama selected;
  final ValueChanged<Drama> onSelected;
  @override
  Widget build(BuildContext context) => Padding(
    padding: const EdgeInsets.fromLTRB(12, 8, 12, 4),
    child: Align(
      alignment: Alignment.centerLeft,
      child: ConstrainedBox(
        constraints: const BoxConstraints(maxHeight: 120),
        child: SingleChildScrollView(
          child: Wrap(
            spacing: 8,
            runSpacing: 6,
            children: [
              for (final entry in sites.indexed)
                Tooltip(
                  message:
                      '${SourceSite.byId(entry.$2.source).name}${Uri.tryParse(entry.$2.sourceId)?.hasAuthority == true ? ' · ${Uri.parse(entry.$2.sourceId).host}' : ''} · ${entry.$2.title}',
                  child: ChoiceChip(
                    key: ValueKey('ordinary-site-${entry.$1 + 1}'),
                    label: Text('站点${entry.$1 + 1}'),
                    selected: entry.$2.siteIdentity == selected.siteIdentity,
                    onSelected: (value) {
                      if (value) onSelected(entry.$2);
                    },
                  ),
                ),
            ],
          ),
        ),
      ),
    ),
  );
}
