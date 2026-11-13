package core

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"golang.org/x/net/html"
)

// TestYeguoCategoriesKeepAll375Options 验证「Yeguo Categories Keep All 375 Options」回归场景。
func TestYeguoCategoriesKeepAll375Options(t *testing.T) {
	options := make([]any, 375)
	for i := range options {
		options[i] = map[string]any{"value": fmt.Sprint(i + 1), "name": fmt.Sprintf("分类%d", i+1)}
	}
	body, _ := json.Marshal(map[string]any{"status": "1", "data": map[string]any{"video_filter": map[string]any{"theme": map[string]any{"list": options}}}})
	d := providerParityDownloader(t, func(request *http.Request, _ url.Values) (*http.Response, error) {
		if request.URL.Path != "/api/home/contentOptions" {
			t.Fatalf("unexpected category request: %s", request.URL)
		}
		return sourceFixtureResponse(request, 200, string(body)), nil
	})
	d.yeguoClient().access = &yeguoAccess{base: "https://api.yeguo.test", identifier: "fixture", loadedAt: time.Now()}
	categories, err := d.fetchYeguoCategories(context.Background())
	if err != nil || len(categories) != 375 || categories[374].ID != "theme:375" {
		t.Fatalf("categories above300 were rejected or truncated: count=%d err=%v", len(categories), err)
	}
}

// TestDSDCategoriesKeepAll375Links 验证「DSD Categories Keep All 375 Links」回归场景。
func TestDSDCategoriesKeepAll375Links(t *testing.T) {
	var body strings.Builder
	for i := 1; i <= 375; i++ {
		fmt.Fprintf(&body, `<a href="/index.php/vod/type/id/%d.html">分类%d</a>`, i, i)
	}
	document, err := html.Parse(strings.NewReader(body.String()))
	if err != nil {
		t.Fatal(err)
	}
	categories, err := parseDSDCategories(document, "https://dsd.test/")
	if err != nil || len(categories) != 375 || categories[374].ID != "375" {
		t.Fatalf("category links were rejected or truncated: count=%d err=%v", len(categories), err)
	}
}

// TestHuangjuCategoriesKeep375IndependentEntries 验证「Huangju Categories Keep 375 Independent Entries」回归场景。
func TestHuangjuCategoriesKeep375IndependentEntries(t *testing.T) {
	rows := make([]any, 375)
	for i := range rows {
		rows[i] = map[string]any{"slug": fmt.Sprintf("category-%d", i), "name": fmt.Sprintf("分类%d", i)}
	}
	body, _ := json.Marshal(rows)
	d := providerParityDownloader(t, func(request *http.Request, _ url.Values) (*http.Response, error) {
		if request.URL.Path == "/auth/guest" {
			return sourceFixtureResponse(request, 200, `{"token":"fixture"}`), nil
		}
		return sourceFixtureResponse(request, 200, string(body)), nil
	})
	categories, err := d.fetchHuangjuCategories(context.Background())
	if err != nil || len(categories) != 376 || categories[375].ID != "category-374" {
		t.Fatalf("independent categories above300 were lost: count=%d err=%v", len(categories), err)
	}
}

// TestUncategorizedSourcesExposeAllWithoutNetwork 验证「Uncategorized Sources Expose All Without Network」回归场景。
func TestUncategorizedSourcesExposeAllWithoutNetwork(t *testing.T) {
	engine := sourceFixtureEngine(t, func(request *http.Request) (*http.Response, error) {
		t.Fatalf("source without a category endpoint made a category request: %s", request.URL)
		return nil, nil
	})
	for _, source := range []string{sourceHuangdou, sourceGuanguo, sourceWangguo, sourceFaguo} {
		categories, err := engine.nativeCategories(context.Background(), source, true)
		if err != nil || len(categories) != 1 || categories[0].ID != "" || categories[0].Name != "全部" {
			t.Fatalf("uncategorized source did not keep All/local filtering: %s %+v %v", source, categories, err)
		}
	}
}
