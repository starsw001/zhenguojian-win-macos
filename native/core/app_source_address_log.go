package core

import (
	"encoding/json"
	"strconv"
)

func (d *Downloader) recordSourceAddresses(status nativeSourceStatus) {
	if d == nil || d.diagnostics == nil {
		return
	}
	entries := []map[string]string{}
	if status.CatalogOrigin != "" {
		entries = append(entries, map[string]string{"kind": "catalog", "origin": status.CatalogOrigin})
	}
	for index, mirror := range status.Mirrors {
		entries = append(entries, map[string]string{"kind": "mirror", "line": strconv.Itoa(index + 1), "origin": mirror.Origin, "effectiveOrigin": mirror.EffectiveOrigin, "addedFrom": mirror.AddedFrom, "duplicateOf": mirror.DuplicateOf})
	}
	for index, publication := range status.MirrorPublications {
		entries = append(entries, map[string]string{"kind": "publication", "line": strconv.Itoa(index + 1), "url": publication.URL})
	}
	if len(entries) == 0 {
		return
	}
	body, err := json.Marshal(entries)
	if err != nil {
		return
	}
	snapshot := status.StartedAt.String() + string(body)
	if status.Health != nil {
		snapshot += status.Health.CheckedAt.String()
	}
	log := d.diagnostics
	log.mu.Lock()
	if log.sourceAddressSnapshots == nil {
		log.sourceAddressSnapshots = map[string]string{}
	}
	if log.sourceAddressSnapshots[status.Source] == snapshot {
		log.mu.Unlock()
		return
	}
	log.sourceAddressSnapshots[status.Source] = snapshot
	log.mu.Unlock()
	for _, entry := range entries {
		message, err := json.Marshal(entry)
		if err == nil {
			d.recordDiagnostic(diagnosticEvent{Level: "info", Event: "source.address", Source: status.Source, Message: string(message)})
		}
	}
}
