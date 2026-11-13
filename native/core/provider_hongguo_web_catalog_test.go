package core

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"testing"
)

// hongguoWebFixture 返回红果网页Fixture。
func hongguoWebFixture(category string, page, totalPages, count int) string {
	route, topic, _, _ := hongguoWebCategory(category)
	canonical := "https://hongguoduanju.com/category/" + route
	if topic != "" {
		canonical += "/" + topic
	}
	rows := []map[string]any{}
	for i := 0; i < count; i++ {
		rows = append(rows, map[string]any{
			"series_id": strconv.Itoa(page*100 + i + 1), "series_name": fmt.Sprintf("网页作品%d-%d", page, i),
			"episode_cnt": 8 + i, "series_cover": "https://images.test/poster.jpg",
		})
	}
	body, _ := json.Marshal(map[string]any{"loaderData": map[string]any{"category_$": map[string]any{
		"isSuccess": true, "categoryRoute": map[string]any{"contentType": route}, "canonicalUrl": canonical,
		"query": map[string]any{"page": page}, "recommendList": rows,
		"pagination":   map[string]any{"pageNum": page, "pageSize": 24, "totalPages": totalPages, "total": (totalPages-1)*24 + 2},
		"selectorList": []any{map[string]any{"items": []any{map[string]any{"selector_item_id": "romance", "show_name": "爱情"}}}},
	}}})
	return "<script>window._ROUTER_DATA = " + string(body) + ";</script>"
}

// TestHongguoWebCategoriesAndVerifiedPaging 验证「Hongguo Web Categories And Verified Paging」回归场景。
func TestHongguoWebCategoriesAndVerifiedPaging(t *testing.T) {
	for _, route := range hongguoWebRoutes {
		category := "web:" + route.slug
		if !validNativeCategory(sourceHongguo, category) || !validNativeCategory(sourceHongguo, category+":romance") {
			t.Fatal("web category not accepted", category)
		}
		for _, page := range []int{1, 2, 3} {
			count := 24
			if page == 3 {
				count = 2
			}
			result, err := parseHongguoWebCatalogPage(hongguoWebFixture(category, page, 3, count), category, page)
			if err != nil || result.Page != page || result.TotalPages != 3 || len(result.Items) != count || len(result.Categories) != 1 {
				t.Fatalf("%s page %d: %+v %v", category, page, result, err)
			}
			if result.Items[0].DisplayTitle() != fmt.Sprintf("网页作品%d-0", page) || fmt.Sprint(result.Items[0].EpisodeCount) != "8" {
				t.Fatal("source title or episode count changed")
			}
		}
	}
	for _, category := range []string{"web:other", "web:comic:", "web:real-drama:../ai-drama", "web:comic:romance:other"} {
		if validNativeCategory(sourceHongguo, category) {
			t.Fatal("invalid route accepted", category)
		}
	}
}

// TestHongguoWebCatalogRejectsCrossCategoryAndRepeatedPage 验证「Hongguo Web Catalog Rejects Cross Category And Repeated Page」回归场景。
func TestHongguoWebCatalogRejectsCrossCategoryAndRepeatedPage(t *testing.T) {
	category := "web:real-drama"
	body := hongguoWebFixture(category, 1, 3, 24)
	cases := map[string]string{
		"other content type": strings.Replace(body, `"contentType":"real-drama"`, `"contentType":"ai-drama"`, 1),
		"other topic":        strings.Replace(body, `/category/real-drama"`, `/category/real-drama/romance"`, 1),
		"partial page":       hongguoWebFixture(category, 1, 3, 23),
		"partial last page":  strings.Replace(hongguoWebFixture(category, 1, 1, 2), `"total":2`, `"total":3`, 1),
		"unrecognized work":  strings.Replace(body, `"series_id":"101"`, `"series_id":"invalid"`, 1),
		"duplicate work":     strings.Replace(body, `"series_id":"102"`, `"series_id":"101"`, 1),
		"failed response":    strings.Replace(body, `"isSuccess":true`, `"isSuccess":false`, 1),
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := parseHongguoWebCatalogPage(body, category, 1); err == nil {
				t.Fatal("accepted mismatched or incomplete response")
			}
		})
	}
	if _, err := parseHongguoWebCatalogPage(body, category, 2); err == nil {
		t.Fatal("repeated first page was treated as page two")
	}
	if _, err := parseHongguoWebCatalogPage(hongguoWebFixture(category+":romance", 1, 3, 24), category+":romance", 1); err != nil {
		t.Fatal(err)
	}
}

// TestHongguoWebNativeCursorsAndMenusIndependent 验证「Hongguo Web Native Cursors And Menus Independent」回归场景。
func TestHongguoWebNativeCursorsAndMenusIndependent(t *testing.T) {
	requests := []string{}
	engine := sourceFixtureEngine(t, func(request *http.Request) (*http.Response, error) {
		if !strings.HasPrefix(request.URL.Path, "/category/") {
			return nil, fmt.Errorf("unexpected non-web request: %s", request.URL)
		}
		requests = append(requests, request.URL.Path+"?"+request.URL.RawQuery)
		category := "web:" + strings.ReplaceAll(strings.TrimPrefix(request.URL.Path, "/category/"), "/", ":")
		page, _ := strconv.Atoi(request.URL.Query().Get("page"))
		page = max(page, 1)
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"text/html"}}, Body: io.NopCloser(strings.NewReader(hongguoWebFixture(category, page, 3, 24))), Request: request}, nil
	})
	menus, err := engine.nativeCategories(context.Background(), sourceHongguo, true)
	if err != nil || len(menus) != 9 || menus[1].Name != "真人剧" || menus[7].Name != "漫画" {
		t.Fatalf("menu mismatch: %+v %v", menus, err)
	}
	for _, input := range []nativeInput{
		{Source: sourceHongguo, Category: "web:real-drama", Page: 1},
		{Source: sourceHongguo, Category: "web:comic-drama", Page: 1},
		{Source: sourceHongguo, Category: "web:real-drama", Page: 2},
		{Source: sourceHongguo, Category: "web:ai-drama:romance", Page: 1},
	} {
		result, err := engine.nativeCatalog(context.Background(), input)
		if err != nil || len(result.Items) != 24 || result.Page != input.Page || result.NextPage != input.Page+1 || !result.HasMore || !result.Authoritative || result.Pagination == nil || !result.Pagination.Verified {
			t.Fatalf("independent cursor failed: %+v %+v %v", input, result, err)
		}
	}
	if engine.catalogStates["hongguo|web:real-drama"].Page != 2 || engine.catalogStates["hongguo|web:comic-drama"].Page != 1 {
		t.Fatal("content-type cursors mixed")
	}
	if len(requests) < 6 {
		t.Fatal("web source was not requested")
	}
}

// TestHongguoWebExplicitEmptyCategoryIsTerminal 验证「Hongguo Web Explicit Empty Category Is Terminal」回归场景。
func TestHongguoWebExplicitEmptyCategoryIsTerminal(t *testing.T) {
	category := "web:real-drama"
	body := strings.Replace(hongguoWebFixture(category, 1, 1, 0), `"total":2`, `"total":0`, 1)
	body = strings.Replace(body, `"totalPages":1`, `"totalPages":0`, 1)
	engine := sourceFixtureEngine(t, func(request *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"text/html"}}, Body: io.NopCloser(strings.NewReader(body)), Request: request}, nil
	})
	result, err := engine.nativeCatalog(context.Background(), nativeInput{Source: sourceHongguo, Category: category, Page: 1})
	if err != nil || len(result.Items) != 0 || result.HasMore || !result.Authoritative {
		t.Fatal("explicit empty page was not preserved", result, err)
	}
}
