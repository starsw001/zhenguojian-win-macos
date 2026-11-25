package core

import (
	"bytes"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestSourceAddressesLoggedWithoutNetworkOrPollingDuplicates(t *testing.T) {
	e := sourceFixtureEngine(t, func(r *http.Request) (*http.Response, error) {
		t.Fatal("address logging must not request the network")
		return nil, nil
	})
	e.sourceRecords[sourceHuangguoAI] = nativeSourceRecord{CatalogOrigin: "https://catalog.example.invalid"}
	registry := e.downloader.huangguoMirrorStore()
	registry.mu.Lock()
	registry.disk.Mirrors = []huangguoMirrorRecord{{Origin: "https://mirror.example.invalid", Kind: sourceHuangguoAI, EffectiveOrigin: "https://redirect.example.invalid", AddedFrom: "https://publisher.example.invalid", DuplicateOf: "https://original.example.invalid"}}
	registry.disk.Mirrors = append(registry.disk.Mirrors, huangguoMirrorRecord{Origin: "https://original.example.invalid", Kind: sourceHuangguoAI, EffectiveOrigin: "https://redirect.example.invalid"})
	registry.disk.Publications = []huangguoPublicationStatus{{URL: "https://publication.example.invalid"}}
	registry.mu.Unlock()
	status := e.sourceStatus(sourceHuangguoAI)
	path := e.downloader.diagnostics.filePath()
	first, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, address := range []string{"catalog", "mirror", "redirect", "publisher", "original", "publication"} {
		if !strings.Contains(string(first), "https://"+address+".example.invalid") {
			t.Fatal(address, string(first))
		}
	}
	if status.CatalogOrigin != "https://catalog.example.invalid" || status.Mirrors[0].Origin != "https://mirror.example.invalid" {
		t.Fatal(status)
	}
	e.sourceStatus(sourceHuangguoAI)
	second, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(first, second) {
		t.Fatal("polling duplicated address snapshot", err)
	}
	record := e.sourceRecords[sourceHuangguoAI]
	record.StartedAt = time.Now()
	e.sourceRecords[sourceHuangguoAI] = record
	e.sourceStatus(sourceHuangguoAI)
	third, err := os.ReadFile(path)
	if err != nil || len(third) <= len(first) {
		t.Fatal("new check must write addresses", err)
	}
	custom := filepath.Join(t.TempDir(), "custom-logs")
	e.downloader.diagnostics.setDirectory(custom)
	e.sourceStatus(sourceHuangguoAI)
	if body, err := os.ReadFile(filepath.Join(custom, "app.log")); err != nil || !strings.Contains(string(body), "source.address") {
		t.Fatal(err, string(body))
	}
	t.Log("source.address: catalog, mirror, effective origin, added-from, duplicate and publication present in app.log; unchanged polls deduplicated; new checks and current log directory preserved")
}
