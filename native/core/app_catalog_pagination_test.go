package core

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"golang.org/x/net/html"
)

// TestCatalogPaginationConfirmedOverlapCanAdvance 验证「Catalog Pagination Confirmed Overlap Can Advance」回归场景。
func TestCatalogPaginationConfirmedOverlapCanAdvance(t *testing.T) {
	p := &nativeCatalogPagination{Current: 5, TotalPages: 2553, Verified: true}
	r := nativeCatalogResult{Page: 5, HasMore: true, Items: []nativeDrama{{ID: "huaguo:1"}}, Pagination: p}
	if !sourceCatalogPageAdvanced(r) {
		t.Fatal("confirmed page was blocked by repeated work IDs")
	}
	for _, changed := range []nativeCatalogResult{
		{Page: 5, HasMore: true, Items: r.Items},
		{Page: 4, HasMore: true, Items: r.Items, Pagination: p},
		{Page: 5, Items: r.Items, Pagination: p},
		{Page: 5, HasMore: true, Pagination: p},
	} {
		if sourceCatalogPageAdvanced(changed) {
			t.Fatal("unconfirmed or nonadvancing page accepted", changed)
		}
	}
}

// TestCatalogPaginationWebIdentityAndVisiblePager 验证「Catalog Pagination Web Identity And Visible Pager」回归场景。
func TestCatalogPaginationWebIdentityAndVisiblePager(t *testing.T) {
	for _, source := range []string{sourceHuaguo, sourceWuguo} {
		address, body := "https://ordinary.example/search.html?page=5&searchtype=5&tid=27&year=", `<ul class="hashed-page"><li class="active"><a href="?page=5&searchtype=5&tid=27&year=">5</a></li><li><a href="?page=2553&searchtype=5&tid=27&year=">尾页</a></li><li><a href="?page=9999&searchtype=5&tid=99&year=">9999</a></li><li hidden><a href="?page=9998&searchtype=5&tid=27&year=">9998</a></li></ul>`
		if source == sourceWuguo {
			address, body = "https://ordinary.example/index.php/vod/type/id/1/page/5.html", `<div class="paginationCom_pageContent"><div class="paginationCom_activePage">5</div><a href="/index.php/vod/type/id/1/page/6.html">6</a><a href="/index.php/vod/type/id/2/page/9999.html">9999</a><input data-total="2400"></div>`
		}
		doc, _ := html.Parse(strings.NewReader(body))
		cursor := &sourceCatalogCursor{}
		ctx := context.WithValue(context.Background(), sourceCatalogCursorKey{}, cursor)
		p, err := sourceMaccmsPagination(ctx, doc, source, address, 5)
		if err != nil || p == nil || !p.Verified || p.Current != 5 || p.TotalPages != map[string]int{sourceHuaguo: 2553, sourceWuguo: 2400}[source] || cursor.Pagination == nil {
			t.Fatalf("%s %+v %v", source, p, err)
		}
		if _, err = sourceMaccmsPagination(ctx, doc, source, address, 6); err == nil {
			t.Fatal("clamped page accepted", source)
		}
	}
}

// TestCatalogPaginationHeguoRejectsWrongPageAndCategory 验证「Catalog Pagination Heguo Rejects Wrong Page And Category」回归场景。
func TestCatalogPaginationHeguoRejectsWrongPageAndCategory(t *testing.T) {
	for _, fields := range []string{`"page":1,"pages":450,"typeThreeId":"585"`, `"page":62,"pages":450,"typeThreeId":"462"`} {
		d := sourceFixtureDownloader(t, func(r *http.Request) (*http.Response, error) {
			return sourceFixtureResponse(r, 200, `<script id="__NEXT_DATA__" type="application/json">{"props":{"pageProps":{`+fields+`,"bookList":[]}}}</script>`), nil
		})
		if _, _, err := d.fetchHeguoCatalogPage(context.Background(), 62, "585"); err == nil {
			t.Fatal("different source page accepted", fields)
		}
	}
}

type soraniInterruptedBody struct{ closed *bool }

// Read 读取SoraniInterrupted正文。
func (body soraniInterruptedBody) Read([]byte) (int, error) { return 0, io.ErrUnexpectedEOF }

// Close 关闭SoraniInterrupted正文并释放其持有的资源。
func (body soraniInterruptedBody) Close() error { *body.closed = true; return nil }

// TestSoraniReadRecoveryRetriesOnlyTransientBodyOnce 验证「Sorani Read Recovery Retries Only Transient Body Once」回归场景。
func TestSoraniReadRecoveryRetriesOnlyTransientBodyOnce(t *testing.T) {
	for _, mode := range []string{"recovered", "interrupted", "403", "429", "oversize", "invalid"} {
		t.Run(mode, func(t *testing.T) {
			attempts, closed := 0, false
			d := sourceFixtureDownloader(t, func(r *http.Request) (*http.Response, error) {
				attempts++
				response := sourceFixtureResponse(r, 200, `{"code":200,"data":{}}`)
				if mode == "interrupted" || mode == "recovered" && attempts == 1 {
					response.Body = soraniInterruptedBody{closed: &closed}
				}
				if mode == "403" {
					response.StatusCode = 403
				}
				if mode == "429" {
					response.StatusCode = 429
					response.Header.Set("Retry-After", "120")
				}
				if mode == "oversize" {
					response.Body = io.NopCloser(strings.NewReader(strings.Repeat("x", 101)))
				}
				if mode == "invalid" {
					response.Body = io.NopCloser(strings.NewReader("not JSON"))
				}
				return response, nil
			})
			_, err := d.soraniRequest(context.Background(), "/video", nil, 100)
			if mode == "recovered" {
				if err != nil || attempts != 2 || !closed {
					t.Fatal(attempts, closed, err)
				}
			} else {
				want := 1
				if mode == "interrupted" {
					want = 2
				}
				if err == nil || attempts != want {
					t.Fatal(attempts, err)
				}
				if mode == "oversize" && !strings.Contains(err.Error(), "超过读取上限") {
					t.Fatal(err)
				}
				if mode == "interrupted" && !strings.Contains(err.Error(), "读取中断") {
					t.Fatal(err)
				}
			}
		})
	}
}

// TestSoraniCatalogRefreshBudgetAndPageIdentity 验证「Sorani Catalog Refresh Budget And Page Identity」回归场景。
func TestSoraniCatalogRefreshBudgetAndPageIdentity(t *testing.T) {
	d := sourceFixtureDownloader(t, func(r *http.Request) (*http.Response, error) {
		deadline, ok := r.Context().Deadline()
		if !ok || time.Until(deadline) < 29*time.Second {
			t.Fatal("background catalog budget still too short")
		}
		return sourceFixtureResponse(r, 200, `{"code":200,"data":{"current":1,"total":3845,"records":[]}}`), nil
	})
	ctx := context.WithValue(context.Background(), backgroundCatalogKey{}, true)
	if _, _, err := d.fetchSoraniCatalogPage(ctx, 41, "1", ""); err == nil || !strings.Contains(err.Error(), "页码") {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := d.soraniRequest(ctx, "/video", nil, 100); !errors.Is(err, context.Canceled) && err == nil {
		t.Fatal(err)
	}
}

// TestCatalogPaginationResultHasExplicitProvenance 验证「Catalog Pagination Result Has Explicit Provenance」回归场景。
func TestCatalogPaginationResultHasExplicitProvenance(t *testing.T) {
	data, err := json.Marshal(nativeCatalogResult{Page: 5, Pagination: &nativeCatalogPagination{Current: 5, TotalPages: 2553, Verified: true, Rule: "huaguo_visible_pager"}})
	if err != nil || !strings.Contains(string(data), `"pagination":{"current":5,"totalPages":2553,"verified":true`) {
		t.Fatal(string(data), err)
	}
}
