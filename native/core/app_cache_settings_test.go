package core

import (
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// TestCacheSettingsAllBoundsAndPartialDefaults 验证「Cache Settings All Bounds And Partial Defaults」回归场景。
func TestCacheSettingsAllBoundsAndPartialDefaults(t *testing.T) {
	for _, spec := range nativeCacheSpecs {
		if defaultNativeCacheLimits()[spec.key] != spec.minimum*2 {
			t.Fatal(spec.key)
		}
		for _, value := range []int{spec.minimum, spec.minimum * 8} {
			limits := defaultNativeCacheLimits()
			limits[spec.key] = value
			if limits.validate() != nil {
				t.Fatal("valid bound rejected", spec.key, value)
			}
		}
		for _, value := range []int{spec.minimum - 1, spec.minimum*8 + 1} {
			limits := defaultNativeCacheLimits()
			limits[spec.key] = value
			if limits.validate() == nil {
				t.Fatal("invalid bound accepted", spec.key, value)
			}
		}
	}
	var limits nativeCacheLimits
	if json.Unmarshal([]byte(`{"catalogMiB":512}`), &limits) != nil || len(limits) != 10 || limits["catalogMiB"] != 512 || limits["playerMiB"] != 64 {
		t.Fatal(limits)
	}
	for _, body := range []string{`{"catalogMiB":"512"}`, `{"catalogMiB":1e100}`, `{"unknown":1}`} {
		if json.Unmarshal([]byte(body), &limits) == nil {
			t.Fatal("invalid cache JSON accepted", body)
		}
	}
}

// TestCacheSettingsLegacyPersistenceAndUsageAreReadOnly 验证「Cache Settings Legacy Persistence And Usage Are Read Only」回归场景。
func TestCacheSettingsLegacyPersistenceAndUsageAreReadOnly(t *testing.T) {
	engine := sourceFixtureEngine(t, func(*http.Request) (*http.Response, error) {
		t.Error("cache settings must not request sources")
		return nil, errors.New("network forbidden")
	})
	file := filepath.Join(engine.directory, "resource-settings.json")
	old := []byte(`{"proxyMode":"direct","proxyUrl":"","catalogConcurrency":4,"catalogIntervalMs":350,"downloadConcurrency":6,"downloadSourceConcurrency":2,"downloadBySource":true,"futureOption":{"keep":1}}`)
	if err := os.WriteFile(file, old, 0600); err != nil {
		t.Fatal(err)
	}
	engine.loadResourceSettings()
	settings := engine.resourceSettings()
	after, _ := os.ReadFile(file)
	if string(after) != string(old) || settings.CacheLimits["catalogMiB"] != 256 || settings.DownloadConcurrency != 6 || settings.DownloadSourceConcurrency != 2 || len(settings.CacheUsage) != 8 {
		t.Fatal("legacy settings rewritten or defaults/usage missing", settings)
	}
	settings.CacheLimits["catalogMiB"] = 512
	if _, err := engine.saveResourceSettings(settings); err != nil {
		t.Fatal(err)
	}
	body, _ := os.ReadFile(file)
	var stored map[string]json.RawMessage
	if json.Unmarshal(body, &stored) != nil || stored["futureOption"] == nil || stored["cacheUsage"] != nil {
		t.Fatal("unknown settings lost or transient usage persisted")
	}
	restarted := reopenCatalogEngine(t, engine.directory, nil)
	if restarted.resourceSettings().CacheLimits["catalogMiB"] != 512 || restarted.cacheBytes("catalogMiB") != 512<<20 {
		t.Fatal("cache policy did not survive restart")
	}
	var legacy nativeResourceSettings
	if json.Unmarshal(old, &legacy) != nil {
		t.Fatal("legacy JSON invalid")
	}
	if _, err := restarted.saveResourceSettings(legacy); err != nil || restarted.cacheBytes("catalogMiB") != 512<<20 {
		t.Fatal("a legacy sender reset the customized cache", err)
	}
}

// TestCacheSettingsRejectUnsafeReductionWithoutDeletingRecords 验证「Cache Settings Reject Unsafe Reduction Without Deleting Records」回归场景。
func TestCacheSettingsRejectUnsafeReductionWithoutDeletingRecords(t *testing.T) {
	engine := sourceFixtureEngine(t, nil)
	settings := engine.resourceSettings()
	if _, err := engine.saveResourceSettings(settings); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(engine.directory, "resource-settings.json")
	before, _ := os.ReadFile(file)
	records, _ := json.Marshal(map[string]nativeSourceRecord{sourceGuipian: {Stage: strings.Repeat("x", 5<<20)}})
	path := filepath.Join(engine.directory, "sources.json")
	if err := os.WriteFile(path, records, 0600); err != nil {
		t.Fatal(err)
	}
	settings.CacheLimits["sourceRecordsMiB"] = 4
	if _, err := engine.saveResourceSettings(settings); err == nil || !strings.Contains(err.Error(), "不会被删除") {
		t.Fatal("unsafe reduction accepted", err)
	}
	after, _ := os.ReadFile(file)
	preserved, _ := os.ReadFile(path)
	if string(before) != string(after) || string(preserved) != string(records) || engine.cacheBytes("sourceRecordsMiB") != 8<<20 {
		t.Fatal("failed reduction changed saved configuration or history")
	}
}

// TestCacheSettingsWriteFailureDoesNotApplyPolicy 验证「Cache Settings Write Failure Does Not Apply Policy」回归场景。
func TestCacheSettingsWriteFailureDoesNotApplyPolicy(t *testing.T) {
	engine := sourceFixtureEngine(t, nil)
	if err := os.Mkdir(filepath.Join(engine.directory, "resource-settings.json"), 0700); err != nil {
		t.Fatal(err)
	}
	settings := engine.resourceSettings()
	settings.CacheLimits["catalogMiB"] = 1024
	if _, err := engine.saveResourceSettings(settings); err == nil || engine.cacheBytes("catalogMiB") != 256<<20 {
		t.Fatal("failed save applied new policy", err)
	}
}

// TestCacheSettingsUsageAndRuntimeLimits 验证「Cache Settings Usage And Runtime Limits」回归场景。
func TestCacheSettingsUsageAndRuntimeLimits(t *testing.T) {
	engine := sourceFixtureEngine(t, nil)
	for name, size := range map[string]int{"catalogs.json": 100, "sources.json": 70, "rankings.json": 20} {
		if err := os.WriteFile(filepath.Join(engine.directory, name), make([]byte, size), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.MkdirAll(engine.covers.directory, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(engine.covers.directory, strings.Repeat("a", 64)+".img"), make([]byte, 30), 0600); err != nil {
		t.Fatal(err)
	}
	usage := engine.resourceSettings().CacheUsage
	if usage["catalogMiB"].Value != 100 || usage["sourceRecordsMiB"].Value != 70 || usage["rankingMiB"].Value != 20 || usage["coverMiB"].Value != 30 || usage["coverEntries"].Value != 1 {
		t.Fatal(usage)
	}
	settings := engine.resourceSettings()
	for _, spec := range nativeCacheSpecs {
		settings.CacheLimits[spec.key] = spec.minimum * 8
	}
	if _, err := engine.saveResourceSettings(settings); err != nil || engine.covers.limit != 2048<<20 || engine.covers.entryLimit != 16000 || engine.downloader.cacheBytes("preloadMiB") != 32<<20 || engine.downloader.cacheLimit("rankingPages") != 1024 {
		t.Fatal("runtime limits did not change", err)
	}
}

// TestCacheSettingsSnapshotsDoNotRaceOrMutatePolicy 验证「Cache Settings Snapshots Do Not Race Or Mutate Policy」回归场景。
func TestCacheSettingsSnapshotsDoNotRaceOrMutatePolicy(t *testing.T) {
	engine := sourceFixtureEngine(t, nil)
	settings := engine.resourceSettings()
	settings.CacheLimits["catalogMiB"] = 512
	if _, err := engine.saveResourceSettings(settings); err != nil {
		t.Fatal(err)
	}
	settings.CacheLimits["catalogMiB"] = 128
	var wait sync.WaitGroup
	for range 5 {
		wait.Add(1)
		go func() {
			defer wait.Done()
			for range 10 {
				bytes := engine.cacheBytes("catalogMiB")
				limit := engine.resourceSettings().CacheLimits["catalogMiB"]
				if bytes != 512<<20 && bytes != 256<<20 || limit != 512 && limit != 256 {
					t.Error("caller mutated the active cache policy")
				}
			}
		}()
	}
	wait.Add(1)
	go func() {
		defer wait.Done()
		for index := range 10 {
			value := engine.resourceSettings()
			value.CacheLimits["catalogMiB"] = 256 + (index%2)*256
			if _, err := engine.saveResourceSettings(value); err != nil {
				t.Error(err)
			}
		}
	}()
	wait.Wait()
}

// TestCacheSettingsInvalidCacheDoesNotResetNetworkConfiguration 验证「Cache Settings Invalid Cache Does Not Reset Network Configuration」回归场景。
func TestCacheSettingsInvalidCacheDoesNotResetNetworkConfiguration(t *testing.T) {
	for _, value := range []string{`{"catalogMiB":1025}`, `{"catalogMiB":"bad"}`} {
		engine := sourceFixtureEngine(t, nil)
		body := []byte(`{"proxyMode":"direct","catalogConcurrency":4,"catalogIntervalMs":350,"downloadConcurrency":6,"downloadSourceConcurrency":2,"downloadBySource":true,"cacheLimits":` + value + `}`)
		path := filepath.Join(engine.directory, "resource-settings.json")
		if err := os.WriteFile(path, body, 0600); err != nil {
			t.Fatal(err)
		}
		engine.loadResourceSettings()
		settings := engine.resourceSettings()
		if settings.ProxyMode != "direct" || settings.DownloadConcurrency != 6 || settings.CatalogConcurrency != 4 || settings.CacheLimits["catalogMiB"] != 256 || settings.Warning == "" {
			t.Fatal("invalid cache settings reset unrelated configuration", settings)
		}
		after, _ := os.ReadFile(path)
		if string(after) != string(body) {
			t.Fatal("loading invalid settings rewrote the original file")
		}
	}
}
