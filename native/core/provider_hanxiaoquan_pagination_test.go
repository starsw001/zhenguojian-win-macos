package core

import (
	"context"
	"net/http"
	"strings"
	"testing"
)

// TestHanPaginationHiddenNextDoesNotExtendTerminal 验证「Han Pagination Hidden Next Does Not Extend Terminal」回归场景。
func TestHanPaginationHiddenNextDoesNotExtendTerminal(t *testing.T) {
	for _, style := range []string{"display:none;", "DISPLAY : none !important", "visibility: hidden"} {
		body := `<div id="page"><a href="/hxq/3-23.html">23</a><span class="page-current">24</span><a href="/hxq/3-25.html" style="` + style + `">下一页</a><a href="/hxq/3-24.html">尾页</a></div>`
		current, last := hanxiaoquanPageRange(body, "3")
		if current != 24 || last != 24 || hanxiaoquanPageCount(body, "3", 24, 36) != 24 {
			t.Fatal(current, last, style)
		}
	}
}

// TestHanPaginationVisibleNextAndSinglePage 验证「Han Pagination Visible Next And Single Page」回归场景。
func TestHanPaginationVisibleNextAndSinglePage(t *testing.T) {
	body := `<div id="page"><span class="page-current">23</span><a href="/hxq/3-24.html">下一页</a><a href="/hxq/3-24.html">尾页</a></div>`
	if hanxiaoquanPageCount(body, "3", 23, 36) != 24 {
		t.Fatal("visible next page was dropped")
	}
	body = `<div id="page"><a href="/hxq/4-0.html" hidden>上一页</a><span class="page-current">1</span><a href="/hxq/4-2.html" style="display:none">下一页</a><a href="/hxq/4.html">尾页</a></div>`
	if hanxiaoquanPageCount(body, "4", 1, 29) != 1 {
		t.Fatal("single full page fabricated another page")
	}
}

// TestHanPaginationHiddenAncestorAndOtherCategories 验证「Han Pagination Hidden Ancestor And Other Categories」回归场景。
func TestHanPaginationHiddenAncestorAndOtherCategories(t *testing.T) {
	body := `<div id="page"><span class="page-current">1</span><div aria-hidden="true"><a href="/hxq/4-99.html">99</a></div><a href="/hxq/3-100.html">其他分类</a><a href="/hxq/4.html">尾页</a></div>`
	if hanxiaoquanPageCount(body, "4", 1, 36) != 1 {
		t.Fatal("hidden or foreign-category link extended current category")
	}
}

// TestHanPaginationUnknownTemplateStaysConservative 验证「Han Pagination Unknown Template Stays Conservative」回归场景。
func TestHanPaginationUnknownTemplateStaysConservative(t *testing.T) {
	if hanxiaoquanPageCount(`<a href="/hxq/3-5.html">5</a>`, "3", 2, 36) != 5 {
		t.Fatal("old template visible pagination lost")
	}
	if hanxiaoquanPageCount(`<div>没有分页证据</div>`, "3", 2, 36) != 3 {
		t.Fatal("unknown full page was treated as a proven terminal")
	}
}

// TestHanPaginationRejectsClampedPageWithoutLosingCorrectCards 验证「Han Pagination Rejects Clamped Page Without Losing Correct Cards」回归场景。
func TestHanPaginationRejectsClampedPageWithoutLosingCorrectCards(t *testing.T) {
	body := `<a href="/hanxiaoquan/77.html" title="普通韩剧" class="module-item-pic"><img data-src="/77.jpg"></a><div id="page"><span class="page-current">24</span><a href="/hxq/3-25.html" style="display:none">下一页</a><a href="/hxq/3-24.html">尾页</a></div>`
	d := sourceFixtureDownloader(t, func(request *http.Request) (*http.Response, error) {
		return sourceFixtureResponse(request, 200, body), nil
	})
	items, more, err := d.fetchHanxiaoquanCatalogPage(context.Background(), 24, "3", "")
	if err != nil || more || len(items) != 1 || items[0].ID != "hanxiaoquan:77" || items[0].Title != "普通韩剧" {
		t.Fatal(items, more, err)
	}
	items, _, err = d.fetchHanxiaoquanCatalogPage(context.Background(), 25, "3", "")
	if err == nil || len(items) != 0 || !strings.Contains(err.Error(), "返回第24页") {
		t.Fatal("clamped duplicate page accepted", items, err)
	}
}
