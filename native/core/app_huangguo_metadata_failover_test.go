package core

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// metadataFailoverFixture 返回元数据FailoverFixture。
func metadataFailoverFixture(t *testing.T, mode string) (*nativeEngine, map[string]int) {
	t.Helper()
	oldBuild, oldPublications, oldPermanent := buildAllSources, huangguoPublicationURLs, huangguoAIPermanentURLs
	buildAllSources = "true"
	huangguoPublicationURLs = []string{"https://publication.example.org"}
	huangguoAIPermanentURLs = nil
	t.Cleanup(func() {
		buildAllSources, huangguoPublicationURLs, huangguoAIPermanentURLs = oldBuild, oldPublications, oldPermanent
	})
	reads := map[string]int{}
	engine := sourceFixtureEngine(t, func(request *http.Request) (*http.Response, error) {
		reads[request.URL.Host+request.URL.Path+"?"+request.URL.RawQuery]++
		if request.URL.Host == "publication.example.org" {
			return sourceFixtureResponse(request, 200, `<a href="https://primary.example.org/">入口一</a><a href="https://backup.example.org/">入口二</a>`), nil
		}
		if request.URL.Host != "primary.example.org" && request.URL.Host != "backup.example.org" {
			return sourceFixtureResponse(request, 404, "ordinary unavailable"), nil
		}
		if strings.HasPrefix(request.URL.Path, "/api/videos/category/") {
			page, _ := strconv.Atoi(request.URL.Query().Get("page"))
			section := request.URL.Path[strings.LastIndex(request.URL.Path, "/")+1:]
			primary := request.URL.Host == "primary.example.org"
			if primary && mode == "blocked" {
				return sourceFixtureResponse(request, 403, "ordinary access denied"), nil
			}
			if primary && page > 1 || !primary && mode == "all-failed" {
				return sourceFixtureResponse(request, 502, "ordinary upstream unavailable"), nil
			}
			prefix := "backup"
			if primary {
				prefix = "primary"
			}
			body, _ := json.Marshal(map[string]any{"data": []map[string]any{{"id": prefix + "-" + section, "title": "普通资料 " + prefix + " " + section, "episodes": 3}}, "pagination": map[string]any{"hasMore": primary}})
			return sourceFixtureResponse(request, 200, string(body)), nil
		}
		if request.URL.Path != "/" {
			t.Fatalf("metadata requested non-catalog content: %s", request.URL.Path)
		}
		return sourceFixtureResponse(request, 200, `<section class="hg-home-popular-tags"><a class="hg-tagrow__item" href="/tag/qihuan/">奇幻</a></section><div class="hg-card-grid"></div>`), nil
	})
	engine.catalogs[sourceHuangguoAI] = []nativeDrama{{ID: "huangguoai:old", Source: sourceHuangguoAI, Title: "旧目录资料"}}
	engine.catalogs[nativeCatalogKey(sourceHuangguoAI, "ai-duanju")] = []nativeDrama{{ID: "huangguoai:stale", Source: sourceHuangguoAI, Title: "旧分类资料"}}
	engine.catalogs[sourceHongguo] = []nativeDrama{{ID: "hongguo:keep", Source: sourceHongguo, Title: "保留其他站目录"}}
	engine.sourceRecords[sourceHuangguoAI] = nativeSourceRecord{MetadataCursor: "keep-history"}
	if err := engine.writeCatalogDiskLocked(); err != nil {
		t.Fatal(err)
	}
	return engine, reads
}

// TestMetadataSingleOriginFailoverDiscardsIncompleteCandidate 验证「Metadata Single Origin Failover Discards Incomplete Candidate」回归场景。
func TestMetadataSingleOriginFailoverDiscardsIncompleteCandidate(t *testing.T) {
	engine, reads := metadataFailoverFixture(t, "fallback")
	beforeConfig := engine.downloader.cfg
	if err := engine.refreshHuangguoAICatalog(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(engine.catalogs[sourceHuangguoAI]) != len(nativeAICategories) || len(engine.catalogs[sourceHongguo]) != 1 {
		t.Fatal("candidate replaced another source or failed to replace stale index")
	}
	for _, row := range engine.catalogs[sourceHuangguoAI] {
		if !strings.Contains(row.Title, "backup") {
			t.Fatal("mixed metadata generations")
		}
	}
	if len(engine.catalogs[nativeCatalogKey(sourceHuangguoAI, "ai-duanju")]) != 0 {
		t.Fatal("old category cache remained visible")
	}
	if engine.downloader.cfg.HuangguoAIURL != beforeConfig.HuangguoAIURL || engine.sourceRecords[sourceHuangguoAI].MetadataCursor != "keep-history" {
		t.Fatal("metadata changed user configuration or historical cursor")
	}
	if reads["publication.example.org/?"] != 1 || reads["primary.example.org/api/videos/category/ai-duanju?sort=hot&page=1&size=24"] != 1 {
		t.Fatal("discovery or validated first page was refetched", reads)
	}
	entries, _, _ := engine.downloader.huangguoMirrorStore().snapshot()
	failed, selected := false, false
	for _, entry := range entries {
		if entry.Origin == "https://primary.example.org" {
			failed = strings.Contains(entry.CatalogMessage, "未完成")
		}
		if entry.Origin == "https://backup.example.org" {
			selected = strings.Contains(entry.CatalogMessage, "同步完成") && !entry.Playback && !entry.Poster
		}
	}
	if !failed || !selected {
		t.Fatal("failed candidate reasons lost or metadata was promoted to media verification")
	}
}

// TestMetadataAllCandidatesFailPreservesCommittedBytes 验证「Metadata All Candidates Fail Preserves Committed Bytes」回归场景。
func TestMetadataAllCandidatesFailPreservesCommittedBytes(t *testing.T) {
	engine, _ := metadataFailoverFixture(t, "all-failed")
	path := filepath.Join(engine.directory, "catalogs.json")
	before, _ := os.ReadFile(path)
	if err := engine.refreshHuangguoAICatalog(context.Background()); err == nil {
		t.Fatal("all failed candidates appeared complete")
	}
	after, _ := os.ReadFile(path)
	if string(before) != string(after) || engine.catalogs[sourceHuangguoAI][0].ID != "huangguoai:old" {
		t.Fatal("incomplete metadata replaced committed catalog")
	}
}

// TestMetadataChallengeDoesNotRotateOrigins 验证「Metadata Challenge Does Not Rotate Origins」回归场景。
func TestMetadataChallengeDoesNotRotateOrigins(t *testing.T) {
	engine, reads := metadataFailoverFixture(t, "blocked")
	if err := engine.refreshHuangguoAICatalog(context.Background()); err == nil || !huangguoCatalogRequiresHuman(err) {
		t.Fatal("challenge was not handed to manual processing", err)
	}
	for key := range reads {
		if strings.HasPrefix(key, "backup.example.org") {
			t.Fatal("challenge bypassed by origin rotation")
		}
	}
	if engine.catalogs[sourceHuangguoAI][0].ID != "huangguoai:old" {
		t.Fatal("challenge erased catalog")
	}
}

// TestMetadataSaveFailureDoesNotPublishCandidateInMemory 验证「Metadata Save Failure Does Not Publish Candidate In Memory」回归场景。
func TestMetadataSaveFailureDoesNotPublishCandidateInMemory(t *testing.T) {
	engine, _ := metadataFailoverFixture(t, "fallback")
	blocker := filepath.Join(t.TempDir(), "not-directory")
	if err := os.WriteFile(blocker, []byte("ordinary blocker"), 0600); err != nil {
		t.Fatal(err)
	}
	engine.directory = blocker
	err := engine.commitHuangguoCatalog(huangguoCatalogSnapshot{origin: "https://backup.example.org", rows: []nativeDrama{{ID: "huangguoai:new", Source: sourceHuangguoAI, Title: "新目录"}}, pages: 1})
	if err == nil || engine.catalogs[sourceHuangguoAI][0].ID != "huangguoai:old" {
		t.Fatal("failed disk commit still changed visible index")
	}
}

// TestMetadataInterventionIncludesJSONAuthorizationAndLimits 验证「Metadata Intervention Includes JSON Authorization And Limits」回归场景。
func TestMetadataInterventionIncludesJSONAuthorizationAndLimits(t *testing.T) {
	for _, message := range []string{"目录接口返回状态码401", "目录接口返回状态码403", "目录接口返回状态码429", "需要人工验证码"} {
		if !huangguoCatalogRequiresHuman(errors.New(message)) {
			t.Fatal(message)
		}
	}
	if huangguoCatalogRequiresHuman(errors.New("ordinary upstream unavailable")) {
		t.Fatal("ordinary transport failure incorrectly blocked failover")
	}
}
