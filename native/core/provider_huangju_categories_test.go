package core

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"reflect"
	"strings"
	"testing"
)

// TestHuangjuCategoryNamesSplitCombinedLabels 验证「Huangju Category Names Split Combined Labels」回归场景。
func TestHuangjuCategoryNamesSplitCombinedLabels(t *testing.T) {
	row := map[string]any{
		"category": " 都市，重生；都市 ",
		"categories": []any{
			map[string]any{"name": "青春、都市"},
			"穿越|奇幻,爱情",
			strings.Repeat("长", 25),
			"异常\n标签",
		},
	}
	want := []string{"都市", "重生", "青春", "穿越", "奇幻", "爱情"}
	if got := huangjuCategoryNames(row); !reflect.DeepEqual(got, want) {
		t.Fatalf("combined metadata categories were not normalized: %q", got)
	}
	row["id"], row["slug"], row["title"] = "7", "sample", "合成分类剧"
	drama, err := huangjuDramaFromMap(row, "https://huangju.test", "")
	if err != nil || drama.Category != "都市" || !reflect.DeepEqual(drama.Tags, want) {
		t.Fatalf("normalized primary category and tags disagree: %+v %v", drama, err)
	}
}

// TestHuangjuCategoryDiscoveryKeepsValidRowsBeyondOldLimit 验证「Huangju Category Discovery Keeps Valid Rows Beyond Old Limit」回归场景。
func TestHuangjuCategoryDiscoveryKeepsValidRowsBeyondOldLimit(t *testing.T) {
	rows := []any{
		map[string]any{"slug": "hot", "name": "热门"},
		map[string]any{"slug": "urban", "name": " 都市 "},
		map[string]any{"slug": "rebirth", "name": "重生"},
		map[string]any{"slug": "urban-alias", "name": "都市"},
		map[string]any{"slug": "urban", "name": "重复编号"},
		map[string]any{"slug": "../bad", "name": "无效地址"},
		map[string]any{"slug": "long-label", "name": strings.Repeat("长", 25)},
		nil,
	}
	for len(rows) < 374 {
		rows = append(rows, map[string]any{"slug": fmt.Sprintf("combined-%d", len(rows)), "name": "都市，重生"})
	}
	rows = append(rows, map[string]any{"slug": "qing-chun", "name": "青春"})
	body, _ := json.Marshal(map[string]any{"data": rows})
	d := providerParityDownloader(t, func(request *http.Request, _ url.Values) (*http.Response, error) {
		switch request.URL.Path {
		case "/auth/guest":
			return sourceFixtureResponse(request, 200, `{"token":"category-fixture"}`), nil
		case "/categories":
			return sourceFixtureResponse(request, 200, string(body)), nil
		default:
			t.Fatalf("unexpected category fixture request: %s", request.URL.Path)
			return nil, nil
		}
	})
	categories, err := d.fetchHuangjuCategories(context.Background())
	want := []nativeCategory{{ID: "@new", Name: "最新"}, {ID: "urban", Name: "都市"}, {ID: "rebirth", Name: "重生"}, {ID: "qing-chun", Name: "青春"}}
	if err != nil || !reflect.DeepEqual(categories, want) {
		t.Fatalf("valid categories were rejected with malformed rows or old count limit: %+v %v", categories, err)
	}
}

// TestHuangjuCategoryDiscoveryRetainsSizeAndSchemaBounds 验证「Huangju Category Discovery Retains Size And Schema Bounds」回归场景。
func TestHuangjuCategoryDiscoveryRetainsSizeAndSchemaBounds(t *testing.T) {
	for _, body := range []string{`{"data":{}}`, `{"data":[{"slug":"combined","name":"都市，重生"}]}`, `{"data":[` + strings.Repeat(`{},`, nativeCategoryMaxEntries) + `{}` + `]}`} {
		t.Run(fmt.Sprint(len(body)), func(t *testing.T) {
			d := providerParityDownloader(t, func(request *http.Request, _ url.Values) (*http.Response, error) {
				if request.URL.Path == "/auth/guest" {
					return sourceFixtureResponse(request, 200, `{"token":"category-fixture"}`), nil
				}
				return sourceFixtureResponse(request, 200, body), nil
			})
			if _, err := d.fetchHuangjuCategories(context.Background()); err == nil {
				t.Fatal("invalid category response was accepted")
			}
		})
	}
}

// TestHuangjuCachedCategoryMenuCleansWithoutDeletingTitles 验证「Huangju Cached Category Menu Cleans Without Deleting Titles」回归场景。
func TestHuangjuCachedCategoryMenuCleansWithoutDeletingTitles(t *testing.T) {
	engine := sourceFixtureEngine(t, func(request *http.Request) (*http.Response, error) {
		t.Fatalf("cached categories must not make remote requests: %s", request.URL)
		return nil, nil
	})
	engine.categoryOptions = map[string][]nativeCategory{sourceHuangju: {
		{Name: "全部"}, {ID: "@new", Name: "最新"}, {ID: "combined", Name: "都市，重生"}, {ID: "urban", Name: "都市"}, {ID: "alias", Name: "都市"},
	}}
	engine.catalogs[sourceHuangju] = []nativeDrama{{ID: "huangju:preserved-1", Source: sourceHuangju, Category: "都市，重生"}}
	before := append([]nativeDrama{}, engine.catalogs[sourceHuangju]...)
	got, err := engine.nativeCategories(context.Background(), sourceHuangju, false)
	want := []nativeCategory{{Name: "全部"}, {ID: "@new", Name: "最新"}, {ID: "urban", Name: "都市"}}
	if err != nil || !reflect.DeepEqual(got, want) || !reflect.DeepEqual(engine.catalogs[sourceHuangju], before) {
		t.Fatalf("cached menu cleanup changed titles or kept malformed chips: %+v %v", got, err)
	}
}

// TestHuangjuCategoryCatalogUsesOriginalSlug 验证「Huangju Category Catalog Uses Original Slug」回归场景。
func TestHuangjuCategoryCatalogUsesOriginalSlug(t *testing.T) {
	d := providerParityDownloader(t, func(request *http.Request, query url.Values) (*http.Response, error) {
		if request.URL.Path == "/auth/guest" {
			return sourceFixtureResponse(request, 200, `{"token":"category-fixture"}`), nil
		}
		if request.URL.Path != "/dramas" || query.Get("category") != "urban" || query.Get("sort") != "" || query.Get("q") != "" {
			t.Fatalf("category display name replaced server slug: %s", request.URL)
		}
		return sourceFixtureResponse(request, 200, `{"items":[{"id":"7","slug":"sample","title":"合成都市剧","categories":[{"name":"都市，重生"}]}],"page":1,"pageSize":20,"total":1}`), nil
	})
	items, more, err := d.fetchHuangjuCatalogPage(context.Background(), 1, "urban", "")
	if err != nil || more || len(items) != 1 || items[0].Category != "都市" || !reflect.DeepEqual(items[0].Tags, []string{"都市", "重生"}) {
		t.Fatalf("category catalog could not load by original slug: %+v %v", items, err)
	}
}
