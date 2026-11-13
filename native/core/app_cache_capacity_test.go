package core

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestNativeCacheCapacityDoubled 验证「Native Cache Capacity Doubled」回归场景。
func TestNativeCacheCapacityDoubled(t *testing.T) {
	if nativeCatalogMaxBytes != 256<<20 || nativeSourceMaxBytes != 8<<20 ||
		nativeCoverCacheBytes != 512<<20 || nativeCoverCacheEntries != 4000 ||
		rankingCacheMaxBytes != 16<<20 || rankingCacheMaxPages != 256 ||
		hongguoSuggestionCacheLimit != 256 || nativePreloadLimit != 8<<20 {
		t.Fatal("cache limits are not the agreed doubled capacities")
	}
}

// TestNativeExpandedSourceRecordLoadsWithoutLosingHistory 验证「Native Expanded Source Record Loads Without Losing History」回归场景。
func TestNativeExpandedSourceRecordLoadsWithoutLosingHistory(t *testing.T) {
	directory := t.TempDir()
	stage := strings.Repeat("x", 5<<20)
	records := map[string]nativeSourceRecord{sourceGuipian: {Stage: stage}}
	body, err := json.Marshal(records)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(directory, "sources.json")
	if err := os.WriteFile(path, body, 0600); err != nil {
		t.Fatal(err)
	}
	engine := &nativeEngine{directory: directory}
	engine.loadSourceRecords()
	if engine.sourceRecords[sourceGuipian].Stage != stage {
		t.Fatal("a valid record above the old 4 MiB ceiling was not retained")
	}
	after, err := os.ReadFile(path)
	if err != nil || string(after) != string(body) {
		t.Fatal("loading changed the saved source record", err)
	}
}

// TestNativeCacheLoadRejectsNonRegularFile 验证「Native Cache Load Rejects Non Regular File」回归场景。
func TestNativeCacheLoadRejectsNonRegularFile(t *testing.T) {
	directory := t.TempDir()
	for _, name := range []string{"catalogs.json", "sources.json", "rankings.json"} {
		if err := os.Mkdir(filepath.Join(directory, name), 0700); err != nil {
			t.Fatal(err)
		}
	}
	engine := &nativeEngine{directory: directory}
	engine.loadCatalogCache()
	engine.loadSourceRecords()
	downloader := &Downloader{cfg: Config{dataDir: directory}}
	downloader.loadRankingCache()
	if len(engine.catalogs) != 0 || len(engine.sourceRecords) != 0 || len(downloader.rankings.pages) != 0 {
		t.Fatal("non-regular cache files produced data")
	}
}
