package core

import (
	"context"
	"fmt"
	"net/http"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
)

// TestRoutes163AllProvidersExposeActualRoutes 验证「Routes 163 All Providers Expose Actual Routes」回归场景。
func TestRoutes163AllProvidersExposeActualRoutes(t *testing.T) {
	for _, source := range []string{sourceHongguo, sourceSorani, sourceGuipian, sourceHanxiaoquan, sourceYaguo, sourceMaoguo, sourceFanguo, sourceGuanguo, sourceHeguo, sourceXingguo, sourceHuaguo, sourceNiuguo, sourceWangguo, sourceFaguo, sourcePiguo, sourceWuguo, sourceYeguo, sourceHuangdou, sourceHuangju, sourceDSD, sourceHuangguoAI, sourceHuangguoVideo} {
		t.Run(source, func(t *testing.T) {
			e := routes149Engine(t)
			e.downloader.client.Transport = sourceFixtureTransport(func(req *http.Request) (*http.Response, error) {
				if req.Header.Get("Range") != "bytes=0-1023" {
					t.Fatal("unbounded probe")
				}
				return sourceFixtureResponse(req, 206, string(routes149Body())), nil
			})
			p, err := e.nativeOpenPlayback(context.Background(), nativePlaybackChoicesForSource(source, routes149Media(), 0))
			if err != nil || len(p.Routes) != 10 {
				t.Fatal(p, err)
			}
			defer e.nativeReleasePlayback(p.Session)
			for _, r := range p.Routes {
				if r.Status != "unknown" {
					t.Fatal("unprobed passed")
				}
			}
			status, err := e.nativeCheckPlaybackRoute(context.Background(), p.Session, 9)
			if err != nil || status.Status != "passed" || status.BytesRead != 1024 {
				t.Fatal(status, err)
			}
			selected, err := e.nativeSelectPlaybackRoute(context.Background(), p.Session, 9)
			if err != nil || selected.RouteIndex != 9 {
				t.Fatal(selected, err)
			}
			e.nativeReleasePlayback(selected.Session)
		})
	}
}

// TestRoutes163SingleAddressAndPlaylistStatus 验证「Routes 163 Single Address And Playlist Status」回归场景。
func TestRoutes163SingleAddressAndPlaylistStatus(t *testing.T) {
	for _, body := range []string{"\ufeff#EXTM3U\n#EXTINF:3,\none.ts\n", "<html>not media</html>", "", "FLV\x01\x01\x00\x00\x00\x09fixture"} {
		e := routes149Engine(t)
		e.downloader.client.Transport = sourceFixtureTransport(func(req *http.Request) (*http.Response, error) {
			return sourceFixtureResponse(req, 200, body), nil
		})
		p, err := e.nativeOpenPlayback(context.Background(), nativePlaybackChoicesForSource(sourceSorani, providerMedia{URL: "https://media.test/a.m3u8"}, 0))
		if err != nil || p.RouteCount != 1 || len(p.Routes) != 1 {
			t.Fatal(p, err)
		}
		s, err := e.nativeCheckPlaybackRoute(context.Background(), p.Session, 0)
		passed := strings.Contains(body, "#EXTM3U") || strings.HasPrefix(body, "FLV")
		if err != nil || (s.Status == "passed") != passed {
			t.Fatal(s, err)
		}
		if strings.Contains(body, "#EXTM3U") && !strings.Contains(s.Message, "播放列表") {
			t.Fatal("playlist mislabeled as verified segments")
		}
		e.nativeReleasePlayback(p.Session)
	}
}

// TestRoutes163RefusalStopsProbingAndManualSelection 验证「Routes 163 Refusal Stops Probing And Manual Selection」回归场景。
func TestRoutes163RefusalStopsProbingAndManualSelection(t *testing.T) {
	for _, code := range []int{401, 403, 429, 451, 503} {
		t.Run(fmt.Sprint(code), func(t *testing.T) {
			e := routes149Engine(t)
			var calls atomic.Int32
			e.downloader.client.Transport = sourceFixtureTransport(func(req *http.Request) (*http.Response, error) {
				calls.Add(1)
				res := sourceFixtureResponse(req, code, "")
				res.Header.Set("Retry-After", "90")
				return res, nil
			})
			p, err := e.nativeOpenPlayback(context.Background(), nativePlaybackChoicesForSource(sourceWuguo, routes149Media(), 0))
			if err != nil {
				t.Fatal(err)
			}
			defer e.nativeReleasePlayback(p.Session)
			s, err := e.nativeCheckPlaybackRoute(context.Background(), p.Session, 0)
			if err != nil || !s.Stop || s.Status != "failed" {
				t.Fatal(s, err)
			}
			if _, err = e.nativeSelectPlaybackRoute(context.Background(), p.Session, 1); !nativeRouteMustStop(err) {
				t.Fatal("manual bypass", err)
			}
			other, err := e.nativeCheckPlaybackRoute(context.Background(), p.Session, 1)
			if err != nil || !other.Stop || other.Status != "unknown" || calls.Load() != 1 {
				t.Fatal(other, err, calls.Load())
			}
		})
	}
}

// routes163Chapter 返回候选线路163分集。
func routes163Chapter(line, number int) Chapter {
	address := fmt.Sprintf("https://media.test/play/12-%d-%d.html", line, number)
	return duanjuChapter(sourceWuguo, "12", number, fmt.Sprintf("第%d集", number), address, address, "https://media.test/")
}

// TestRoutes163SameEpisodeOnlyAndLazyResolution 验证「Routes 163 Same Episode Only And Lazy Resolution」回归场景。
func TestRoutes163SameEpisodeOnlyAndLazyResolution(t *testing.T) {
	first := routes163Chapter(1, 1)
	second := routes163Chapter(2, 1)
	wrong := routes163Chapter(3, 2)
	foreign := routes163Chapter(4, 1)
	foreign.PageURL = "https://media.test/play/99-4-1.html"
	vip := routes163Chapter(5, 1)
	vip.VIP = true
	originalID := first.ID
	grouped := attachChapterRoutes([]Chapter{first}, []Chapter{first, second, second, wrong, foreign, vip})
	if len(grouped[0].Alternatives) != 1 || grouped[0].ID != originalID {
		t.Fatal(grouped)
	}
	e := routes149Engine(t)
	var pages, media int
	e.downloader.client.Transport = sourceFixtureTransport(func(req *http.Request) (*http.Response, error) {
		if strings.HasSuffix(req.URL.Path, ".html") {
			pages++
			return sourceFixtureResponse(req, 200, `<script>player_aaaa={"url":"https://media.test/main.mp4"}</script>`), nil
		}
		media++
		return sourceFixtureResponse(req, 206, string(routes149Body())), nil
	})
	m, err := e.downloader.resolvePlaybackMedia(context.Background(), Task{DramaID: "wuguo:12", Chapter: grouped[0], Index: 1})
	if err != nil || pages != 0 {
		t.Fatal("eager alternate fetch", err, pages)
	}
	p, err := e.nativeOpenPlayback(context.Background(), nativePlaybackChoicesForSource(sourceWuguo, m, 0))
	if err != nil || pages != 1 || p.RouteCount != 2 {
		t.Fatal(p, err, pages)
	}
	defer e.nativeReleasePlayback(p.Session)
	s, err := e.nativeCheckPlaybackRoute(context.Background(), p.Session, 1)
	if err != nil || s.Status != "passed" || pages != 2 || media != 1 {
		t.Fatal(s, err, pages, media)
	}
	next, err := e.nativeSelectPlaybackRoute(context.Background(), p.Session, 1)
	if err != nil || pages != 2 {
		t.Fatal("lost shared resolution cache", err, pages)
	}
	e.nativeReleasePlayback(next.Session)
	if next.Headers["Referer"] != second.PageURL {
		t.Fatal("alternate referer lost", next)
	}
	mutated := grouped[0]
	mutated.Alternatives = []Chapter{wrong}
	if _, err = e.downloader.resolvePlaybackMedia(context.Background(), Task{DramaID: "wuguo:12", Chapter: mutated, Index: 1}); err == nil {
		t.Fatal("cross episode accepted")
	}
}

// TestRoutes163SingleMediaCallersStayResolved 验证「Routes 163 Single Media Callers Stay Resolved」回归场景。
func TestRoutes163SingleMediaCallersStayResolved(t *testing.T) {
	e := routes149Engine(t)
	first, second := routes163Chapter(1, 1), routes163Chapter(2, 1)
	first.Alternatives = []Chapter{second}
	pages := 0
	e.downloader.client.Transport = sourceFixtureTransport(func(req *http.Request) (*http.Response, error) {
		pages++
		return sourceFixtureResponse(req, 200, `<script>player_aaaa={"url":"https://media.test/current.mp4"}</script>`), nil
	})
	m, err := e.downloader.resolveProviderMedia(context.Background(), Task{DramaID: "wuguo:12", Chapter: first, Index: 1})
	if err != nil || m.pending != nil || m.URL != "https://media.test/current.mp4" || pages != 1 {
		t.Fatal("single-media diagnostic received an unresolved page", m, err, pages)
	}
}

// TestRoutes163MaccmsRetainsScopedAlternates 验证「Routes 163 Maccms Retains Scoped Alternates」回归场景。
func TestRoutes163MaccmsRetainsScopedAlternates(t *testing.T) {
	e := routes149Engine(t)
	e.downloader.client.Transport = sourceFixtureTransport(func(req *http.Request) (*http.Response, error) {
		return sourceFixtureResponse(req, 200, `<h1>Fixture</h1><ul class="playlist"><a href="/play/12-1-1.html">第1集</a><a href="/play/12-1-2.html">第2集</a><a href="/play/12-2-1.html">第1集</a><a href="/play/12-2-2.html">第2集</a><a href="/play/99-3-1.html">第1集</a><a href="/play/12-4-1.html">全集</a></ul>`), nil
	})
	_, chapters, err := e.downloader.fetchMaccmsDetail(context.Background(), sourceWuguo, "12")
	if err != nil || len(chapters) != 2 {
		t.Fatal(chapters, err)
	}
	for _, c := range chapters {
		if len(c.Alternatives) != 1 || !sameEpisodeRoute(c, c.Alternatives[0]) {
			t.Fatal(c)
		}
	}
	prepared, _, err := prepareOrdinaryChapters(Drama{Source: sourceWuguo}, chapters)
	if err != nil || !reflect.DeepEqual(prepared, chapters) {
		t.Fatal("routes or episode numbering lost", err)
	}
}

// TestRoutes163DownloadUsesAllActualCandidates 验证「Routes 163 Download Uses All Actual Candidates」回归场景。
func TestRoutes163DownloadUsesAllActualCandidates(t *testing.T) {
	e := routes149Engine(t)
	e.downloader.cfg.Retries = 1
	e.downloads.resolve = func(context.Context, nativeDownloadJob) (providerMedia, error) { return routes149Media(), nil }
	var hosts []string
	e.downloader.client.Transport = sourceFixtureTransport(func(req *http.Request) (*http.Response, error) {
		hosts = append(hosts, req.URL.Hostname())
		if req.URL.Hostname() != "route5.test" {
			return sourceFixtureResponse(req, 404, ""), nil
		}
		return sourceFixtureResponse(req, 200, string(routes149Body())), nil
	})
	_, err := e.downloads.transfer(context.Background(), nativeDownloadJob{ID: "routes163", Drama: nativeDrama{ID: "yeguo:12", Source: sourceYeguo}, Index: 1, Chapter: Chapter{Source: sourceYeguo}, Folder: ".download-cache/routes163"})
	if err != nil || len(hosts) != 6 {
		t.Fatal(err, hosts)
	}
}
