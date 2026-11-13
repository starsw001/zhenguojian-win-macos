package core

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
)

// TestSourceRulesLocalFirstForEveryNonAISource 验证「Source Rules Local First For Every Non AI Source」回归场景。
func TestSourceRulesLocalFirstForEveryNonAISource(t *testing.T) {
	var calls atomic.Int32
	engine := sourceFixtureEngine(t, func(r *http.Request) (*http.Response, error) {
		calls.Add(1)
		return nil, errors.New("local query must not use network")
	})
	sources := []string{sourceHongguo, sourceHanxiaoquan, sourceGuipian, sourceSorani, sourceHuangdou, sourceHuangju, sourceYeguo, sourceDSD, sourceHuangguoVideo, sourceCloudFront}
	for _, spec := range duanjuProviderCatalog {
		sources = append(sources, spec.ID)
	}
	if len(sources) != 22 {
		t.Fatal(len(sources))
	}
	for _, source := range sources {
		for i := 0; i < 121; i++ {
			engine.catalogs[source] = append(engine.catalogs[source], nativeDrama{ID: fmt.Sprintf("%s:%d", source, i), Source: source, Title: fmt.Sprintf("普通本地剧集%d", i)})
		}
		for page := 1; page <= 3; page++ {
			result, err := engine.nativeCatalog(context.Background(), nativeInput{Source: source, Query: "普通本地剧集", Page: page})
			want := 60
			if page == 3 {
				want = 1
			}
			if err != nil || !result.LocalSearch || len(result.Items) != want || result.HasMore != (page < 3) || result.SearchState != "local_hit" {
				t.Fatalf("%s page%d: %+v %v", source, page, result, err)
			}
		}
	}
	if calls.Load() != 0 {
		t.Fatal(calls.Load())
	}
}

// sourceRulesSoraniFixture 返回站源规则SoraniFixture。
func sourceRulesSoraniFixture(t *testing.T, mode string) (*nativeEngine, *atomic.Int32) {
	t.Helper()
	old := buildAllSources
	buildAllSources = "true"
	t.Cleanup(func() { buildAllSources = old })
	reads := &atomic.Int32{}
	engine := sourceFixtureEngine(t, func(r *http.Request) (*http.Response, error) {
		reads.Add(1)
		if mode == "blocked" {
			return sourceFixtureResponse(r, 503, "ordinary temporary unavailable"), nil
		}
		if mode == "invalid" {
			return sourceFixtureResponse(r, 200, `{"code":200,"data":null}`), nil
		}
		page, _ := strconv.Atoi(r.URL.Query().Get("page"))
		category, _ := strconv.Atoi(r.URL.Query().Get("categoryId"))
		if mode == "repeated" {
			page = 1
		}
		rows := []map[string]any{}
		if mode != "empty" {
			for i := 0; i < 24; i++ {
				rows = append(rows, map[string]any{"id": category*10000 + page*100 + i + 1, "title": fmt.Sprintf("普通末页新剧%d-%d-%d", category, page, i), "cover": "https://images.example.org/ordinary.jpg", "episodeCount": 3})
			}
		}
		body, _ := json.Marshal(map[string]any{"code": 200, "data": map[string]any{"records": rows, "total": 240}})
		if mode == "empty" {
			body = []byte(`{"code":200,"data":{"records":[],"total":0}}`)
		}
		return sourceFixtureResponse(r, 200, string(body)), nil
	})
	engine.downloader.cfg.SoraniAPIURL = "https://catalog.example.org"
	return engine, reads
}

// TestSourceRulesOnlineMissEmptyAndFailureDistinct 验证「Source Rules Online Miss Empty And Failure Distinct」回归场景。
func TestSourceRulesOnlineMissEmptyAndFailureDistinct(t *testing.T) {
	for _, mode := range []string{"normal", "empty", "invalid", "blocked"} {
		t.Run(mode, func(t *testing.T) {
			engine, reads := sourceRulesSoraniFixture(t, mode)
			result, err := engine.nativeCatalog(context.Background(), nativeInput{Source: sourceSorani, Query: "普通未缓存新剧", Page: 1})
			if reads.Load() == 0 {
				t.Fatal("local miss skipped online")
			}
			if mode == "invalid" || mode == "blocked" {
				if err == nil || result.SearchState == "empty_confirmed" {
					t.Fatalf("bad result %+v %v", result, err)
				}
			} else if err != nil || result.SearchState != map[string]string{"normal": "online_hit", "empty": "empty_confirmed"}[mode] {
				t.Fatalf("%+v %v", result, err)
			}
		})
	}
}

// TestSourceRulesFullPaginationFreshDataAndPreservation 验证「Source Rules Full Pagination Fresh Data And Preservation」回归场景。
func TestSourceRulesFullPaginationFreshDataAndPreservation(t *testing.T) {
	engine, reads := sourceRulesSoraniFixture(t, "normal")
	engine.catalogs[sourceSorani] = []nativeDrama{{ID: "sorani:old", Source: sourceSorani, Title: "保留旧索引"}}
	engine.catalogStates[sourceSorani] = nativeCatalogState{Page: 99, HasMore: true}
	engine.catalogs[sourceHuangguoAI] = []nativeDrama{{ID: "huangguoai:keep", Title: "普通AI索引"}}
	aiBefore, _ := json.Marshal(engine.catalogs[sourceHuangguoAI])
	if err := engine.refreshSourceByRule(context.Background(), sourceSorani); err != nil {
		t.Fatal(err)
	}
	if reads.Load() != 30 {
		t.Fatal("expected 3 categories x 10 pages", reads.Load())
	}
	status := engine.sourceStatus(sourceSorani)
	if status.CatalogPages != 30 || status.CatalogFetched != 720 || status.Count != 721 || status.CatalogFullAt.IsZero() || status.HasMore {
		t.Fatalf("%+v", status)
	}
	result, err := engine.nativeCatalog(context.Background(), nativeInput{Source: sourceSorani, Query: "普通末页新剧5-10-23", Page: 1})
	if err != nil || len(result.Items) != 1 || !result.LocalSearch {
		t.Fatalf("last page not indexed %+v %v", result, err)
	}
	aiAfter, _ := json.Marshal(engine.catalogs[sourceHuangguoAI])
	if string(aiBefore) != string(aiAfter) {
		t.Fatal("AI changed")
	}
}

// TestSourceRulesRepeatedPagesNeverComplete 验证「Source Rules Repeated Pages Never Complete」回归场景。
func TestSourceRulesRepeatedPagesNeverComplete(t *testing.T) {
	engine, _ := sourceRulesSoraniFixture(t, "repeated")
	err := engine.refreshSourceByRule(context.Background(), sourceSorani)
	if err == nil || !strings.Contains(err.Error(), "分页重复") || !engine.sourceStatus(sourceSorani).CatalogFullAt.IsZero() {
		t.Fatalf("%v", err)
	}
}

// TestSourceRulesUnsupportedSearchIsNotEmpty 验证「Source Rules Unsupported Search Is Not Empty」回归场景。
func TestSourceRulesUnsupportedSearchIsNotEmpty(t *testing.T) {
	engine := sourceFixtureEngine(t, func(r *http.Request) (*http.Response, error) {
		return sourceFixtureResponse(r, 403, "ordinary denied"), nil
	})
	for _, source := range []string{sourceHuangdou, sourceCloudFront, sourceHuangguoVideo} {
		result, err := engine.nativeCatalog(context.Background(), nativeInput{Source: source, Query: "普通未缓存剧集", Page: 1})
		if err == nil || result.SearchState == "empty_confirmed" {
			t.Fatalf("%s %+v %v", source, result, err)
		}
	}
}

// TestSourceRulesRealNextPageAndVideoConnectionGate 验证「Source Rules Real Next Page And Video Connection Gate」回归场景。
func TestSourceRulesRealNextPageAndVideoConnectionGate(t *testing.T) {
	if !sourceHTMLHasMore(`<a rel="next" href="?page=2">下一页</a>`, "https://ordinary.example/list?page=1", 1) {
		t.Fatal("next page lost")
	}
	if sourceHTMLHasMore(`<a class="disabled" href="#">下一页</a><a href="/drama/999">999</a>`, "https://ordinary.example/list?page=2", 2) {
		t.Fatal("invented next page")
	}
	row := huangguoMirrorRecord{Kind: sourceHuangguoVideo, State: "connected", Browse: true, Selection: true, MediaReachable: true, Poster: true, PosterWidth: 10, PosterHeight: 10, PosterBytes: 40}
	if !usableHuangguoMirror(row, sourceHuangguoVideo) || usableHuangguoMirror(row, sourceHuangguoAI) || row.Playback {
		t.Fatal("connectivity confused with playback")
	}
}

// TestSourceRulesSearchEnvelopeNeverConfusesErrorsWithEmpty 验证「Source Rules Search Envelope Never Confuses Errors With Empty」回归场景。
func TestSourceRulesSearchEnvelopeNeverConfusesErrorsWithEmpty(t *testing.T) {
	for source, payload := range map[string]string{
		sourceYaguo: `{"data":{"list":[]}}`, sourceMaoguo: `{"data":{"list":[]}}`,
		sourceGuanguo: `{"data":[]}`, sourceFanguo: `{"result":{"elements":[]}}`,
		sourceXingguo: `{"data":{"datalist":[]}}`, sourceHeguo: `{"data":{"bookList":[]}}`, sourceNiuguo: `{"list":[]}`,
	} {
		var response map[string]any
		_ = json.Unmarshal([]byte(payload), &response)
		if err := sourceSearchEnvelope(response, source); err != nil {
			t.Fatalf("%s valid empty: %v", source, err)
		}
		for _, broken := range []string{`{"code":503,"data":[]}`, `{"data":null}`, `{"success":false,"data":[]}`, `{}`} {
			_ = json.Unmarshal([]byte(broken), &response)
			if sourceSearchEnvelope(response, source) == nil {
				t.Fatalf("%s accepted %s", source, broken)
			}
			response = nil
		}
	}
}

// TestSourceRulesDynamicCategoriesAndMaoguoZero 验证「Source Rules Dynamic Categories And Maoguo Zero」回归场景。
func TestSourceRulesDynamicCategoriesAndMaoguoZero(t *testing.T) {
	if !validNativeCategory(sourceMaoguo, "0") || validNativeCategory(sourceHeguo, "0") {
		t.Fatal("category zero scope incorrect")
	}
	d := sourceFixtureDownloader(t, func(r *http.Request) (*http.Response, error) {
		if strings.Contains(r.URL.Path, "/playlet/index") {
			return sourceFixtureResponse(r, 200, `{"data":{"tag_items":[{"tag_id":"0","tag_name":"推荐"},{"tag_id":"12","tag_name":"普通新分类"}]}}`), nil
		}
		return sourceFixtureResponse(r, 500, "unexpected"), nil
	})
	categories, err := d.fetchDuanjuCategories(context.Background(), sourceMaoguo)
	if err != nil || len(categories) != 2 || categories[1].ID != "12" {
		t.Fatalf("dynamic tags shadowed: %+v %v", categories, err)
	}
}

// TestSourceRulesGuanguoDoesNotInventEndAtPage500 验证「Source Rules Guanguo Does Not Invent End At Page 500」回归场景。
func TestSourceRulesGuanguoDoesNotInventEndAtPage500(t *testing.T) {
	var requests atomic.Int32
	d := sourceFixtureDownloader(t, func(r *http.Request) (*http.Response, error) {
		requests.Add(1)
		return sourceFixtureResponse(r, 200, `{"data":[]}`), nil
	})
	_, more, err := d.fetchGuanguoCatalogPage(context.Background(), 501, "")
	if err != nil || more || requests.Load() != 1 {
		t.Fatalf("page501 did not reach source: %d %t %v", requests.Load(), more, err)
	}
}

// TestSourceRulesObservedYaguoNestedGroups 验证「Source Rules Observed Yaguo Nested Groups」回归场景。
func TestSourceRulesObservedYaguoNestedGroups(t *testing.T) {
	var payload map[string]any
	_ = json.Unmarshal([]byte(`{"data":{"list":[{"id":1,"class_name":"剧场","alg_class":[{"id":0,"class_name":"全部"},{"id":4,"class_name":"都市"}]},{"id":3,"class_name":"新剧"}]}}`), &payload)
	categories, err := yaguoCategoriesFromResponse(payload)
	if err != nil || len(categories) != 3 || categories[1].ID != "1-4" || categories[2].ID != "3" {
		t.Fatalf("%+v %v", categories, err)
	}
}

// TestSourceRulesMaoguoUsesReturnedCursorAndCurrentTags 验证「Source Rules Maoguo Uses Returned Cursor And Current Tags」回归场景。
func TestSourceRulesMaoguoUsesReturnedCursorAndCurrentTags(t *testing.T) {
	old := buildAllSources
	buildAllSources = "true"
	t.Cleanup(func() { buildAllSources = old })
	var requests atomic.Int32
	engine := sourceFixtureEngine(t, func(r *http.Request) (*http.Response, error) {
		requests.Add(1)
		next := r.URL.Query().Get("next_id")
		id := r.URL.Query().Get("tag_id") + "-1"
		cursor := "7"
		if next != "" {
			if next != "7" {
				t.Errorf("invented next_id %s", next)
			}
			id = r.URL.Query().Get("tag_id") + "-2"
			cursor = "0"
		}
		body, _ := json.Marshal(map[string]any{"data": map[string]any{"tag_items": []map[string]string{{"tag_id": "0", "tag_name": "推荐"}, {"tag_id": "-1", "tag_name": "新剧"}}, "next_id": cursor, "list": []map[string]string{{"playlet_id": id, "title": "普通游标剧集"}}}})
		return sourceFixtureResponse(r, 200, string(body)), nil
	})
	if err := engine.refreshSourceByRule(context.Background(), sourceMaoguo); err != nil {
		t.Fatal(err)
	}
	status := engine.sourceStatus(sourceMaoguo)
	if requests.Load() != 5 || status.CatalogPages != 4 || status.CatalogFetched != 4 || status.CatalogFullAt.IsZero() {
		t.Fatalf("requests=%d %+v", requests.Load(), status)
	}
}

// TestSourceRulesConnectionCheckUsesDuanjuDetailDispatcher 验证「Source Rules Connection Check Uses Duanju Detail Dispatcher」回归场景。
func TestSourceRulesConnectionCheckUsesDuanjuDetailDispatcher(t *testing.T) {
	d := sourceFixtureDownloader(t, func(r *http.Request) (*http.Response, error) {
		return sourceFixtureResponse(r, 200, `<script id="__NEXT_DATA__" type="application/json">{"props":{"pageProps":{"bookInfoVo":{"bookId":"1001","bookName":"普通详情"},"chapterList":[{"chapterId":"1","chapterNum":1,"chapterName":"第1集"}]}}}</script>`), nil
	})
	title, chapters, err := d.sourceCheckChapters(context.Background(), sourceHeguo, "1001")
	if err != nil || title != "普通详情" || len(chapters) != 1 {
		t.Fatalf("dispatcher: %q %+v %v", title, chapters, err)
	}
}

// TestSourceRulesMissingWebPageDoesNotConfirmEmptySearch 验证「Source Rules Missing Web Page Does Not Confirm Empty Search」回归场景。
func TestSourceRulesMissingWebPageDoesNotConfirmEmptySearch(t *testing.T) {
	if sourceExplicitEmpty("404 页面未找到") {
		t.Fatal("error page marked empty")
	}
	if !sourceExplicitEmpty("暂无相关资源") {
		t.Fatal("explicit empty result unrecognized")
	}
}
