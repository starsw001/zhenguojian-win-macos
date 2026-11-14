package core

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"
)

// TestNativeCategoryPaginationStaysIndependent 验证「Native Category Pagination Stays Independent」回归场景。
func TestNativeCategoryPaginationStaysIndependent(t *testing.T) {
	engine := sourceFixtureEngine(t, func(request *http.Request) (*http.Response, error) {
		if !strings.HasPrefix(request.URL.Path, "/api/videos/category/ai-") {
			return nil, errors.New("only catalog JSON is permitted")
		}
		category := request.URL.Path[strings.LastIndex(request.URL.Path, "/")+1:]
		page := request.URL.Query().Get("page")
		count := 24
		if page != "1" {
			count = 1
		}
		rows := make([]map[string]any, count)
		for index := range rows {
			rows[index] = map[string]any{"id": fmt.Sprintf("%s-%s-%d", category, page, index), "title": "合成分类剧集"}
		}
		body, _ := json.Marshal(map[string]any{"data": rows})
		return sourceFixtureResponse(request, 200, string(body)), nil
	})
	for _, input := range []nativeInput{
		{Source: sourceHuangguoAI, Category: "ai-duanju", Page: 1},
		{Source: sourceHuangguoAI, Category: "ai-duanju", Page: 2},
		{Source: sourceHuangguoAI, Category: "ai-manju", Page: 1},
	} {
		if _, err := engine.nativeCatalog(context.Background(), input); err != nil {
			t.Fatal(err)
		}
	}
	short := engine.nativeCached(nativeCatalogKey(sourceHuangguoAI, "ai-duanju"))
	comic := engine.nativeCached(nativeCatalogKey(sourceHuangguoAI, "ai-manju"))
	if short.Page != 2 || short.HasMore || len(short.Items) != 25 || comic.Page != 1 || !comic.HasMore || len(comic.Items) != 24 {
		t.Fatal("category cursors or entries leaked", short.Page, short.HasMore, len(short.Items), comic.Page, comic.HasMore, len(comic.Items))
	}
	for _, row := range comic.Items {
		if !strings.Contains(row.ID, "ai-manju") {
			t.Fatal("another category was merged into the selected list")
		}
	}
	if len(engine.nativeCached(sourceHuangguoAI).Items) != 49 {
		t.Fatal("source management lost categorized titles")
	}
	restored := &nativeEngine{directory: engine.directory, catalogs: map[string][]nativeDrama{}, catalogStates: map[string]nativeCatalogState{}}
	restored.loadCatalogCache()
	if restored.nativeCached(nativeCatalogKey(sourceHuangguoAI, "ai-duanju")).Page != 2 {
		t.Fatal("category position was not persisted")
	}
}

// TestNativeCategoryDiscoveryUsesSourceLinks 验证「Native Category Discovery Uses Source Links」回归场景。
func TestNativeCategoryDiscoveryUsesSourceLinks(t *testing.T) {
	categories := parseHuangguoVideoCategories(`<a href="/videos">全部</a><a href="/videos?category=2&amp;sort=hot"><span>合成分类甲</span></a><a href="/videos?category=5">合成分类乙</a><a href="/videos?category=2">重复</a><a href="/other?category=8">无关</a><a href="/videos?category=../bad">无效</a>`)
	if len(categories) != 2 || categories[0].ID != "2" || categories[0].Name != "合成分类甲" || categories[1].ID != "5" {
		t.Fatal("category discovery included unrelated navigation", categories)
	}
	for _, source := range []string{sourceHongguo, sourceHuangdou, sourceHuangguoAI, sourceHuangguoVideo, sourceCloudFront} {
		if validNativeCategory(source, "../other|source") {
			t.Fatal("invalid category entered a cache namespace")
		}
		if err := nativeAuthorizeInput(nativeInput{Action: "categories", Source: source}); (err == nil) != nativeSourceAvailable(source) {
			t.Fatal("categories ignored edition permissions", source, err)
		}
	}
}

func TestHuangguoWebsiteCategoriesMatchCurrentNavigation(t *testing.T) {
	expected := []nativeCategory{
		{ID: "original", Name: "黄果原创"},
		{ID: "ai-duanju", Name: "短剧"},
		{ID: "ai-manju", Name: "漫剧"},
		{ID: "topics", Name: "专题"},
		{ID: "ranks-hot", Name: "排行榜"},
	}
	if len(nativeAIWebsiteCategories) != len(expected) {
		t.Fatal(nativeAIWebsiteCategories)
	}
	for index, entry := range expected {
		if nativeAIWebsiteCategories[index] != entry || !validNativeCategory(sourceHuangguoAI, entry.ID) {
			t.Fatal(index, nativeAIWebsiteCategories[index], entry)
		}
	}
	if path, name, valid := huangguoAIWebsiteCategory(""); !valid || path != "/" || name != "全部" {
		t.Fatal(path, name, valid)
	}
	expectedChildren := []nativeCategory{
		{ID: "original-manju", Name: "原创漫剧"},
		{ID: "original-duanju", Name: "原创短剧"},
		{ID: "ai-duanju-hot", Name: "当前热播"},
		{ID: "ai-duanju-random", Name: "精选推荐"},
		{ID: "ai-manju-hot", Name: "当前热播"},
		{ID: "ai-manju-random", Name: "精选推荐"},
		{ID: "ranks-recommend", Name: "推荐榜"},
		{ID: "ranks-potential", Name: "飙升榜"},
	}
	if len(nativeAIWebsiteSubcategories) != len(expectedChildren) {
		t.Fatal(nativeAIWebsiteSubcategories)
	}
	for index, entry := range expectedChildren {
		if nativeAIWebsiteSubcategories[index] != entry || !validNativeCategory(sourceHuangguoAI, entry.ID) {
			t.Fatal(index, nativeAIWebsiteSubcategories[index], entry)
		}
	}
	for category, expectedPath := range map[string]string{
		"original":          "/original/",
		"original-manju":    "/original/manju/",
		"original-duanju":   "/original/duanju/",
		"ai-duanju":         "/ai-duanju/",
		"ai-duanju-hot":     "/ai-duanju/hot/",
		"ai-duanju-random":  "/ai-duanju/random/",
		"ai-manju":          "/ai-manju/",
		"ai-manju-hot":      "/ai-manju/hot/",
		"ai-manju-random":   "/ai-manju/random/",
		"topics":            "/topics/",
		"topic:fixture-one": "/topics/fixture-one/",
		"ranks-hot":         "/ranks/hot/",
		"ranks-recommend":   "/ranks/recommend/",
		"ranks-potential":   "/ranks/potential/",
	} {
		path, _, valid := huangguoAIWebsiteCategory(category)
		if !valid || path != expectedPath {
			t.Fatal(category, path, valid)
		}
	}
}

func TestHuangguoWebsiteCategoriesReturnPlayableWorkRoutes(t *testing.T) {
	d := sourceFixtureDownloader(t, func(request *http.Request) (*http.Response, error) {
		card := func(id, title string) string {
			return `<div class="hg-drama-card" data-track-id="` + id + `"><a href="/video/` + id + `/"><h3 class="hg-drama-card__title">` + title + `</h3></a></div>`
		}
		if strings.HasPrefix(request.URL.Path, "/ranks/") {
			board := strings.Trim(strings.TrimPrefix(request.URL.Path, "/ranks/"), "/")
			return sourceFixtureResponse(request, 200, `<div class="hg-rank-item" data-track-id="104" data-track-title="榜单剧集"><a href="/video/104/"><img data-src="https://images.example.org/104.jpg"></a></div><script type="application/ld+json">{"@graph":[{"@type":"ItemList","@id":"https://ordinary.example.org/ranks/`+board+`/#itemlist","itemListElement":[{"position":1,"name":"榜单剧集","url":"https://ordinary.example.org/video/104/"}]}]}</script>`), nil
		}
		switch request.URL.Path {
		case "/":
			return sourceFixtureResponse(request, 200, `<main class="hg-main">`+card("101", "首页剧集")+`</main>`), nil
		case "/original/", "/original/manju/", "/original/duanju/", "/ai-duanju/", "/ai-duanju/hot/", "/ai-duanju/random/", "/ai-manju/", "/ai-manju/hot/", "/ai-manju/random/", "/topics/", "/topics/fixture-one/":
			return sourceFixtureResponse(request, 200, `<div class="hg-card-grid">`+card("102", "分类剧集")+`</div>`), nil
		default:
			return nil, fmt.Errorf("unexpected route %s", request.URL.Path)
		}
	})
	ctx := context.WithValue(context.Background(), huangguoCatalogOriginKey{}, "https://ordinary.example.org")
	for _, category := range []string{"", "original", "original-manju", "original-duanju", "ai-duanju", "ai-duanju-hot", "ai-duanju-random", "ai-manju", "ai-manju-hot", "ai-manju-random", "topics", "topic:fixture-one", "ranks-hot", "ranks-recommend", "ranks-potential"} {
		items, more, err := d.fetchHuangguoAICatalogPage(ctx, 1, category)
		if err != nil || more || len(items) != 1 || !strings.HasPrefix(items[0].SourceID, "https://ordinary.example.org/video/") {
			t.Fatal(category, len(items), more, err, items)
		}
		if strings.HasPrefix(category, "ranks-") && items[0].Cover != "https://images.example.org/104.jpg" {
			t.Fatal("ranking cover missing", items[0])
		}
	}
}

func TestHuangguoWebsiteCategoryPaginationUsesCurrentRoutes(t *testing.T) {
	requested := ""
	d := sourceFixtureDownloader(t, func(request *http.Request) (*http.Response, error) {
		requested = request.URL.Path
		return sourceFixtureResponse(request, 200, `<div class="hg-card-grid is-active"><div class="hg-drama-card" data-track-id="page-two"><a href="/video/page-two/"><h3 class="hg-drama-card__title">第二页剧集</h3></a></div></div>`), nil
	})
	ctx := context.WithValue(context.Background(), huangguoCatalogOriginKey{}, "https://ordinary.example.org")
	items, more, err := d.fetchHuangguoAICatalogPage(ctx, 2, "ai-duanju-hot")
	if err != nil || more || len(items) != 1 || requested != "/ai-duanju/hot/2/" {
		t.Fatal(requested, len(items), more, err)
	}
}

// TestLegacyResource404KeepsHealthyEndpoint 验证「Legacy Resource 404 Keeps Healthy Endpoint」回归场景。
func TestLegacyResource404KeepsHealthyEndpoint(t *testing.T) {
	d := legacyFixtureDownloader(t, func(request *http.Request) (*http.Response, error) {
		if strings.Contains(request.URL.Path, "/detail/") {
			return sourceFixtureResponse(request, 404, "removed drama"), nil
		}
		return legacyFixtureResponse(request, []Tab{{ID: "active", Name: "合成分类"}}), nil
	})
	d.cfg.Token = "fixture-session"
	d.cfg.InterfaceKey, d.cfg.ParamKey, d.cfg.ParamIV = fixtureLegacyProtocol.InterfaceKey, fixtureLegacyProtocol.ParamKey, fixtureLegacyProtocol.ParamIV
	d.apiBase = d.cfg.APIBase
	var detail detailResponse
	if err := d.fetchAPI(context.Background(), "/api/app/playlet/detail/removed", nil, &detail); err == nil {
		t.Fatal("a missing drama was treated as available")
	}
	if d.apiBase != d.cfg.APIBase || len(d.apiFailures) != 0 {
		t.Fatal("one removed drama disabled a healthy API host")
	}
	var tabs legacyTabList
	if err := d.fetchAPI(context.Background(), "/api/app/playlet-tab/all", nil, &tabs); err != nil || len(tabs) != 1 {
		t.Fatal("catalog did not remain usable", err)
	}
}
