package core

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"testing"
)

// TestHongguoBackupCDNRoutes 验证「Hongguo Backup CDN Routes」回归场景。
func TestHongguoBackupCDNRoutes(t *testing.T) {
	for _, status := range []int{404, 403} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			calls := []string{}
			const fixture = "synthetic-only-media"
			engine := sourceFixtureEngine(t, func(req *http.Request) (*http.Response, error) {
				host := req.URL.Hostname()
				calls = append(calls, host)
				switch host {
				case "api5-normal-sinfonlineb.fqnovel.com":
					return sourceFixtureResponse(req, 200, `{"code":0,"data":{"5678":{"video_model":{"video_list":[{"codec_type":"h264","definition":"720p","backup_url_1":"https://first-fixture.qznovelvod.com/5678","backup_url_2":"https://second-fixture.qznovelvod.com/5678","backup_urls":["https://second-fixture.qznovelvod.com/5678",null,5,{"url":"https://untrusted.example/5678"},"https://untrusted.example/5678"]}]}}}}`), nil
				case "first-fixture.qznovelvod.com":
					if req.URL.Path != "/5678" {
						t.Fatal("episode changed")
					}
					return sourceFixtureResponse(req, status, "fixture rejection"), nil
				case "second-fixture.qznovelvod.com":
					if req.URL.Path != "/5678" {
						t.Fatal("episode changed")
					}
					res := sourceFixtureResponse(req, 200, fixture)
					res.Header.Set("Content-Type", "video/mp4")
					return res, nil
				default:
					t.Fatalf("unexpected host %q", host)
					return nil, nil
				}
			})
			client := engine.downloader.hongguoClient()
			client.state.DeviceID, client.state.InstallID, client.state.Registered = "123456789", "987654321", true
			ctx := context.Background()
			media, err := engine.downloader.resolveHongguoMultiMedia(ctx, "5678")
			out := map[string]any{"firstStatus": status, "routes": len(media.Variants), "playbackBytes": false, "downloadBytes": false}
			if err != nil {
				out["metadataError"] = err.Error()
			} else {
				plan, e := engine.nativeOpenPlayback(ctx, nativePlaybackChoicesForSource(sourceHongguo, media, 720))
				if e != nil {
					t.Fatal(e)
				}
				defer engine.stream.server.Close()
				localClient := &http.Client{Transport: &http.Transport{}}
				resp, e := localClient.Get(plan.URL)
				if e != nil {
					t.Fatal(e)
				}
				io.Copy(io.Discard, resp.Body)
				resp.Body.Close()
				next, e := engine.nativeNextPlayback(ctx, plan.Session)
				if e == nil {
					resp, e = localClient.Get(next.URL)
					if e != nil {
						t.Fatal(e)
					}
					b, readErr := io.ReadAll(resp.Body)
					resp.Body.Close()
					out["playbackBytes"] = readErr == nil && string(b) == fixture
				}
				engine.downloads.resolve = func(context.Context, nativeDownloadJob) (providerMedia, error) { return media, nil }
				job := nativeDownloadJob{ID: "backup-cdn", Drama: nativeDrama{ID: "hongguo:1234"}, Index: 1, Quality: 720}
				_, e = engine.downloads.transfer(ctx, job)
				if e == nil {
					b, readErr := os.ReadFile(filepath.Join(engine.downloads.jobDirectory(job), "media.mp4"))
					out["downloadBytes"] = readErr == nil && string(b) == fixture
				}
			}
			out["requests"] = calls
			if len(media.Variants) != 2 || out["playbackBytes"] != (status == 404) || out["downloadBytes"] != (status == 404) {
				t.Errorf("unexpected backup outcome: %v", out)
			}
			for _, h := range calls {
				if h == "second-fixture.qznovelvod.com" && status != 404 {
					t.Error("403 triggered backup")
				}
			}
		})
	}
}
