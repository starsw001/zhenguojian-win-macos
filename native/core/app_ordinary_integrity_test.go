package core

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// TestOrdinaryCatalogEpochSurvivesRestartAndChangesPerPackage 验证「Ordinary Catalog Epoch Survives Restart And Changes Per Package」回归场景。
func TestOrdinaryCatalogEpochSurvivesRestartAndChangesPerPackage(t *testing.T) {
	previous := buildAllSources
	buildAllSources = "true"
	defer func() { buildAllSources = previous }()
	engine := ordinaryEngine(t)
	engine.catalogs[sourceHongguo] = []nativeDrama{{ID: "hongguo:1", Source: sourceHongguo}}
	engine.catalogs[sourceMaoguo] = []nativeDrama{{ID: "maoguo:2", Source: sourceMaoguo}}
	engine.catalogs[sourceHuangguoAI] = []nativeDrama{{ID: "huangguoai:3", Source: sourceHuangguoAI}}
	if err := engine.writeCatalogDiskLocked(); err != nil {
		t.Fatal(err)
	}
	stats, err := engine.rebuildOrdinaryCatalogsWithEpoch("package-first")
	if err != nil || stats["catalogRows"] != 2 || len(engine.catalogs[sourceHuangguoAI]) != 1 {
		t.Fatal(stats, err)
	}
	engine.catalogs[sourceMaoguo] = []nativeDrama{{ID: "maoguo:4", Source: sourceMaoguo, Title: "新目录", Episodes: 8}}
	if err := engine.writeCatalogDiskLocked(); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(engine.directory, "catalogs.json")
	before, _ := os.ReadFile(file)
	restart, err := newNativeEngine(engine.directory)
	if err != nil {
		t.Fatal(err)
	}
	defer restart.downloads.close()
	stats, err = restart.rebuildOrdinaryCatalogsWithEpoch("package-first")
	after, _ := os.ReadFile(file)
	if err != nil || stats["catalogRows"] != 0 || string(before) != string(after) || len(restart.catalogs[sourceMaoguo]) != 1 {
		t.Fatal("same package erased progress", stats, err)
	}
	next, err := newNativeEngine(engine.directory)
	if err != nil {
		t.Fatal(err)
	}
	defer next.downloads.close()
	stats, err = next.rebuildOrdinaryCatalogsWithEpoch("package-second")
	if err != nil || stats["catalogRows"] != 1 || len(next.catalogs[sourceMaoguo]) != 0 || len(next.catalogs[sourceHuangguoAI]) != 1 {
		t.Fatal("new package did not rebuild scoped cache", stats, err)
	}
	if next.catalogEpochs["zhenguoshang"] != "package-second" {
		t.Fatal(next.catalogEpochs)
	}
}

// TestOrdinaryCatalogEpochLegacyAndEmptyCaches 验证「Ordinary Catalog Epoch Legacy And Empty Caches」回归场景。
func TestOrdinaryCatalogEpochLegacyAndEmptyCaches(t *testing.T) {
	previous := buildAllSources
	buildAllSources = "false"
	defer func() { buildAllSources = previous }()
	for _, body := range []string{"", `{"hongguo":[{"id":"hongguo:1","source":"hongguo"}],"huangguoai":[{"id":"huangguoai:2","source":"huangguoai"}]}`} {
		t.Run(body, func(t *testing.T) {
			engine := ordinaryEngine(t)
			file := filepath.Join(engine.directory, "catalogs.json")
			if body != "" {
				if err := os.WriteFile(file, []byte(body), 0600); err != nil {
					t.Fatal(err)
				}
				engine.loadCatalogCache()
			}
			if _, err := engine.rebuildOrdinaryCatalogsWithEpoch("legacy-package"); err != nil {
				t.Fatal(err)
			}
			restart, err := newNativeEngine(engine.directory)
			if err != nil {
				t.Fatal(err)
			}
			defer restart.downloads.close()
			if restart.catalogEpochs["hongguoshang"] != "legacy-package" || len(restart.catalogs[sourceHongguo]) != 0 {
				t.Fatal("epoch lost when loading legacy migration")
			}
			if body != "" && len(restart.catalogs[sourceHuangguoAI]) != 1 {
				t.Fatal("legacy unrelated cache lost")
			}
		})
	}
}

// TestOrdinaryCatalogEpochFailureDoesNotCommit 验证「Ordinary Catalog Epoch Failure Does Not Commit」回归场景。
func TestOrdinaryCatalogEpochFailureDoesNotCommit(t *testing.T) {
	engine := ordinaryEngine(t)
	file := filepath.Join(engine.directory, "catalogs.json")
	body := []byte(`{"catalogs":BROKEN}`)
	if err := os.WriteFile(file, body, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := engine.rebuildOrdinaryCatalogsWithEpoch("not-committed"); err == nil || engine.ordinaryCachesReset || len(engine.catalogEpochs) != 0 {
		t.Fatal("failed reset committed epoch", err)
	}
	after, _ := os.ReadFile(file)
	if string(body) != string(after) {
		t.Fatal("failed reset changed original")
	}
}

// TestOrdinaryIntegrityRejectsConflictingEpisodesAndCanonicalizesExactDuplicates 验证「Ordinary Integrity Rejects Conflicting Episodes And Canonicalizes Exact Duplicates」回归场景。
func TestOrdinaryIntegrityRejectsConflictingEpisodesAndCanonicalizesExactDuplicates(t *testing.T) {
	raw := Drama{EpisodeCount: json.Number("4")}
	chapter := Chapter{ID: "wuguo:123:1", Source: sourceWuguo, Title: "第01集", CurrentEpisode: json.RawMessage(`"01"`), VideoURL: "https://cdn.test/one.mp4"}
	other := Chapter{ID: "wuguo:123:4", Source: sourceWuguo, Title: "第4集", CurrentEpisode: rawEpisode(4), VideoURL: "https://cdn.test/four.mp4"}
	chapters, info, err := prepareOrdinaryChapters(raw, []Chapter{other, chapter, chapter})
	if err != nil || len(chapters) != 2 || info.Actual != 2 || info.DuplicateRows != 1 || !reflect.DeepEqual(info.MissingNumbers, []int{2, 3}) || chapters[0].EpisodeString(1) != "1" {
		t.Fatal(chapters, info, err)
	}
	for _, change := range []func(*Chapter){
		func(c *Chapter) { c.ID = "wuguo:123:1" },
		func(c *Chapter) { c.VideoURL = chapter.VideoURL },
		func(c *Chapter) { c.CurrentEpisode = rawEpisode(1) },
		func(c *Chapter) { c.Title = "第2集" },
		func(c *Chapter) { c.CurrentEpisode = json.RawMessage(`1.5`) },
	} {
		bad := other
		change(&bad)
		if _, _, err := prepareOrdinaryChapters(raw, []Chapter{chapter, bad}); err == nil {
			t.Fatal("conflicting episode accepted", bad)
		}
	}
}

// TestOrdinaryIntegrityReportsGapsAndBothCountDirections 验证「Ordinary Integrity Reports Gaps And Both Count Directions」回归场景。
func TestOrdinaryIntegrityReportsGapsAndBothCountDirections(t *testing.T) {
	chapters := []Chapter{{ID: "one", Title: "第1集", CurrentEpisode: rawEpisode(1)}, {ID: "three", Title: "第3集", CurrentEpisode: rawEpisode(3)}}
	if warning := ordinaryDetailCountWarning(Drama{EpisodeCount: json.Number("1")}, chapters); !strings.Contains(warning, "标注1集") || !strings.Contains(warning, "第2集") {
		t.Fatal(warning)
	}
	if warning := ordinaryDetailCountWarning(Drama{}, chapters); !strings.Contains(warning, "未声明") || !strings.Contains(warning, "第2集") {
		t.Fatal(warning)
	}
	chapters = []Chapter{{ID: "unnumbered", Title: "正片"}}
	_, info, err := prepareOrdinaryChapters(Drama{}, chapters)
	if err != nil || info.InferredNumbers != 1 || ordinaryDetailCountWarning(Drama{}, chapters) == "" {
		t.Fatal(info, err)
	}
}

// TestOrdinaryDetailFreshHeadersAndConnectionAuthorization 验证「Ordinary Detail Fresh Headers And Connection Authorization」回归场景。
func TestOrdinaryDetailFreshHeadersAndConnectionAuthorization(t *testing.T) {
	calls := 0
	d := sourceFixtureDownloader(t, func(request *http.Request) (*http.Response, error) {
		calls++
		if request.Header.Get("Cache-Control") != "no-cache" || request.Header.Get("Pragma") != "no-cache" {
			t.Fatal("fresh detail did not request a fresh HTTP response")
		}
		return sourceFixtureResponse(request, 200, "current page"), nil
	})
	if _, err := d.fetchProviderText(withFreshOrdinaryDetail(context.Background(), sourceGuipian), "https://example.test/detail/1", ""); err != nil || calls != 1 {
		t.Fatal(calls, err)
	}
	engine := ordinaryEngine(t)
	if _, err := engine.checkOrdinaryEpisodeConnection(context.Background(), nativeInput{Drama: nativeDrama{ID: "huangguoai:1", Source: sourceHuangguoAI}}); err == nil {
		t.Fatal("excluded source connection was accepted")
	}
	if _, err := engine.checkOrdinaryEpisodeConnection(context.Background(), nativeInput{Drama: nativeDrama{ID: "hongguo:1", Source: sourceHongguo}, Chapter: Chapter{VIP: true}}); err == nil {
		t.Fatal("paid episode connection was accepted")
	}
}

// TestOrdinaryMaccmsMainHeadingPreservesFullWorkName 验证「Ordinary Maccms Main Heading Preserves Full Work Name」回归场景。
func TestOrdinaryMaccmsMainHeadingPreservesFullWorkName(t *testing.T) {
	d := sourceFixtureDownloader(t, func(request *http.Request) (*http.Response, error) {
		return sourceFixtureResponse(request, 200, `<title>短剧《完整名称-动漫合集》全集免费高清在线观看_短剧主演_影视网</title><h1 class="title">完整名称-动漫合集</h1><ul class="playlist"><a href="/zywplay/77-1-1.html">全集</a></ul>`), nil
	})
	drama, chapters, err := d.fetchMaccmsDetail(context.Background(), sourceHuaguo, "77")
	if err != nil || drama.Title != "完整名称-动漫合集" || len(chapters) != 1 {
		t.Fatal(drama, chapters, err)
	}
}

// TestOrdinaryWuguoCategoryUsesVerifiedTypePagination 验证「Ordinary Wuguo Category Uses Verified Type Pagination」回归场景。
func TestOrdinaryWuguoCategoryUsesVerifiedTypePagination(t *testing.T) {
	var paths []string
	engine := sourceFixtureEngine(t, func(request *http.Request) (*http.Response, error) {
		paths = append(paths, request.URL.Path)
		if request.URL.Path == "/" {
			if request.Header.Get("Cache-Control") != "no-cache" {
				t.Fatal("forced category did not request latest source")
			}
			return sourceFixtureResponse(request, 200, `<a href="/index.php/vod/type/id/1.html">短剧</a><a href="https://other.test/index.php/vod/type/id/9.html">外链</a>`), nil
		}
		return sourceFixtureResponse(request, 200, `<div class="BrowseList_listItem"><a href="/index.php/vod/detail/id/77.html" title="普通作品"><img src="/77.jpg"></a></div><div class="pagination"><a href="/index.php/vod/type/id/1/page/2391.html">2391</a></div>`), nil
	})
	categories, err := engine.nativeCategories(context.Background(), sourceWuguo, true)
	if err != nil || len(categories) != 2 || categories[1].ID != "1" {
		t.Fatal(categories, err)
	}
	items, more, err := engine.downloader.fetchMaccmsCatalogPage(context.Background(), sourceWuguo, 1, "")
	if err != nil || !more || len(items) != 1 || paths[len(paths)-1] != "/index.php/vod/type/id/1.html" {
		t.Fatal(items, more, paths, err)
	}
	_, more, err = engine.downloader.fetchMaccmsCatalogPage(context.Background(), sourceWuguo, 2, "1")
	if err != nil || !more || paths[len(paths)-1] != "/index.php/vod/type/id/1/page/2.html" {
		t.Fatal(paths, more, err)
	}
}

// TestOrdinaryAuditDetailDoesNotRewriteCatalogAndNormalDetailDoes 验证「Ordinary Audit Detail Does Not Rewrite Catalog And Normal Detail Does」回归场景。
func TestOrdinaryAuditDetailDoesNotRewriteCatalogAndNormalDetailDoes(t *testing.T) {
	engine := sourceFixtureEngine(t, func(request *http.Request) (*http.Response, error) {
		return sourceFixtureResponse(request, 200, `<h1 class="title">普通作品</h1><ul class="playlist"><a href="/zywplay/77-1-1.html">第1集</a></ul>`), nil
	})
	drama := nativeDrama{ID: "huaguo:77", Source: sourceHuaguo, SourceID: "77", Title: "普通作品", Episodes: 100}
	engine.catalogs[sourceHuaguo] = []nativeDrama{drama}
	if err := engine.writeCatalogDiskLocked(); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(engine.directory, "catalogs.json")
	before, _ := os.ReadFile(file)
	result, err := engine.nativeDetail(context.WithValue(context.Background(), ordinaryDetailReadOnlyKey{}, true), drama)
	if err != nil {
		t.Fatal(err)
	}
	after, _ := os.ReadFile(file)
	fresh := result.(map[string]any)["drama"].(nativeDrama)
	if string(before) != string(after) || fresh.Episodes != 1 || !fresh.EpisodeCountVerified || engine.catalogs[sourceHuaguo][0].Episodes != 100 {
		t.Fatal("audit rewrote catalog or did not validate actual count")
	}
	if _, err = engine.nativeDetail(context.Background(), drama); err != nil || engine.catalogs[sourceHuaguo][0].Episodes != 1 || !engine.catalogs[sourceHuaguo][0].EpisodeCountVerified {
		t.Fatal("normal detail did not publish actual count", err)
	}
}

// TestOrdinaryMaccmsNavigationCannotBecomeDramaAndRealCardCountIsKept 验证「Ordinary Maccms Navigation Cannot Become Drama And Real Card Count Is Kept」回归场景。
func TestOrdinaryMaccmsNavigationCannotBecomeDramaAndRealCardCountIsKept(t *testing.T) {
	for _, address := range []string{"/index.php/vod/type/id/1.html", "/index.php/vod/search/page/2.html", "/p/66/c/67", "/play/1-1-1.html"} {
		if maccmsSourceIDFromURL(address) != "" {
			t.Fatal("navigation became a drama", address)
		}
	}
	d := sourceFixtureDownloader(t, func(request *http.Request) (*http.Response, error) {
		return sourceFixtureResponse(request, 200, `<a href="/index.php/vod/type/id/1.html">全部</a><div class="BrowseList_itemBox"><a href="/index.php/vod/detail/id/77.html"><img alt="普通作品" src="/cover.jpg"></a><a class="BrowseList_totalChapterNum" href="/index.php/vod/detail/id/77.html">更新至第20集</a></div>`), nil
	})
	items, _, err := d.fetchMaccmsCatalogPage(context.Background(), sourceWuguo, 1, "1")
	if err != nil || len(items) != 1 || items[0].ID != "wuguo:77" || items[0].EpisodeCount != json.Number("20") {
		t.Fatal(items, err)
	}
}
