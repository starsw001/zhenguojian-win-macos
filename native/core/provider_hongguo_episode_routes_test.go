package core

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestHongguoHistoricalGroupedPlaybackAndDownload(t *testing.T) {
	var model map[string]any
	if err := json.Unmarshal([]byte(`{"video_list":[{"main_url":"https://main.qznovelvod.com/ep57","backup_url_list":[["https://backup1.qznovelvod.com/ep57"],["https://backup2.qznovelvod.com/ep57","https://backup1.qznovelvod.com/ep57"]],"video_meta":{"definition":"720p","codec_type":"h264"}}]}`), &model); err != nil {
		t.Fatal(err)
	}
	m, err := selectHongguoAppMedia(model)
	if err != nil {
		t.Fatal(err)
	}
	e := routes149Engine(t)
	plan, err := e.nativeOpenPlayback(context.Background(), nativePlaybackChoicesForSource(sourceHongguo, m, 0))
	if err != nil {
		t.Fatal(err)
	}
	defer e.nativeReleasePlayback(plan.Session)
	t.Logf("episode=57 advertised=3 selectable=%d", plan.RouteCount)
	if plan.RouteCount != 3 {
		t.Fatal("the nested same-episode backups were lost")
	}
	next, err := e.nativeSelectPlaybackRoute(context.Background(), plan.Session, 2)
	if err != nil || next.RouteIndex != 2 {
		t.Fatal("third address not selectable", err)
	}
	defer e.nativeReleasePlayback(next.Session)
	e.downloads.resolve = func(context.Context, nativeDownloadJob) (providerMedia, error) { return m, nil }
	payload := routes149Body()
	e.downloader.client.Transport = sourceFixtureTransport(func(req *http.Request) (*http.Response, error) {
		if req.URL.Hostname() != "backup2.qznovelvod.com" || req.URL.Path != "/ep57" {
			return nil, fmt.Errorf("wrong mock address")
		}
		res := sourceFixtureResponse(req, 200, string(payload))
		res.Header.Set("Content-Type", "video/mp4")
		res.ContentLength = int64(len(payload))
		return res, nil
	})
	job := nativeDownloadJob{ID: "fixture57", Drama: nativeDrama{ID: "hongguo:1234", Source: sourceHongguo}, Index: 57, Chapter: Chapter{Source: sourceHongguo}, Folder: ".download-cache/fixture57", PreferredRoute: 3, PreferredRouteKey: nativeRouteKey(m.Variants[2])}
	result, err := e.downloads.transfer(context.Background(), job)
	if err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(e.downloads.jobDirectory(job), result.file))
	if err != nil || !bytes.Equal(b, payload) {
		t.Fatal("fixture download mismatch", err)
	}
	t.Logf("selected_address=3 synthetic_download_bytes=%d", len(b))
}

func TestHongguoPlaybackDownloadShareOneEpisodeRead(t *testing.T) {
	const primary = `{"code":0,"data":{"1004":{"5678":{"video_model":{"video_list":[{"main_url":"https://main.qznovelvod.com/ep57","backup_url":"https://backup.qznovelvod.com/ep57","video_meta":{"definition":"720p","codec_type":"h264"}}]}}}}}`
	started, second := make(chan struct{}), make(chan struct{})
	var reads atomic.Int32
	d := sourceFixtureDownloader(t, func(r *http.Request) (*http.Response, error) {
		switch {
		case strings.Contains(r.URL.Path, "device_register"):
			return sourceFixtureResponse(r, 200, `{"device_id_str":"123456789","install_id_str":"987654321"}`), nil
		case r.URL.Hostname() == "api5-normal-sinfonlinea.fqnovel.com":
			n := reads.Add(1)
			if n == 1 {
				close(started)
				select {
				case <-second:
				case <-time.After(350 * time.Millisecond):
				case <-r.Context().Done():
					return nil, r.Context().Err()
				}
				return sourceFixtureResponse(r, 200, primary), nil
			}
			if n == 2 {
				close(second)
			}
			return sourceFixtureResponse(r, 200, `{"code":0,"data":{}}`), nil
		case r.URL.Hostname() == "hongguoduanju.com":
			return sourceFixtureResponse(r, 404, ""), nil
		default:
			return sourceFixtureResponse(r, 200, `{"code":0,"data":{}}`), nil
		}
	})
	c := d.hongguoClient()
	c.state.DeviceID, c.state.InstallID, c.state.Registered = "123456789", "987654321", true
	task := Task{DramaID: "hongguo:1234", Index: 57, Chapter: Chapter{Source: sourceHongguo, VideoURL: "hongguo-cenc://5678"}}
	run := func(out chan int) {
		m, err := d.resolvePlaybackMedia(context.Background(), task)
		if err != nil {
			out <- 0
			return
		}
		out <- len(nativePlaybackChoicesForSource(sourceHongguo, m, 0).media)
	}
	play, download := make(chan int, 1), make(chan int, 1)
	go run(play)
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("first read did not start")
	}
	go run(download)
	p, q := <-play, <-download
	t.Logf("episode=57 first_model_reads=%d playback_addresses=%d download_addresses=%d", reads.Load(), p, q)
	if reads.Load() != 1 || p != 2 || q != 2 {
		t.Fatal("simultaneous consumers did not share the same issued addresses")
	}
}
