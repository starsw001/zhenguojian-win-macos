package core

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

// TestHongguoResiliencePublicRouteStopsAtProtectedPage 验证「Hongguo Resilience Public Route Stops At Protected Page」回归场景。
func TestHongguoResiliencePublicRouteStopsAtProtectedPage(t *testing.T) {
	calls := []string{}
	d := sourceFixtureDownloader(t, func(r *http.Request) (*http.Response, error) {
		calls = append(calls, r.URL.Host)
		switch r.URL.Host {
		case "api5-normal-sinfonlineb.fqnovel.com":
			return nil, io.ErrUnexpectedEOF
		case "hongguoduanju.com":
			return sourceFixtureResponse(r, 200, `<script>_ROUTER_DATA={"loaderData":{"player_page":{"isSuccess":true,"series_id":"1234","vid":"5678","protectedPlayback":true}}}}</script>`), nil
		}
		t.Fatalf("untrusted alternate endpoint: %s", r.URL.Host)
		return nil, errors.New("unexpected")
	})
	c := d.hongguoClient()
	c.state.DeviceID, c.state.InstallID, c.state.Registered = "123456789", "987654321", true
	_, err := d.resolveHongguoMedia(context.Background(), Task{DramaID: "hongguo:1234", Index: 1, Chapter: Chapter{VideoURL: "hongguo-cenc://5678"}})
	if err == nil || !strings.Contains(err.Error(), "protectedPlayback") || strings.Join(calls, ",") != "api5-normal-sinfonlineb.fqnovel.com,hongguoduanju.com" {
		t.Fatalf("wrong fallback sequence: %v %v", calls, err)
	}
}

// TestHongguoResilienceCacheScopeExpiryAndReset 验证「Hongguo Resilience Cache Scope Expiry And Reset」回归场景。
func TestHongguoResilienceCacheScopeExpiryAndReset(t *testing.T) {
	var c hongguoMediaCache
	now := time.Unix(1700000000, 0)
	media := providerMedia{URL: "https://fixture.qznovelvod.com/one.mp4?expire=1700000009", CENCKey: []byte{1, 2, 3}}
	_, generation, _ := c.get("series/one", false, now)
	c.put("series/one", generation, media, now)
	media.CENCKey[0] = 0
	if result, _, ok := c.get("series/one", false, now.Add(6*time.Second)); !ok || result.CENCKey[0] != 1 {
		t.Fatal("cache did not preserve the approved result")
	}
	if _, _, ok := c.get("series/two", false, now); ok {
		t.Fatal("cache crossed episode")
	}
	if _, _, ok := c.get("series/one", false, now.Add(7*time.Second)); ok {
		t.Fatal("cache crossed signed expiry safety margin")
	}
	c.put("series/one", generation, media, now)
	if _, _, ok := c.get("series/one", true, now); ok {
		t.Fatal("forced online lookup used cache")
	}
	c.clear()
	c.put("series/one", generation, media, now)
	if _, _, ok := c.get("series/one", false, now); ok {
		t.Fatal("in-flight obsolete result survived a network setting change")
	}
}

// TestHongguoResilienceProxyRetryPreservesExactRead 验证「Hongguo Resilience Proxy Retry Preserves Exact Read」回归场景。
func TestHongguoResilienceProxyRetryPreservesExactRead(t *testing.T) {
	for _, status := range []int{0, 200, 401, 403, 429} {
		const body = `{"mixed_video_id_map":{"1004":["5678"]}}`
		ctx := context.WithValue(context.Background(), hongguoMetadataReadKey{}, true)
		url := hongguoMultiVideoAPI + "?iid=123&device_id=456"
		request, _ := http.NewRequestWithContext(ctx, http.MethodPost, url, strings.NewReader(body))
		router := autoProxyFixtureRouter("auto")
		calls := 0
		base := sourceFixtureTransport(func(r *http.Request) (*http.Response, error) {
			calls++
			data, e := io.ReadAll(r.Body)
			proxy, _ := router.proxy(r)
			if e != nil || string(data) != body || r.URL.String() != url || (calls == 1) != (proxy != nil) {
				t.Fatalf("request changed: calls=%d proxy=%v", calls, proxy)
			}
			if calls == 1 && status == 0 {
				return nil, io.EOF
			}
			if status == 0 {
				return sourceFixtureResponse(r, 200, "recovered fixture metadata"), nil
			}
			return sourceFixtureResponse(r, status, ""), nil
		})
		res, err := newAutoProxyFallbackTransport(base, router, nil).RoundTrip(request)
		if err != nil || res == nil || status == 0 && calls != 2 || status != 0 && (calls != 1 || res.StatusCode != status) {
			t.Fatalf("status=%d calls=%d err=%v", status, calls, err)
		}
		res.Body.Close()
	}
}
