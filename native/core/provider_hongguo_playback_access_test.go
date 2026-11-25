package core

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
)

// TestHongguoRequestedPlaybackPageIgnoresLayoutsAndOtherEpisodes 验证「Hongguo Requested Playback Page Ignores Layouts And Other Episodes」回归场景。
func TestHongguoRequestedPlaybackPageIgnoresLayoutsAndOtherEpisodes(t *testing.T) {
	matching := map[string]any{"series_id": "1234", "vid": "5678", "protectedPlayback": true}
	data := map[string]any{"loaderData": map[string]any{
		"player_layout":                   map[string]any{"series_id": "1234", "vid": "5678"},
		"player_(series_id)/(vid)/layout": map[string]any{"series_id": "1234", "vid": "5678"},
		"player_(series_id)/(vid)/page":   matching,
		"player_other/page":               map[string]any{"series_id": "9999", "vid": "5678"},
	}}
	for i := 0; i < 30; i++ {
		page, err := hongguoRequestedPlaybackPage(data, "1234", "5678")
		if err != nil || page["protectedPlayback"] != true {
			t.Fatalf("requested page lost to a layout: %+v, %v", page, err)
		}
	}
	for _, ids := range [][2]string{{"1234", "9999"}, {"9999", "5678"}} {
		if _, err := hongguoRequestedPlaybackPage(map[string]any{"loaderData": map[string]any{"player_page": matching}}, ids[0], ids[1]); err == nil {
			t.Fatal("wrong work or episode accepted")
		}
	}
}

// TestHongguoProtectedPlaybackDoesNotInventMedia 验证「Hongguo Protected Playback Does Not Invent Media」回归场景。
func TestHongguoProtectedPlaybackDoesNotInventMedia(t *testing.T) {
	for _, protected := range []bool{true, false} {
		body := `<script>_ROUTER_DATA={"loaderData":{"player_(series_id)/(vid)/page":{"isSuccess":true,"series_id":"1234","vid":"5678","protectedPlayback":` + map[bool]string{true: "true", false: "false"}[protected] + `,"videoList":{},"publicVideoSeo":{"available":true}}}}</script>`
		d := sourceFixtureDownloader(t, func(request *http.Request) (*http.Response, error) {
			return sourceFixtureResponse(request, 200, body), nil
		})
		media, err := d.resolveHongguoWebMedia(context.Background(), "1234", "5678")
		if err == nil || media.URL != "" || strings.Contains(err.Error(), "protectedPlayback") != protected {
			t.Fatalf("protected metadata became media: %+v %v", media, err)
		}
	}
}

// TestHongguoBackupPlaybackDoesNotRepeatTLSOrAuthorizationFailures 验证「Hongguo Backup Playback Does Not Repeat TLS Or Authorization Failures」回归场景。
func TestHongguoBackupPlaybackDoesNotRepeatTLSOrAuthorizationFailures(t *testing.T) {
	for _, status := range []int{0, http.StatusForbidden, http.StatusTooManyRequests} {
		calls := 0
		d := sourceFixtureDownloader(t, func(request *http.Request) (*http.Response, error) {
			calls++
			if request.URL.Hostname() != "djapi.999888456.xyz" || request.Method != http.MethodGet {
				t.Fatal("backup request changed target")
			}
			if status == 0 {
				return nil, errors.New("remote error: tls: no application protocol")
			}
			return sourceFixtureResponse(request, status, "unavailable"), nil
		})
		d.cfg.Retries = 3
		if _, err := d.resolveHongguoPlaybackAPI(context.Background(), "1234", "5678"); err == nil || calls != 1 {
			t.Fatalf("unavailable endpoint repeated: status=%d calls=%d error=%v", status, calls, err)
		}
	}
}

// TestHongguoWebRouteChecksOnlyOfficialSameEpisode 验证「Hongguo Web Route Checks Only Official Same Episode」回归场景。
func TestHongguoWebRouteChecksOnlyOfficialSameEpisode(t *testing.T) {
	const publicAddress = "https://synthetic-fixture.qznovelvod.com/5678.mp4"
	for _, tc := range []struct {
		name, page, wantError string
		wantCalls             int
	}{
		{"public same episode", `"series_id":"1234","vid":"5678","protectedPlayback":false,"video_player_info":{"vid":"5678","main_url":"` + publicAddress + `"}`, "", 1},
		{"protected despite address", `"series_id":"1234","vid":"5678","protectedPlayback":true,"video_player_info":{"vid":"5678","main_url":"` + publicAddress + `"}`, "protectedPlayback", 1},
		{"same episode public preview", `"series_id":"1234","vid":"5678","protectedPlayback":true,"publicVideoSeo":{"available":true,"seriesId":"1234","itemId":"5678","durationSeconds":151.267}`, "公开预览不是正片", 2},
		{"other episode public preview", `"series_id":"1234","vid":"5678","protectedPlayback":true,"publicVideoSeo":{"available":true,"seriesId":"1234","itemId":"9999"}`, "protectedPlayback", 1},
		{"wrong page episode", `"series_id":"1234","vid":"9999","video_player_info":{"main_url":"` + publicAddress + `"}`, "未返回所请求", 1},
		{"wrong media episode", `"series_id":"1234","vid":"5678","video_player_info":{"video_id":"9999","main_url":"` + publicAddress + `"}`, "分集与请求不符", 1},
		{"wrong media work", `"series_id":"1234","vid":"5678","video_player_info":{"series_id":"9999","main_url":"` + publicAddress + `"}`, "作品与请求不符", 1},
		{"unrelated address", `"series_id":"1234","vid":"5678","video_player_info":{"main_url":"https://not-hongguo.example.invalid/5678.mp4"}`, "未提供所选分集", 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			d := sourceFixtureDownloader(t, func(r *http.Request) (*http.Response, error) {
				calls++
				if calls == 1 && r.URL.String() == hongguoBaseURL+"/player/1234/5678" && r.Method == http.MethodGet && r.Header.Get("Authorization") == "" && r.Header.Get("Cookie") == "" {
					return sourceFixtureResponse(r, http.StatusOK, `<script>_ROUTER_DATA={"loaderData":{"player_page":{`+tc.page+`}}}</script>`), nil
				}
				if r.URL.Path == hongguoProtectedFallbackPath && r.Method == http.MethodPost && r.Header.Get("Authorization") == "" && r.Header.Get("Cookie") == "" {
					return sourceFixtureResponse(r, http.StatusServiceUnavailable, "unavailable"), nil
				}
				t.Fatalf("unexpected request %d %s %s", calls, r.Method, r.URL.Path)
				return nil, errors.New("unexpected request")
			})
			media, err := d.resolveHongguoWebMedia(context.Background(), "1234", "5678")
			if calls != tc.wantCalls {
				t.Fatalf("unexpected request count: %d", calls)
			}
			if tc.wantError == "" {
				if err != nil || media.URL != publicAddress {
					t.Fatalf("public episode lost: %v, %v", media, err)
				}
			} else if err == nil || media.URL != "" || !strings.Contains(err.Error(), tc.wantError) {
				t.Fatalf("invalid source became playable: %v, %v", media, err)
			}
		})
	}
}

// TestHongguoPublicPageDoesNotFollowRedirect 验证「Hongguo Public Page Does Not Follow Redirect」回归场景。
func TestHongguoPublicPageDoesNotFollowRedirect(t *testing.T) {
	calls := 0
	d := sourceFixtureDownloader(t, func(r *http.Request) (*http.Response, error) {
		calls++
		res := sourceFixtureResponse(r, http.StatusTemporaryRedirect, "")
		res.Header.Set("Location", "https://example.invalid/other-episode")
		return res, nil
	})
	if media, err := d.resolveHongguoWebMedia(context.Background(), "1234", "5678"); err == nil || media.URL != "" || calls != 1 {
		t.Fatalf("redirect followed: %v %v calls=%d", media, err, calls)
	}
}
