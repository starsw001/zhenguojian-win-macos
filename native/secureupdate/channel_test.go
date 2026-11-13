package secureupdate

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

// testAtom 返回testAtom。
func testAtom(tags ...string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "<feed xmlns=\"%s\"><id>tag:github.com,2008:%s</id>", atomNamespace, ReleasesURL)
	for _, tag := range tags {
		fmt.Fprintf(&b, "<entry><link rel=\"alternate\" href=\"%s/tag/%s\"/></entry>", ReleasesURL, tag)
	}
	b.WriteString("</feed>")
	return b.String()
}

// publicResponse 构造公开响应。
func publicResponse(code int, body string) *http.Response {
	return &http.Response{StatusCode: code, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}
}

// TestReleaseLinksAreScoped 验证「Release Links Are Scoped」回归场景。
func TestReleaseLinksAreScoped(t *testing.T) {
	for _, x := range []struct {
		addr string
		ok   bool
	}{{ReleasesURL + "/tag/tag1.1.30", true}, {ReleasesURL + "/tag/v1.1.29%2B160", true}, {"https://github.com/other/project/releases/tag/tag1.1.30", false}, {ReleasesURL + "/tag/tag1.01.29", false}, {"https://user@github.com/" + Repository + "/releases/tag/tag1.1.30", false}, {ReleasesURL + "/tag/tag1.1.30#ignored", false}, {ReleasesURL + "/tag/tag1.1.30?x", false}, {ReleasesURL + "/tag/%2e%2e%2ftag1.1.30", false}, {ReleasesURL + "/tag/tag1.1.30/other", false}} {
		if _, ok := linkedRelease(x.addr, false); ok != x.ok {
			t.Fatal(x.addr, ok)
		}
	}
}

// TestFeedScopeAndEmpty 验证「Feed Scope And Empty」回归场景。
func TestFeedScopeAndEmpty(t *testing.T) {
	bad := strings.ReplaceAll(testAtom("tag1.1.29"), "tag:github.com,2008:"+ReleasesURL, "tag:github.com,2008:alien")
	for _, body := range []string{bad, "<html>login</html>", strings.Replace(testAtom("tag1.1.30"), "rel=\"alternate\"", "rel=\"enclosure\"", 1), strings.Replace(testAtom("tag1.1.29"), ReleasesURL+"/tag", "https://else.invalid/tag", 1)} {
		if _, e := parseReleaseFeed([]byte(body)); e == nil || errors.Is(e, ErrNoPublishedRelease) {
			t.Fatal("invalid or untrusted content was treated as a current release")
		}
	}
	if _, e := parseReleaseFeed([]byte(testAtom())); !errors.Is(e, ErrNoPublishedRelease) {
		t.Fatal(e)
	}
}

// TestPublicPageFallbackIsBoundedAndScoped 验证「Public Page Fallback Is Bounded And Scoped」回归场景。
func TestPublicPageFallbackIsBoundedAndScoped(t *testing.T) {
	page := "<div class='Box-body'><div class='markdown-body'><span class='f1'><a class='Link--primary' href='/" + Repository + "/releases/tag/tag9.9.9'>untrusted release notes</a></span></div><span class='f1'><a class='Link--primary Link' href='/" + Repository + "/releases/tag/tag1.1.30'>real</a></span></div>"
	for _, status := range []int{404, 502} {
		n := 0
		c := &http.Client{Transport: transport(func(req *http.Request) (*http.Response, error) {
			n++
			switch req.URL.String() {
			case ReleaseFeedURL:
				return publicResponse(status, ""), nil
			case ReleasesURL:
				return publicResponse(200, page), nil
			}
			t.Fatal(req.URL)
			return nil, nil
		})}
		r, e := LatestPublishedRelease(context.Background(), c)
		if e != nil || r.Tag != "tag1.1.30" || n != 2 {
			t.Fatal(r, e, n)
		}
	}
	if _, e := parseReleasePage([]byte("<html>sign in</html>")); e == nil || errors.Is(e, ErrNoPublishedRelease) {
		t.Fatal("misreported error page")
	}
}

// TestRateLimitDefersFurtherRequests 验证「Rate Limit Defers Further Requests」回归场景。
func TestRateLimitDefersFurtherRequests(t *testing.T) {
	for _, code := range []int{403, 429, 503} {
		n := 0
		c := NewPublicClient(transport(func(r *http.Request) (*http.Response, error) {
			n++
			s := publicResponse(code, "")
			s.Header.Set("Retry-After", "120")
			return s, nil
		}))
		for j := 0; j < 2; j++ {
			_, e := LatestPublishedRelease(context.Background(), c)
			var limit *updateLimit
			if !errors.As(e, &limit) || limit.until.Before(time.Now().Add(110*time.Second)) {
				t.Fatalf("did not honor Retry-After %d %v", code, e)
			}
		}
		if n != 1 {
			t.Fatal("sent a second request or tried another channel", code, n)
		}
	}
}

// TestNoOfferWithMissingAssetsOrOldTag 验证「No Offer With Missing Assets Or Old Tag」回归场景。
func TestNoOfferWithMissingAssetsOrOldTag(t *testing.T) {
	for _, ed := range []string{"hongguoshang", "zhenguoshang"} {
		for _, x := range []struct {
			tag   string
			newer bool
		}{{"tag1.1.30", true}, {"tag1.1.29", false}, {"tag1.1.28", false}} {
			n := 0
			c := &http.Client{Transport: transport(func(r *http.Request) (*http.Response, error) {
				n++
				if n == 1 {
					return publicResponse(200, testAtom(x.tag)), nil
				}
				if !strings.HasSuffix(r.URL.Path, ed+"-windows-x64.update.json") {
					t.Fatal(r.URL)
				}
				return publicResponse(404, ""), nil
			})}
			o, e := Discover(context.Background(), c, ed, "fixture", "", "1.1.29+160")
			if o != nil || (e != nil) != x.newer || x.newer && !strings.Contains(e.Error(), ed+"-windows-x64.update.json") {
				t.Fatal(o, e)
			}
		}
	}
}
