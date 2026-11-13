package core

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"golang.org/x/net/html"
)

// TestHuangguoPopularTagsPreserveOrderAndDoNotUseCardTags 验证「Huangguo Popular Tags Preserve Order And Do Not Use Card Tags」回归场景。
func TestHuangguoPopularTagsPreserveOrderAndDoNotUseCardTags(t *testing.T) {
	var body strings.Builder
	body.WriteString(`<a class="hg-tagrow__item" href="/tag/outside/">无关</a><section class="hg-home-popular-tags"><div>`)
	for index := 0; index < 350; index++ {
		fmt.Fprintf(&body, `<a class="hg-tagrow__item" href="/tag/topic-%d/"><span>普通标签%d</span></a>`, index, index)
	}
	body.WriteString(`<a class="hg-tagrow__item" href="/tag/topic-0/">重复</a><a class="hg-tagrow__item" href="https://other.example/tag/foreign/">异站</a><a class="hg-tagrow__item" href="/tag/../other/">非法路径</a><a href="/tag/plain/">不属于标签行</a></div></section>`)
	document, err := html.Parse(strings.NewReader(body.String()))
	if err != nil {
		t.Fatal(err)
	}
	tags := parseHuangguoAIPopularTags(document, "https://fixture.example/")
	if len(tags) != 350 || tags[0].ID != "tag:topic-0" || tags[349].Name != "普通标签349" || tags[0].Kind != "tag" {
		t.Fatal("tag order, source boundary or size changed", len(tags))
	}
	for _, value := range []string{"tag:../other", "tag:", "tag:two/parts", "tag:x|y", "tag:%2f", "tag:x\n", "tag:" + strings.Repeat("x", 97)} {
		if validNativeCategory(sourceHuangguoAI, value) {
			t.Fatal("unsafe tag namespace", value)
		}
	}
}

// TestHuangguoPopularTagsReloadWithoutDiscardingSavedOptionsOnFailure 验证「Huangguo Popular Tags Reload Without Discarding Saved Options On Failure」回归场景。
func TestHuangguoPopularTagsReloadWithoutDiscardingSavedOptionsOnFailure(t *testing.T) {
	reads := 0
	fail := false
	engine := sourceFixtureEngine(t, func(request *http.Request) (*http.Response, error) {
		if request.URL.Path != "/" || request.Header.Get("Cache-Control") != "no-cache" {
			t.Fatal("unexpected tag request", request.URL.Path, request.Header)
		}
		if fail {
			return sourceFixtureResponse(request, 403, "ordinary fixture unavailable"), nil
		}
		reads++
		return sourceFixtureResponse(request, 200, fmt.Sprintf(`<section class="hg-home-popular-tags"><a class="hg-tagrow__item" href="/tag/topic-%d/">普通标签</a></section>`, reads)), nil
	})
	for index := 1; index <= 2; index++ {
		categories, err := engine.nativeCategories(context.Background(), sourceHuangguoAI, false)
		if err != nil || len(categories) != 6 || categories[5].ID != fmt.Sprintf("tag:topic-%d", index) {
			t.Fatal("cached options suppressed refresh", index, err)
		}
	}
	fail = true
	if _, err := engine.nativeCategories(context.Background(), sourceHuangguoAI, true); err == nil {
		t.Fatal("failed request appeared current")
	}
	if engine.categoryOptions[sourceHuangguoAI][5].ID != "tag:topic-2" {
		t.Fatal("failure deleted previous tag choices")
	}
}

// TestHuangguoTagPagesUseRemoteSelectionAndIndependentPagination 验证「Huangguo Tag Pages Use Remote Selection And Independent Pagination」回归场景。
func TestHuangguoTagPagesUseRemoteSelectionAndIndependentPagination(t *testing.T) {
	requests := []string{}
	engine := sourceFixtureEngine(t, func(request *http.Request) (*http.Response, error) {
		if request.URL.Path != "/tag/qihuan/" || request.Header.Get("Cache-Control") != "no-cache" {
			t.Fatal("not a fresh tag request", request.URL.Path)
		}
		page := request.URL.Query().Get("page")
		requests = append(requests, page)
		body := `<div class="hg-card-grid">`
		if page != "3" {
			body += fmt.Sprintf(`<div class="hg-drama-card" data-track-id="topic-%s"><a href="/video/topic-%s/"><h3 class="hg-drama-card__title">普通标签剧集</h3></a></div>`, page, page)
		}
		body += `</div><div class="hg-drama-card" data-track-id="unrelated"><h3 class="hg-drama-card__title">相关推荐</h3></div>`
		if page == "1" {
			body += `<a href="?page=2">下一页</a><a href="https://other.example/tag/qihuan/?page=99">异站</a>`
		}
		return sourceFixtureResponse(request, 200, body), nil
	})
	for page := 1; page <= 3; page++ {
		result, err := engine.nativeCatalog(context.Background(), nativeInput{Source: sourceHuangguoAI, Category: "tag:qihuan", Page: page, Force: true})
		if err != nil || result.Page != page || !result.Authoritative || result.HasMore != (page == 1) {
			t.Fatal("tag cursor or empty final page failed", page, result, err)
		}
		if page < 3 && (len(result.Items) != 1 || !strings.HasSuffix(result.Items[0].ID, fmt.Sprintf(":topic-%d", page))) {
			t.Fatal("old entries or unrelated recommendation leaked", result.Items)
		}
		if page == 3 && len(result.Items) != 0 {
			t.Fatal("empty tag page reused history")
		}
	}
	if strings.Join(requests, ",") != "1,2,3" || len(engine.nativeCached(sourceHuangguoAI).Items) != 2 {
		t.Fatal("tag requests or history retention", requests)
	}
}

// TestHuangguoCurrentResponseKeepsHistoricalCatalog 验证「Huangguo Current Response Keeps Historical Catalog」回归场景。
func TestHuangguoCurrentResponseKeepsHistoricalCatalog(t *testing.T) {
	reads := 0
	engine := sourceFixtureEngine(t, func(request *http.Request) (*http.Response, error) {
		reads++
		return sourceFixtureResponse(request, 200, fmt.Sprintf(`{"data":[{"id":"row-%d","title":"普通目录条目"}]}`, reads)), nil
	})
	for index := 0; index < 2; index++ {
		result, err := engine.nativeCatalog(context.Background(), nativeInput{Source: sourceHuangguoAI, Category: "ai-duanju", Page: 1, Force: true})
		if err != nil || len(result.Items) != 1 || result.Page != 1 || result.HasMore || !result.Authoritative {
			t.Fatal("current response polluted by historical cache", result, err)
		}
	}
	if len(engine.nativeCached(sourceHuangguoAI).Items) != 2 {
		t.Fatal("historical records were removed")
	}
}
