package secureupdate

import (
	"context"
	"net/http"
	"strings"
	"testing"
)

// TestPublicTagSelectionAndIdentity 验证「Public Tag Selection And Identity」回归场景。
func TestPublicTagSelectionAndIdentity(t *testing.T) {
	c := &http.Client{Transport: transport(func(q *http.Request) (*http.Response, error) {
		if q.URL.String() != ReleaseFeedURL || q.Method != "GET" || q.Header.Get("Authorization") != "" || q.Header.Get("Cookie") != "" {
			t.Fatal(q.URL)
		}
		return publicResponse(200, testAtom("tag1.1.8", "tag1.1.028", "tag1.1.29", "tag1.1.28")), nil
	})}
	r, e := LatestPublishedRelease(context.Background(), c)
	if e != nil || r.Tag != "tag1.1.29" || !r.NewerThan("1.1.28+159") || r.NewerThan("1.1.29+160") || !r.matches("1.1.29+160") || r.matches("1.1.28+159") {
		t.Fatal(r, e)
	}
	for _, code := range []int{401, 403, 429} {
		n := 0
		c.Transport = transport(func(q *http.Request) (*http.Response, error) { n++; return publicResponse(code, ""), nil })
		if _, e = LatestPublishedRelease(context.Background(), c); e == nil || n != 1 {
			t.Fatal("access limit ignored", code)
		}
	}
}

// TestShortTagCannotInstallDifferentSignedVersion 验证「Short Tag Cannot Install Different Signed Version」回归场景。
func TestShortTagCannotInstallDifferentSignedVersion(t *testing.T) {
	ed := "hongguoshang"
	f := newFixture(t, ed, nil)
	_, meta, _ := f.offer(t, nil)
	c := &http.Client{Transport: transport(func(q *http.Request) (*http.Response, error) {
		if q.URL.String() == ReleaseFeedURL {
			return publicResponse(200, testAtom("tag1.1.29")), nil
		}
		if !strings.HasSuffix(q.URL.Path, "/"+ed+"-windows-x64.update.json") {
			t.Fatal(q.URL)
		}
		return publicResponse(200, string(meta)), nil
	})}
	if o, e := Discover(context.Background(), c, ed, f.i.ID, f.i.Key, "1.1.27+158"); o != nil || e == nil || !strings.Contains(e.Error(), "签名版本不符") {
		t.Fatal(o, e)
	}
}
