package core

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// fullAICatalogFixture 返回fullAI目录Fixture。
func fullAICatalogFixture(t *testing.T, mode string) (*nativeEngine, *atomic.Int32) {
	t.Helper()
	oldBuild, oldPublications, oldPermanent := buildAllSources, huangguoPublicationURLs, huangguoAIPermanentURLs
	buildAllSources = "true"
	huangguoPublicationURLs = []string{"https://publication.example.org"}
	huangguoAIPermanentURLs = []string{"https://permanent-one.example.org", "https://permanent-two.example.org"}
	t.Cleanup(func() {
		buildAllSources, huangguoPublicationURLs, huangguoAIPermanentURLs = oldBuild, oldPublications, oldPermanent
	})
	reads := &atomic.Int32{}
	engine := sourceFixtureEngine(t, func(request *http.Request) (*http.Response, error) {
		if strings.HasPrefix(request.URL.Path, "/api/videos/category/") {
			reads.Add(1)
			if mode == "blocked" {
				return sourceFixtureResponse(request, 403, "ordinary access denied"), nil
			}
			if mode == "invalid" {
				return sourceFixtureResponse(request, 200, `{"code":503,"message":"ordinary fixture failure"}`), nil
			}
			page, _ := strconv.Atoi(request.URL.Query().Get("page"))
			if mode == "repeated" {
				page = 1
			}
			category := request.URL.Path[strings.LastIndex(request.URL.Path, "/")+1:]
			rows := []map[string]any{}
			for index := 0; index < 24; index++ {
				title := "普通目录剧集"
				if category == "ai-duanju" && page == 10 && index == 23 {
					title = "普通末页新剧"
				}
				rows = append(rows, map[string]any{"id": fmt.Sprintf("%s-%d-%d", category, page, index), "title": title, "description": "普通目录资料", "tags": []string{"奇幻"}, "episodes": 3})
			}
			body, _ := json.Marshal(map[string]any{"data": rows, "pagination": map[string]any{"hasMore": page < 10, "totalPages": 10}})
			return sourceFixtureResponse(request, 200, string(body)), nil
		}
		if request.URL.Host == "fresh.example.org" || mode == "permanent-old" && request.URL.Host == "permanent-one.example.org" {
			return sourceFixtureResponse(request, 200, `<section class="hg-home-popular-tags"><a class="hg-tagrow__item" href="/tag/qihuan/">奇幻</a></section><div class="hg-card-grid"><div class="hg-drama-card" data-track-id="ordinary"><h3 class="hg-drama-card__title">普通目录</h3></div></div>`), nil
		}
		return sourceFixtureResponse(request, 200, `<a href="https://fresh.example.org/">最新地址</a>`), nil
	})
	engine.downloader.cfg.HuangguoAIURL = "https://configured.example.org"
	engine.downloader.providerHosts[sourceHuangguoAI] = "https://media-verified.example.org"
	return engine, reads
}

// awaitFullAICatalogFixture 返回awaitFullAI目录Fixture。
func awaitFullAICatalogFixture(t *testing.T, engine *nativeEngine) nativeSourceStatus {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		status := engine.sourceStatus(sourceHuangguoAI)
		if !status.Running && status.Operation == "refreshAll" && !status.FinishedAt.IsZero() {
			return status
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("ordinary full catalog fixture did not finish")
	return nativeSourceStatus{}
}

// TestStartupAICatalogTraversesAllPagesAndPreservesExistingData 验证「Startup AI Catalog Traverses All Pages And Preserves Existing Data」回归场景。
func TestStartupAICatalogTraversesAllPagesAndPreservesExistingData(t *testing.T) {
	engine, reads := fullAICatalogFixture(t, "normal")
	old := nativeDrama{ID: "huangguoai:keep-old", Source: sourceHuangguoAI, Title: "保留原记录"}
	engine.catalogs[sourceHuangguoAI] = []nativeDrama{old}
	engine.catalogs[nativeCatalogKey(sourceHuangguoAI, "ai-duanju")] = []nativeDrama{{ID: "huangguoai:ai-duanju-10-23", Source: sourceHuangguoAI, Title: "分类旧名称"}}
	engine.sourceRecords[sourceHuangguoAI] = nativeSourceRecord{MetadataCursor: "keep-cursor"}
	engine.scheduleHuangguoStartupCatalog()
	status := awaitFullAICatalogFixture(t, engine)
	if status.Error != "" || status.Count != 960 || status.Completed != 40 || status.Total != 40 || reads.Load() != 40 {
		t.Fatal("full catalog was truncated or not saved", status.Count, status.Completed, status.Total, status.Error, reads.Load())
	}
	result := engine.searchHuangguoCatalog("普通末页新剧", 1)
	if len(result.Items) != 1 || result.Warning != "" || !result.LocalSearch {
		t.Fatal("last-page title missing from index", result)
	}
	first := engine.searchHuangguoCatalog("普通目录剧集", 1)
	second := engine.searchHuangguoCatalog("普通目录剧集", 2)
	if len(first.Items) != 60 || !first.HasMore || len(second.Items) != 60 || first.Items[0].ID == second.Items[0].ID {
		t.Fatal("indexed search pagination is not independent")
	}
	if engine.downloader.cfg.HuangguoAIURL != "https://configured.example.org" || engine.downloader.providerHosts[sourceHuangguoAI] != "https://media-verified.example.org" {
		t.Fatal("catalog refresh replaced user config or media routing")
	}
	if record := engine.sourceRecords[sourceHuangguoAI]; record.MetadataCursor != "keep-cursor" || record.CatalogFullAt.IsZero() || record.CatalogOrigin != "https://fresh.example.org" {
		t.Fatal("source history or full-completion marker", record)
	}
	entries, _, _ := engine.downloader.huangguoMirrorStore().snapshot()
	for _, entry := range entries {
		if entry.Origin == "https://fresh.example.org" && (entry.State == "ready" || entry.Playback || entry.Poster || entry.CatalogMessage == "") {
			t.Fatal("metadata check incorrectly promoted a media mirror")
		}
	}
	before := reads.Load()
	engine.scheduleHuangguoStartupCatalog()
	time.Sleep(20 * time.Millisecond)
	if reads.Load() != before {
		t.Fatal("same process duplicated full refresh")
	}
	restored, err := newNativeEngine(engine.directory)
	if err != nil {
		t.Fatal(err)
	}
	defer restored.downloads.close()
	if len(restored.searchHuangguoCatalog("普通末页新剧", 1).Items) != 1 || restored.downloader.huangguoCatalogOrigin != "https://fresh.example.org" {
		t.Fatal("full index or metadata origin did not survive restart")
	}
	restored.downloader.client.Transport = engine.downloader.client.Transport
	restored.downloader.limiter = newRequestLimiter(3, 0)
	restored.scheduleHuangguoStartupCatalog()
	awaitFullAICatalogFixture(t, restored)
	if reads.Load() <= before {
		t.Fatal("new process reused only a fresh head without full refresh")
	}
}

// TestStartupAICatalogFailureDoesNotClaimCompleteOrBypassAccessControl 验证「Startup AI Catalog Failure Does Not Claim Complete Or Bypass Access Control」回归场景。
func TestStartupAICatalogFailureDoesNotClaimCompleteOrBypassAccessControl(t *testing.T) {
	for _, mode := range []string{"blocked", "invalid", "repeated"} {
		t.Run(mode, func(t *testing.T) {
			engine, reads := fullAICatalogFixture(t, mode)
			engine.catalogs[sourceHuangguoAI] = []nativeDrama{{ID: "huangguoai:keep", Source: sourceHuangguoAI, Title: "保留原记录"}}
			engine.scheduleHuangguoStartupCatalog()
			status := awaitFullAICatalogFixture(t, engine)
			if status.Error == "" || !engine.sourceRecords[sourceHuangguoAI].CatalogFullAt.IsZero() || len(engine.searchCachedCatalog(sourceHuangguoAI, "保留原记录")) != 1 {
				t.Fatal("failure erased data or appeared complete", status.Error)
			}
			if mode == "blocked" && reads.Load() != 1 {
				t.Fatal("access control caused route rotation", reads.Load())
			}
			if mode == "repeated" && status.Count != 1 {
				t.Fatal("repeated pages replaced the old committed catalog", reads.Load(), status.Count)
			}
			if engine.searchHuangguoCatalog("不存在的普通条目", 1).Warning == "" {
				t.Fatal("incomplete index advertised no results as full coverage")
			}
		})
	}
}

// TestHuangguoCatalogPaginationMetadataAndNewCategories 验证「Huangguo Catalog Pagination Metadata And New Categories」回归场景。
func TestHuangguoCatalogPaginationMetadataAndNewCategories(t *testing.T) {
	for _, row := range []struct {
		body  string
		count int
		more  bool
	}{
		{`{"data":[],"pagination":{"hasMore":false}}`, 24, false},
		{`{"data":[],"meta":{"totalPages":3}}`, 1, true},
		{`{"data":[],"meta":{"totalPages":"3"}}`, 1, true},
		{`{"data":[],"meta":{"hasMore":"false"}}`, 24, false},
		{`{"data":[],"total":24,"pageSize":24}`, 24, false},
		{`{"data":[]}`, 24, true},
		{`{"data":[]}`, 0, false},
	} {
		if huangguoAIPageHasMore(row.body, 1, row.count) != row.more {
			t.Fatal("pagination metadata ignored", row.body)
		}
	}
	if !validNativeCategory(sourceHuangguoAI, "ai-new-category") || validNativeCategory(sourceHuangguoAI, "ai-new/escape") {
		t.Fatal("dynamic category validation")
	}
}

// TestHuangguoCatalogRejectsSuccessEnvelopeWithoutRows 验证「Huangguo Catalog Rejects Success Envelope Without Rows」回归场景。
func TestHuangguoCatalogRejectsSuccessEnvelopeWithoutRows(t *testing.T) {
	for _, body := range []string{`{"data":null}`, `{"data":{}}`, `{"code":0,"message":"ordinary"}`} {
		d := sourceFixtureDownloader(t, func(request *http.Request) (*http.Response, error) {
			return sourceFixtureResponse(request, 200, body), nil
		})
		ctx := context.WithValue(context.Background(), huangguoCatalogOriginKey{}, "https://ordinary.example.org")
		if _, _, err := d.fetchHuangguoAISectionPage(ctx, 1, nativeAICategories[0]); err == nil {
			t.Fatal("success envelope without a collection appeared complete", body)
		}
	}
}

// TestHuangguoPermanentAddressKeepsExistingPublicationHistory 验证「Huangguo Permanent Address Keeps Existing Publication History」回归场景。
func TestHuangguoPermanentAddressKeepsExistingPublicationHistory(t *testing.T) {
	d := sourceFixtureDownloader(t, nil)
	r := d.huangguoMirrorStore()
	old := huangguoPublicationStatus{URL: "https://huangguoai.ai", State: "failed", Message: "保留永久入口的原检查记录", CheckedAt: time.Now()}
	r.mu.Lock()
	r.disk.Publications = append(r.disk.Publications, old)
	if err := r.saveLocked(); err != nil {
		t.Fatal(err)
	}
	r.mu.Unlock()
	restored := &Downloader{cfg: d.cfg}
	_, statuses, _ := restored.huangguoMirrorStore().snapshot()
	for _, status := range statuses {
		if status.URL == old.URL && status.State == old.State && status.Message == old.Message {
			return
		}
	}
	t.Fatal("moving the permanent entry out of the publication list erased its saved record")
}

// TestHuangguoCatalogPrefersNewPublicationOverReachablePermanentPage 验证「Huangguo Catalog Prefers New Publication Over Reachable Permanent Page」回归场景。
func TestHuangguoCatalogPrefersNewPublicationOverReachablePermanentPage(t *testing.T) {
	engine, _ := fullAICatalogFixture(t, "permanent-old")
	origin, _, err := engine.downloader.chooseHuangguoCatalogOrigin(context.Background())
	if err != nil || origin != "https://fresh.example.org" {
		t.Fatal("a reachable permanent page displaced the newly published entry", origin, err)
	}
}

// TestStartupAICatalogCanBeStoppedWhileQueued 验证「Startup AI Catalog Can Be Stopped While Queued」回归场景。
func TestStartupAICatalogCanBeStoppedWhileQueued(t *testing.T) {
	engine, reads := fullAICatalogFixture(t, "normal")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	engine.sourceTasks[sourceHuangguoAI] = &nativeSourceTask{cancel: cancel}
	engine.sourceRecords[sourceHuangguoAI] = nativeSourceRecord{Operation: "check", Running: true}
	engine.scheduleHuangguoStartupCatalog()
	engine.stopHuangguoStartupCatalog()
	engine.mu.Lock()
	delete(engine.sourceTasks, sourceHuangguoAI)
	engine.mu.Unlock()
	time.Sleep(30 * time.Millisecond)
	if ctx.Err() != nil || reads.Load() != 0 {
		t.Fatal("canceling queued startup changed an unrelated task or fetched data")
	}
}
