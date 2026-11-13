package core

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

// routes149Media 返回候选线路149媒体。
func routes149Media() providerMedia {
	media := providerMedia{URL: "https://route0.test/episode.mp4", Direct: true, Quality: 1080, Referer: "https://media.test/"}
	for i := 1; i < 10; i++ {
		media.Variants = append(media.Variants, providerMedia{URL: fmt.Sprintf("https://route%d.test/episode.mp4", i), Referer: media.Referer, Direct: true, Quality: 1080})
	}
	return media
}

// routes149Engine 返回候选线路149引擎。
func routes149Engine(t *testing.T) *nativeEngine {
	t.Helper()
	engine, err := newNativeEngine(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		engine.downloads.close()
		if engine.stream != nil {
			engine.stream.server.Close()
		}
	})
	return engine
}

// routes149Body 返回候选线路149正文。
func routes149Body() []byte { b := make([]byte, 2048); copy(b[4:], []byte("ftypisom")); return b }

// TestRoutes149AllCandidatesAndManualSelection 验证「Routes 149 All Candidates And Manual Selection」回归场景。
func TestRoutes149AllCandidatesAndManualSelection(t *testing.T) {
	engine := routes149Engine(t)
	plan, err := engine.nativeOpenPlayback(context.Background(), nativePlaybackChoicesForSource(sourceHongguo, routes149Media(), 0))
	if err != nil || plan.RouteCount != 10 || len(plan.Routes) != 10 {
		t.Fatal(plan, err)
	}
	for _, index := range []int{9, 1, 0, 8} {
		next, err := engine.nativeSelectPlaybackRoute(context.Background(), plan.Session, index)
		if err != nil || next.RouteIndex != index {
			t.Fatal(next, err)
		}
		engine.nativeReleasePlayback(plan.Session)
		plan = next
	}
	if _, err := engine.nativeSelectPlaybackRoute(context.Background(), plan.Session, 10); err == nil {
		t.Fatal("out-of-range route accepted")
	}
	engine.nativeReleasePlayback(plan.Session)
	if len(engine.playbacks) != 0 {
		t.Fatal("session leaked")
	}
}

// TestRoutes149ProbeLimitCacheAndStatus 验证「Routes 149 Probe Limit Cache And Status」回归场景。
func TestRoutes149ProbeLimitCacheAndStatus(t *testing.T) {
	engine := routes149Engine(t)
	var calls atomic.Int32
	engine.downloader.client.Transport = sourceFixtureTransport(func(req *http.Request) (*http.Response, error) {
		calls.Add(1)
		if req.Header.Get("Range") != "bytes=0-1023" || req.Header.Get("Referer") != "https://media.test/" {
			t.Error("probe lost range or referer")
		}
		res := sourceFixtureResponse(req, 206, string(routes149Body()))
		res.Header.Set("Content-Type", "video/mp4")
		return res, nil
	})
	plan, err := engine.nativeOpenPlayback(context.Background(), nativePlaybackChoicesForSource(sourceHongguo, routes149Media(), 0))
	if err != nil {
		t.Fatal(err)
	}
	for round := 0; round < 2; round++ {
		for i := 0; i < 10; i++ {
			status, err := engine.nativeCheckPlaybackRoute(context.Background(), plan.Session, i)
			if err != nil || status.Status != "passed" || status.BytesRead != 1024 {
				t.Fatal(status, err)
			}
		}
	}
	if calls.Load() != 10 {
		t.Fatalf("cached probes repeated: %d", calls.Load())
	}
	engine.nativeReleasePlayback(plan.Session)
	if _, err := engine.nativeCheckPlaybackRoute(context.Background(), plan.Session, 0); err == nil {
		t.Fatal("released probe accepted")
	}
}

// TestRoutes149ProbeRefusalAndEmptyBody 验证「Routes 149 Probe Refusal And Empty Body」回归场景。
func TestRoutes149ProbeRefusalAndEmptyBody(t *testing.T) {
	for _, code := range []int{200, 403, 429, 404} {
		t.Run(fmt.Sprint(code), func(t *testing.T) {
			engine := routes149Engine(t)
			var calls atomic.Int32
			engine.downloader.client.Transport = sourceFixtureTransport(func(req *http.Request) (*http.Response, error) {
				calls.Add(1)
				res := sourceFixtureResponse(req, code, "")
				if code == 429 {
					res.Header.Set("Retry-After", "60")
				}
				return res, nil
			})
			plan, err := engine.nativeOpenPlayback(context.Background(), nativePlaybackChoicesForSource(sourceHongguo, routes149Media(), 0))
			if err != nil {
				t.Fatal(err)
			}
			defer engine.nativeReleasePlayback(plan.Session)
			status, err := engine.nativeCheckPlaybackRoute(context.Background(), plan.Session, 0)
			if err != nil || status.Status != "failed" {
				t.Fatal(status, err)
			}
			if code == 403 || code == 429 {
				if !status.Stop {
					t.Fatal("access refusal did not stop")
				}
				next, err := engine.nativeCheckPlaybackRoute(context.Background(), plan.Session, 1)
				if err != nil || !next.Stop || next.Status != "unknown" || calls.Load() != 1 {
					t.Fatal("unprobed marked failed/passed or access retried", next, err, calls.Load())
				}
			}
		})
	}
}

// TestRoutes149ProbeReleaseCancelsWork 验证「Routes 149 Probe Release Cancels Work」回归场景。
func TestRoutes149ProbeReleaseCancelsWork(t *testing.T) {
	engine := routes149Engine(t)
	started := make(chan struct{})
	done := make(chan error, 1)
	engine.downloader.client.Transport = sourceFixtureTransport(func(req *http.Request) (*http.Response, error) {
		close(started)
		<-req.Context().Done()
		return nil, req.Context().Err()
	})
	plan, err := engine.nativeOpenPlayback(context.Background(), nativePlaybackChoicesForSource(sourceHongguo, routes149Media(), 0))
	if err != nil {
		t.Fatal(err)
	}
	go func() { _, err := engine.nativeCheckPlaybackRoute(context.Background(), plan.Session, 0); done <- err }()
	<-started
	engine.nativeReleasePlayback(plan.Session)
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("probe continued after page release")
	}
}

// TestRoutes149DownloadPrefersExactChoiceAndFallsBack 验证「Routes 149 Download Prefers Exact Choice And Falls Back」回归场景。
func TestRoutes149DownloadPrefersExactChoiceAndFallsBack(t *testing.T) {
	for _, manual := range []bool{false, true} {
		t.Run(fmt.Sprint(manual), func(t *testing.T) {
			engine := routes149Engine(t)
			manager := engine.downloads
			engine.downloader.cfg.Retries = 1
			media := routes149Media()
			manager.resolve = func(context.Context, nativeDownloadJob) (providerMedia, error) { return media, nil }
			var hosts []string
			engine.downloader.client.Transport = sourceFixtureTransport(func(req *http.Request) (*http.Response, error) {
				hosts = append(hosts, req.URL.Hostname())
				if req.URL.Hostname() != "route3.test" {
					return sourceFixtureResponse(req, 404, ""), nil
				}
				body := routes149Body()
				res := sourceFixtureResponse(req, 200, string(body))
				res.ContentLength = int64(len(body))
				res.Header.Set("Content-Length", fmt.Sprint(len(body)))
				res.Header.Set("Content-Type", "video/mp4")
				return res, nil
			})
			job := nativeDownloadJob{ID: "fixture149", Drama: nativeDrama{ID: "hongguo:1234", Source: sourceHongguo, Title: "Fixture"}, Index: 1, Chapter: Chapter{Source: sourceHongguo}, Folder: ".download-cache/fixture149"}
			if manual {
				job.PreferredRoute = 9
				job.PreferredRouteKey = nativeRouteKey(media.Variants[2])
			}
			keep := filepath.Join(manager.root, "existing.mp4")
			if err := os.WriteFile(keep, []byte("KEEP"), 0600); err != nil {
				t.Fatal(err)
			}
			result, err := manager.transfer(context.Background(), job)
			if err != nil {
				t.Fatal(err)
			}
			file := filepath.Join(manager.jobDirectory(job), result.file)
			got, err := os.ReadFile(file)
			if err != nil || !bytes.Equal(got, routes149Body()) {
				t.Fatal("wrong download", err)
			}
			preserved, _ := os.ReadFile(keep)
			if string(preserved) != "KEEP" {
				t.Fatal("existing file changed")
			}
			if manual && (len(hosts) != 1 || hosts[0] != "route3.test") {
				t.Fatal("exact selection not prioritized", hosts)
			}
			if !manual && len(hosts) != 4 {
				t.Fatal("did not reach fourth candidate", hosts)
			}
		})
	}
}

// TestRoutes149DownloadStopsOnForbidden 验证「Routes 149 Download Stops On Forbidden」回归场景。
func TestRoutes149DownloadStopsOnForbidden(t *testing.T) {
	engine := routes149Engine(t)
	engine.downloader.cfg.Retries = 1
	var calls atomic.Int32
	engine.downloads.resolve = func(context.Context, nativeDownloadJob) (providerMedia, error) { return routes149Media(), nil }
	engine.downloader.client.Transport = sourceFixtureTransport(func(req *http.Request) (*http.Response, error) {
		calls.Add(1)
		return sourceFixtureResponse(req, 403, ""), nil
	})
	_, err := engine.downloads.transfer(context.Background(), nativeDownloadJob{ID: "refusal149", Drama: nativeDrama{ID: "hongguo:1234", Source: sourceHongguo}, Folder: ".download-cache/refusal149"})
	if err == nil || calls.Load() != 1 {
		t.Fatal("authorization refusal rotated routes", err, calls.Load())
	}
}

// TestRoutes149DownloadEnqueuePreservesCompletedJobs 验证「Routes 149 Download Enqueue Preserves Completed Jobs」回归场景。
func TestRoutes149DownloadEnqueuePreservesCompletedJobs(t *testing.T) {
	engine := routes149Engine(t)
	manager := engine.downloads
	manager.sourceConcurrency = 0
	choice := nativePlaybackChoicesForSource(sourceHongguo, routes149Media(), 0)
	choice.dramaID = "hongguo:1234"
	choice.episodeIndex = 1
	plan, err := engine.nativeOpenPlayback(context.Background(), choice)
	if err != nil {
		t.Fatal(err)
	}
	defer engine.nativeReleasePlayback(plan.Session)
	input := nativeInput{Drama: nativeDrama{ID: choice.dramaID, Source: sourceHongguo}, Entries: []nativeDownloadEpisode{{Index: 1, Chapter: Chapter{Source: sourceHongguo}}}, Session: plan.Session, PreferredRoute: 4}
	manager.closed = true
	if _, err := manager.enqueue(input); err == nil {
		t.Fatal("closed queue accepted work")
	}
	manager.closed = false

	id := nativeDownloadID(choice.dramaID, 1)
	record := &nativeDownloadRecord{nativeDownloadJob: nativeDownloadJob{ID: id, Drama: input.Drama, Chapter: input.Entries[0].Chapter, Index: 1, State: "completed", Bytes: 4, Folder: "completed-fixture"}, File: "media.mp4"}
	manager.jobs[id] = record
	directory := manager.jobDirectory(record.nativeDownloadJob)
	if err := os.MkdirAll(directory, 0700); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(directory, "media.mp4")
	if err := os.WriteFile(file, []byte("KEEP"), 0600); err != nil {
		t.Fatal(err)
	}
	before := *record
	added, err := manager.enqueue(input)
	if err != nil || added != 0 {
		t.Fatal(added, err)
	}
	got, _ := os.ReadFile(file)
	if string(got) != "KEEP" || record.State != before.State || record.PreferredRoute != before.PreferredRoute || record.PreferredRouteKey != "" {
		t.Fatal("completed download modified")
	}

}
