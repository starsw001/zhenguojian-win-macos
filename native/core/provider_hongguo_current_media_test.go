package core

import (
	"context"
	"crypto/md5"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"testing"
)

func TestHongguoCurrentVideoModelUsesSingleEpisodeID(t *testing.T) {
	d := sourceFixtureDownloader(t, func(request *http.Request) (*http.Response, error) {
		if request.URL.Path != hongguoVideoModelPath {
			t.Fatalf("path=%q", request.URL.Path)
		}
		body, _ := io.ReadAll(request.Body)
		var payload map[string]any
		if json.Unmarshal(body, &payload) != nil || payload["video_id"] != "5678" || payload["mixed_video_id_map"] != nil {
			t.Fatalf("payload=%s", body)
		}
		biz, _ := payload["biz_param"].(map[string]any)
		if biz["device_level"] != float64(3) || biz["video_platform"] != float64(1024) {
			t.Fatalf("biz_param=%v", biz)
		}
		if request.Header.Get("X-Gorgon") == "" || request.Header.Get("X-SS-STUB") != fmt.Sprintf("%X", md5.Sum(body)) {
			t.Fatal("current request signature missing")
		}
		return sourceFixtureResponse(request, 200, `{"code":0,"data":{"video_model":"{\"video_list\":[{\"main_url\":\"https://fixture.qznovelvod.com/5678.mp4\",\"video_meta\":{\"codec_type\":\"h264\",\"definition\":\"720p\"}}]}"}}`), nil
	})
	client := d.hongguoClient()
	client.state.DeviceID, client.state.InstallID, client.state.Registered = "123456789", "987654321", true
	media, err := d.resolveHongguoAppMedia(context.Background(), "5678")
	if err != nil || !media.Direct || media.URL != "https://fixture.qznovelvod.com/5678.mp4" || media.Quality != 720 {
		t.Fatalf("media=%+v err=%v", media, err)
	}
}
