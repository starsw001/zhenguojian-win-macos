package core

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestHongguoProtectedWebFallbackReturnsRequestedFullEpisode(t *testing.T) {
	const pageURL = hongguoBaseURL + "/player/1234/5678"
	const mediaURL = "https://fixture.qznovelvod.com/full/5678.mp4?token=signed"
	calls := 0
	d := sourceFixtureDownloader(t, func(request *http.Request) (*http.Response, error) {
		calls++
		switch calls {
		case 1:
			if request.Method != http.MethodGet || request.URL.String() != pageURL {
				t.Fatalf("unexpected page request: %s %s", request.Method, request.URL)
			}
			body := `<script>_ROUTER_DATA={"loaderData":{"player_(series_id)/(vid)/page":{"isSuccess":true,"series_id":"1234","vid":"5678","protectedPlayback":true,"publicVideoSeo":{"available":true,"seriesId":"1234","itemId":"5678","durationSeconds":211.834,"width":360,"height":640}}}}</script>`
			return sourceFixtureResponse(request, http.StatusOK, body), nil
		case 2:
			if request.Method != http.MethodPost || request.URL.Path != hongguoProtectedFallbackPath ||
				request.Header.Get("Origin") != hongguoBaseURL || request.Header.Get("Referer") != pageURL ||
				request.Header.Get("Content-Type") != "application/json" {
				t.Fatalf("unexpected fallback request: %s %s %#v", request.Method, request.URL, request.Header)
			}
			payload, _ := io.ReadAll(request.Body)
			var input map[string]string
			if json.Unmarshal(payload, &input) != nil || len(input) != 1 || input["item_id"] != "5678" {
				t.Fatalf("unexpected fallback payload: %s", payload)
			}
			body, _ := json.Marshal(map[string]any{
				"main_url": mediaURL, "url_expire": time.Now().Add(time.Hour).Unix(),
				"base_resp": map[string]any{"StatusCode": 0},
			})
			return sourceFixtureResponse(request, http.StatusOK, string(body)), nil
		default:
			t.Fatalf("unexpected request %d: %s", calls, request.URL)
			return nil, nil
		}
	})
	media, err := d.resolveHongguoWebMedia(context.Background(), "1234", "5678")
	if err != nil || calls != 2 || media.URL != mediaURL || !media.Direct || media.Quality != 360 ||
		media.Duration != 211834*time.Millisecond || media.CENCKey != nil || media.credentials == nil ||
		media.credentials.referer != pageURL || !strings.HasPrefix(media.credentials.origin, "https://fixture.qznovelvod.com") {
		t.Fatalf("full official fallback not selected: calls=%d media=%+v err=%v", calls, media, err)
	}
}

func TestHongguoProtectedWebFallbackRejectsPreviewAndWrongStatus(t *testing.T) {
	preview := map[string]any{"durationSeconds": json.Number("211.834"), "width": json.Number("360"), "height": json.Number("640")}
	for _, tc := range []struct {
		name string
		body string
		want string
	}{
		{"preview", `{"main_url":"https://hongguoduanju.com/video/public/5678/360p.mp4","url_expire":9999999999,"base_resp":{"StatusCode":0}}`, "不受支持"},
		{"wrong status", `{"main_url":"https://fixture.qznovelvod.com/full/5678.mp4","url_expire":9999999999,"base_resp":{"StatusCode":1}}`, "状态 1"},
		{"expired", `{"main_url":"https://fixture.qznovelvod.com/full/5678.mp4","url_expire":1,"base_resp":{"StatusCode":0}}`, "已过期"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d := sourceFixtureDownloader(t, func(request *http.Request) (*http.Response, error) {
				return sourceFixtureResponse(request, http.StatusOK, tc.body), nil
			})
			media, err := d.resolveHongguoProtectedWebMedia(context.Background(), hongguoBaseURL+"/player/1234/5678", preview, "5678")
			if err == nil || media.URL != "" || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("unsafe fallback accepted: media=%+v err=%v", media, err)
			}
		})
	}
}
