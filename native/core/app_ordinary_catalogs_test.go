package core

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

// ordinaryEngine 返回普通站源引擎。
func ordinaryEngine(t *testing.T) *nativeEngine {
	t.Helper()
	return sourceFixtureEngine(t, func(request *http.Request) (*http.Response, error) {
		t.Errorf("cache operation requested network: %s", request.URL)
		return nil, errors.New("network forbidden")
	})
}

// readOrdinaryDisk 读取普通站源Disk。
func readOrdinaryDisk(t *testing.T, file string) map[string]any {
	t.Helper()
	body, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	var result map[string]any
	if json.Unmarshal(body, &result) != nil {
		t.Fatal("invalid rewritten cache")
	}
	return result
}

// TestOrdinaryCacheRebuildPreservesOtherSourcesAndAllUserFiles 验证「Ordinary Cache Rebuild Preserves Other Sources And All User Files」回归场景。
func TestOrdinaryCacheRebuildPreservesOtherSourcesAndAllUserFiles(t *testing.T) {
	beforeBuild := buildAllSources
	buildAllSources = "true"
	defer func() { buildAllSources = beforeBuild }()
	engine := ordinaryEngine(t)
	catalogs, states, categories := map[string]any{}, map[string]any{}, map[string]any{}
	ordinary := []string{sourceHongguo, sourceHanxiaoquan, sourceGuipian, sourceSorani,
		sourceYaguo, sourceMaoguo, sourceFanguo, sourceGuanguo, sourceHeguo, sourceXingguo,
		sourceHuaguo, sourceNiuguo, sourceWangguo, sourceFaguo, sourcePiguo, sourceWuguo}
	excluded := []string{sourceHuangdou, sourceHuangju, sourceYeguo, sourceDSD, sourceHuangguoVideo, sourceHuangguoAI, sourceCloudFront}
	engine.categoryOptions = map[string][]nativeCategory{}
	for _, source := range append(ordinary, excluded...) {
		key := source + "|fixture-category"
		catalogs[key] = []map[string]any{{"id": source + ":one", "title": "目录占位"}}
		states[key] = map[string]any{"page": 3, "unknownCursor": "preserved"}
		categories[source] = []map[string]any{{"id": "7", "name": "分类"}}
		engine.catalogs[key] = []nativeDrama{{ID: source + ":one", Source: source}}
		engine.catalogStates[key] = nativeCatalogState{}
		engine.categoryOptions[source] = []nativeCategory{{ID: "7", Name: "分类"}}
	}
	catalogs["future-source"] = []string{"unknown"}
	disk := map[string]any{"version": 3, "catalogs": catalogs, "states": states, "categories": categories,
		"unknownTop": map[string]any{"preserve": true},
		"hongguoApp": map[string]any{"deviceId": "1234567890123456789", "installId": "2234567890123456789",
			"registered": true, "feeds": map[string]any{"default": map[string]any{"offset": 54}}, "extension": "keep"},
		"recommendations": map[string]any{"short_play": map[string]any{"page": 3}}}
	file := filepath.Join(engine.directory, "catalogs.json")
	body, _ := json.Marshal(disk)
	if err := os.WriteFile(file, body, 0600); err != nil {
		t.Fatal(err)
	}
	protected := []string{"settings.json", "profiles.json", "sources.json", "resource-settings.json", "downloads.json", "media-library.json", "video.mp4"}
	for _, name := range protected {
		if err := os.WriteFile(filepath.Join(engine.directory, name), []byte("keep:"+name), 0600); err != nil {
			t.Fatal(err)
		}
	}
	client := engine.downloader.hongguoClient()
	client.state.DeviceID = "1234567890123456789"
	client.state.InstallID = "2234567890123456789"
	client.state.Registered = true
	client.state.Feeds["default"] = hongguoCatalogCursor{Offset: 54}
	client.details["1"] = hongguoDetailEntry{Drama: Drama{Title: "旧详情"}}
	client.searches["old"] = hongguoSearchEntry{}
	stats, err := engine.rebuildOrdinaryCatalogs()
	if err != nil {
		t.Fatal(err)
	}
	if stats["catalogKeys"] != 16 || stats["catalogRows"] != 16 || stats["states"] != 16 || stats["categories"] != 16 {
		t.Fatal(stats)
	}
	after := readOrdinaryDisk(t, file)
	for _, field := range []string{"catalogs", "states", "categories"} {
		values := after[field].(map[string]any)
		for _, source := range ordinary {
			key := source + "|fixture-category"
			if field == "categories" {
				key = source
			}
			if _, ok := values[key]; ok {
				t.Fatalf("ordinary cache retained: %s %s", field, key)
			}
		}
		for _, source := range excluded {
			key := source + "|fixture-category"
			if field == "categories" {
				key = source
			}
			if _, ok := values[key]; !ok {
				t.Fatalf("excluded cache removed: %s %s", field, key)
			}
		}
	}
	if !reflect.DeepEqual(after["unknownTop"], map[string]any{"preserve": true}) {
		t.Fatal("unknown top field lost")
	}
	app := after["hongguoApp"].(map[string]any)
	if app["deviceId"] != "1234567890123456789" || app["installId"] != "2234567890123456789" || app["registered"] != true || app["extension"] != "keep" || len(app["feeds"].(map[string]any)) != 0 {
		t.Fatal(app)
	}
	if len(after["recommendations"].(map[string]any)) != 0 || len(client.details) != 0 || len(client.searches) != 0 || len(client.state.Feeds) != 0 || !client.state.Registered {
		t.Fatal("ordinary runtime cache or device identity incorrect")
	}
	if len(engine.catalogs) != 7 || len(engine.catalogStates) != 7 || len(engine.categoryOptions) != 7 {
		t.Fatal("RAM scope incorrect")
	}
	for _, name := range protected {
		got, _ := os.ReadFile(filepath.Join(engine.directory, name))
		if string(got) != "keep:"+name {
			t.Fatal("protected file changed", name)
		}
	}
	engine.catalogs[sourceHongguo] = []nativeDrama{{ID: "hongguo:new"}}
	first, _ := os.ReadFile(file)
	again, err := engine.rebuildOrdinaryCatalogs()
	if err != nil || again["catalogKeys"] != 0 || len(engine.catalogs[sourceHongguo]) != 1 {
		t.Fatal("same engine reset twice", again, err)
	}
	second, _ := os.ReadFile(file)
	if !bytes.Equal(first, second) {
		t.Fatal("second initialize changed disk")
	}
	t.Log("CACHE_SCOPE: 16 ordinary caches reset; 7 excluded sources, user files and device IDs preserved; once per foreground engine")
}

// TestOrdinaryCacheRedEditionOnlyClearsAvailableSource 验证「Ordinary Cache Red Edition Only Clears Available Source」回归场景。
func TestOrdinaryCacheRedEditionOnlyClearsAvailableSource(t *testing.T) {
	before := buildAllSources
	buildAllSources = "false"
	defer func() { buildAllSources = before }()
	engine := ordinaryEngine(t)
	file := filepath.Join(engine.directory, "catalogs.json")
	body := []byte(`{"version":3,"catalogs":{"hongguo":[{"id":"hongguo:1"}],"guipian":[{"id":"guipian:2"}]},"states":{},"categories":{}}`)
	if err := os.WriteFile(file, body, 0600); err != nil {
		t.Fatal(err)
	}
	stats, err := engine.rebuildOrdinaryCatalogs()
	if err != nil || stats["catalogKeys"] != 1 {
		t.Fatal(stats, err)
	}
	catalogs := readOrdinaryDisk(t, file)["catalogs"].(map[string]any)
	if len(catalogs) != 1 || catalogs[sourceGuipian] == nil {
		t.Fatal("red edition removed unavailable source", catalogs)
	}
}

// TestOrdinaryCacheLegacyAndMissingFiles 验证「Ordinary Cache Legacy And Missing Files」回归场景。
func TestOrdinaryCacheLegacyAndMissingFiles(t *testing.T) {
	before := buildAllSources
	buildAllSources = "true"
	defer func() { buildAllSources = before }()
	engine := ordinaryEngine(t)
	file := filepath.Join(engine.directory, "catalogs.json")
	if err := os.WriteFile(file, []byte(`{"guipian":[{"id":"guipian:1"}],"huangguoai":[{"id":"huangguoai:2"}]}`), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := engine.rebuildOrdinaryCatalogs(); err != nil {
		t.Fatal(err)
	}
	result := readOrdinaryDisk(t, file)
	if len(result) != 1 || result[sourceHuangguoAI] == nil {
		t.Fatal(result)
	}
	other := ordinaryEngine(t)
	if _, err := other.rebuildOrdinaryCatalogs(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(other.directory, "catalogs.json")); !os.IsNotExist(err) {
		t.Fatal("missing cache should not create a file")
	}
}

// TestOrdinaryCacheInvalidAndOversizePreserveOriginal 验证「Ordinary Cache Invalid And Oversize Preserve Original」回归场景。
func TestOrdinaryCacheInvalidAndOversizePreserveOriginal(t *testing.T) {
	for _, body := range []string{`not-json`, `null`, `{"catalogs":[]}`, `{"catalogs":{},"hongguoApp":[]}`} {
		t.Run(body, func(t *testing.T) {
			engine := ordinaryEngine(t)
			file := filepath.Join(engine.directory, "catalogs.json")
			if err := os.WriteFile(file, []byte(body), 0600); err != nil {
				t.Fatal(err)
			}
			engine.catalogs[sourceHongguo] = []nativeDrama{{ID: "hongguo:keep"}}
			if _, err := engine.rebuildOrdinaryCatalogs(); err == nil {
				t.Fatal("invalid cache accepted")
			}
			after, _ := os.ReadFile(file)
			if string(after) != body || engine.ordinaryCachesReset || len(engine.catalogs[sourceHongguo]) != 1 {
				t.Fatal("failure changed original")
			}
		})
	}
	t.Run("oversize", func(t *testing.T) {
		engine := ordinaryEngine(t)
		file := filepath.Join(engine.directory, "catalogs.json")
		handle, err := os.Create(file)
		if err != nil {
			t.Fatal(err)
		}
		size := int64(engine.cacheBytes("catalogMiB")) + 1
		if err = handle.Truncate(size); err != nil {
			t.Fatal(err)
		}
		handle.Close()
		if _, err = engine.rebuildOrdinaryCatalogs(); err == nil {
			t.Fatal("oversize accepted")
		}
		info, _ := os.Stat(file)
		if info.Size() != size || engine.ordinaryCachesReset {
			t.Fatal("oversize file changed")
		}
	})
}

// TestOrdinaryCacheLinkIsNotFollowed 验证「Ordinary Cache Link Is Not Followed」回归场景。
func TestOrdinaryCacheLinkIsNotFollowed(t *testing.T) {
	engine := ordinaryEngine(t)
	file := filepath.Join(engine.directory, "catalogs.json")
	outside := filepath.Join(t.TempDir(), "keep.json")
	if err := os.WriteFile(outside, []byte(`{"hongguo":[]}`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, file); err != nil {
		t.Skip("symlink privilege unavailable", err)
	}
	if _, err := engine.rebuildOrdinaryCatalogs(); err == nil {
		t.Fatal("link accepted")
	}
	body, _ := os.ReadFile(outside)
	if string(body) != `{"hongguo":[]}` {
		t.Fatal("linked file modified")
	}
}

// TestOrdinaryDetailIdentityAndEpisodeCount 验证「Ordinary Detail Identity And Episode Count」回归场景。
func TestOrdinaryDetailIdentityAndEpisodeCount(t *testing.T) {
	selected := nativeDrama{ID: "guipian:123", Source: sourceGuipian, SourceID: "123", Title: "普通作品 第一季", Episodes: 27}
	raw := Drama{ID: selected.ID, Source: selected.Source, SourceID: "123", Title: selected.Title}
	chapters := []Chapter{{ID: "guipian:123:one", Source: sourceGuipian, CurrentEpisode: rawEpisode(1)},
		{ID: "guipian:123:two", Source: sourceGuipian, CurrentEpisode: rawEpisode(2)},
		{ID: "guipian:123:two", Source: sourceGuipian, CurrentEpisode: rawEpisode(2)}}
	if err := validateOrdinaryDetail(selected, raw, "", chapters); err != nil {
		t.Fatal(err)
	}
	if ordinaryEpisodeCount(chapters) != 2 {
		t.Fatal("duplicate episodes inflated count")
	}
	for _, change := range []func(*Drama){
		func(d *Drama) { d.ID = "guipian:999" }, func(d *Drama) { d.Source = sourceHongguo },
		func(d *Drama) { d.SourceID = "999" }, func(d *Drama) { d.Title = "别的作品" },
	} {
		bad := raw
		change(&bad)
		if validateOrdinaryDetail(selected, bad, "", chapters) == nil {
			t.Fatal("wrong work accepted", bad)
		}
	}
	for _, chapter := range []Chapter{{ID: "guipian:999:one", Source: sourceGuipian}, {ID: "hongguo:123:one", Source: sourceHongguo}} {
		if validateOrdinaryDetail(selected, raw, "", []Chapter{chapter}) == nil {
			t.Fatal("foreign chapter accepted", chapter)
		}
	}
	if withFreshOrdinaryDetail(context.Background(), sourceHongguo).Value(ordinaryDetailFreshKey{}) != true {
		t.Fatal("ordinary detail not refreshed")
	}
	if withFreshOrdinaryDetail(context.Background(), sourceHuangguoAI).Value(ordinaryDetailFreshKey{}) != nil {
		t.Fatal("excluded detail behavior changed")
	}
}

// TestOrdinaryGuipianPlaylistFiltersBeforeChoosingLine 验证「Ordinary Guipian Playlist Filters Before Choosing Line」回归场景。
func TestOrdinaryGuipianPlaylistFiltersBeforeChoosingLine(t *testing.T) {
	body := `<li class="swiper-slide ewave-tab" data-target="#ewave-playlist-1">线路一</li><li class="swiper-slide ewave-tab" data-target="#ewave-playlist-2">线路二</li><div class="playlist-slide" id="ewave-playlist-1">`
	for n := 1; n <= 27; n++ {
		body += fmt.Sprintf(`<li><a title='第%d集' href='/play/999-1-%d.html'>`, n, n)
	}
	body += `<div class="playlist-slide" id="ewave-playlist-2">`
	for n := 1; n <= 10; n++ {
		body += fmt.Sprintf(`<li><a title='第%d集' href='/play/123-2-%d.html'>`, n, n)
	}
	body += `</section>`
	_, chapters, err := guipianPickPlaylist(body, "123")
	if err != nil || len(chapters) != 10 {
		t.Fatal("selected unrelated longer playlist", len(chapters), err)
	}
	for _, chapter := range chapters {
		if !strings.Contains(chapter.PageURL, "/play/123-") {
			t.Fatal(chapter)
		}
	}
	if _, _, err = guipianPickPlaylist(body, "888"); err == nil {
		t.Fatal("foreign-only list accepted")
	}
	t.Log("PLAYLIST_ISOLATION: 27 foreign chapters rejected, selected work has 10 independent chapters")
}

// TestOrdinaryHongguoDetailRefreshDoesNotUseCompletedCache 验证「Ordinary Hongguo Detail Refresh Does Not Use Completed Cache」回归场景。
func TestOrdinaryHongguoDetailRefreshDoesNotUseCompletedCache(t *testing.T) {
	calls := 0
	d := sourceFixtureDownloader(t, func(request *http.Request) (*http.Response, error) {
		calls++
		return nil, errors.New("fixture fresh request")
	})
	client := d.hongguoClient()
	client.state.Registered = true
	client.details["1234567890"] = hongguoDetailEntry{Drama: Drama{Title: "cached"}, ExpiresAt: time.Now().Add(time.Hour)}
	if entry, err := d.hongguoAppDetail(context.Background(), "1234567890"); err != nil || entry.Drama.Title != "cached" || calls != 0 {
		t.Fatal("baseline cache fixture invalid", err)
	}
	if _, err := d.hongguoAppDetail(withFreshOrdinaryDetail(context.Background(), sourceHongguo), "1234567890"); err == nil || calls == 0 {
		t.Fatal("fresh request used old detail cache", err, calls)
	}
}
