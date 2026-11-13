package core

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

// TestNativeDownloadQueueDelayBounds 验证「Native Download Queue Delay Bounds」回归场景。
func TestNativeDownloadQueueDelayBounds(t *testing.T) {
	seen := map[time.Duration]bool{}
	for range 1000 {
		delay := nativeDownloadQueueDelay()
		if delay < 100*time.Millisecond || delay > 500*time.Millisecond {
			t.Fatalf("delay out of range: %v", delay)
		}
		seen[delay] = true
	}
	if len(seen) < 2 {
		t.Fatal("delay did not vary")
	}
}

// TestNativeDownloadQueueSerialSourceSpacingAndIndependentSites 验证「Native Download Queue Serial Source Spacing And Independent Sites」回归场景。
func TestNativeDownloadQueueSerialSourceSpacingAndIndependentSites(t *testing.T) {
	if !nativeSourceAvailable(sourceHuangdou) {
		t.Skip("requires buildAllSources=true linker flag")
	}
	manager := downloadTestManager(t)
	manager.concurrency, manager.sourceConcurrency = 3, 1
	manager.queueDelay = func() time.Duration { return 120 * time.Millisecond }
	type event struct {
		source string
		index  int
		at     time.Time
	}
	started := make(chan event, 8)
	releases := []chan struct{}{make(chan struct{}), make(chan struct{}), make(chan struct{})}
	manager.resolve = func(ctx context.Context, job nativeDownloadJob) (providerMedia, error) {
		source := sourceFromDramaID(job.Drama.ID)
		started <- event{source, job.Index, time.Now()}
		if source == "hongguo" {
			select {
			case <-releases[job.Index-1]:
			case <-ctx.Done():
			}
		}
		return providerMedia{}, errors.New("queue fixture finished without remote media")
	}
	input := downloadTestInput(3)
	if _, err := manager.enqueue(input); err != nil {
		t.Fatal(err)
	}
	other := downloadTestInput(1)
	other.Drama.ID, other.Drama.Source = "huangdou:queue-other", sourceHuangdou
	if _, err := manager.enqueue(other); err != nil {
		t.Fatal(err)
	}
	first := map[string]event{}
	for range 2 {
		select {
		case e := <-started:
			first[e.source] = e
		case <-time.After(2 * time.Second):
			t.Fatal("different site was blocked by first source")
		}
	}
	if first["hongguo"].index != 1 || first[sourceHuangdou].index != 1 {
		t.Fatal(first)
	}
	select {
	case e := <-started:
		t.Fatalf("source exceeded one active episode: %+v", e)
	case <-time.After(40 * time.Millisecond):
	}
	for index := 0; index < 2; index++ {
		finished := time.Now()
		close(releases[index])
		select {
		case e := <-started:
			if e.source != "hongguo" || e.index != index+2 || e.at.Sub(finished) < 110*time.Millisecond {
				t.Fatalf("wrong source order or no completion gap: %+v gap=%v", e, e.at.Sub(finished))
			}
		case <-time.After(2 * time.Second):
			t.Fatal("queue timer did not wake next episode")
		}
	}
	close(releases[2])
	waitDownloads(t, manager, func(jobs []nativeDownloadJob) bool {
		for _, job := range jobs {
			if job.State != "failed" {
				return false
			}
		}
		return len(jobs) == 4
	})
}

// TestNativeDownloadQueueTenLimitAndLoweringDrainsWithoutCancellation 验证「Native Download Queue Ten Limit And Lowering Drains Without Cancellation」回归场景。
func TestNativeDownloadQueueTenLimitAndLoweringDrainsWithoutCancellation(t *testing.T) {
	manager := downloadTestManager(t)
	manager.concurrency, manager.sourceConcurrency = 10, 10
	manager.queueDelay = func() time.Duration { return 0 }
	started := make(chan int, 20)
	release := make(chan struct{})
	var active, peak, cancelled atomic.Int32
	manager.resolve = func(ctx context.Context, job nativeDownloadJob) (providerMedia, error) {
		count := active.Add(1)
		for previous := peak.Load(); count > previous && !peak.CompareAndSwap(previous, count); previous = peak.Load() {
		}
		started <- job.Index
		select {
		case <-release:
		case <-ctx.Done():
			cancelled.Add(1)
		}
		active.Add(-1)
		return providerMedia{}, errors.New("queue fixture complete")
	}
	if _, err := manager.enqueue(downloadTestInput(12)); err != nil {
		t.Fatal(err)
	}
	for range 10 {
		select {
		case <-started:
		case <-time.After(2 * time.Second):
			t.Fatal("ten allowed episodes did not start")
		}
	}
	select {
	case n := <-started:
		t.Fatalf("exceeded ten: %d", n)
	case <-time.After(30 * time.Millisecond):
	}
	settings := defaultResourceSettings()
	settings.DownloadConcurrency, settings.DownloadSourceConcurrency = 10, 1
	if _, err := manager.engine.saveResourceSettings(settings); err != nil {
		t.Fatal(err)
	}
	if active.Load() != 10 || cancelled.Load() != 0 {
		t.Fatal("lowering limit cancelled in-flight work")
	}
	close(release)
	waitDownloads(t, manager, func(jobs []nativeDownloadJob) bool {
		for _, job := range jobs {
			if job.State != "failed" {
				return false
			}
		}
		return len(jobs) == 12
	})
	if peak.Load() != 10 || cancelled.Load() != 0 {
		t.Fatalf("peak=%d cancelled=%d", peak.Load(), cancelled.Load())
	}
}

// TestNativeDownloadQueuePauseAndCloseStopPendingWake 验证「Native Download Queue Pause And Close Stop Pending Wake」回归场景。
func TestNativeDownloadQueuePauseAndCloseStopPendingWake(t *testing.T) {
	for _, command := range []string{"pauseAll", "close"} {
		t.Run(command, func(t *testing.T) {
			manager := downloadTestManager(t)
			var starts atomic.Int32
			manager.resolve = func(ctx context.Context, job nativeDownloadJob) (providerMedia, error) {
				starts.Add(1)
				return providerMedia{}, errors.New("unexpected execution")
			}
			manager.sourceStarts = map[string]time.Time{"hongguo": time.Now().Add(150 * time.Millisecond)}
			if _, err := manager.enqueue(downloadTestInput(2)); err != nil {
				t.Fatal(err)
			}
			if command == "close" {
				manager.close()
			} else if err := manager.control("", command); err != nil {
				t.Fatal(err)
			}
			time.Sleep(220 * time.Millisecond)
			if starts.Load() != 0 {
				t.Fatal("paused or closed queue started a future worker")
			}
		})
	}
}

// TestNativeDownloadQueueDefersHongguoEmptyPrimary 验证「Native Download Queue Defers Hongguo Empty Primary」回归场景。
func TestNativeDownloadQueueDefersHongguoEmptyPrimary(t *testing.T) {
	manager := downloadTestManager(t)
	manager.sourceConcurrency = 1
	manager.queueDelay = func() time.Duration { return 0 }
	var requests atomic.Int32
	manager.resolve = func(context.Context, nativeDownloadJob) (providerMedia, error) {
		requests.Add(1)
		return providerMedia{}, errHongguoEmptyPrimaryPlayback
	}
	if _, err := manager.enqueue(downloadTestInput(2)); err != nil {
		t.Fatal(err)
	}
	jobs := waitDownloads(t, manager, func(jobs []nativeDownloadJob) bool {
		states := map[int]string{}
		for _, job := range jobs {
			states[job.Index] = job.State
		}
		return states[1] == "failed" && states[2] == "queued"
	})
	manager.mu.Lock()
	wait := time.Until(manager.sourceReadyLocked(sourceHongguo))
	other := manager.sourceReadyLocked(sourceHuangdou)
	manager.mu.Unlock()
	if requests.Load() != 1 || wait < 55*time.Second || !other.IsZero() || len(jobs) != 2 {
		t.Fatalf("failed redfruit request did not defer only its queue: requests=%d wait=%v other=%v", requests.Load(), wait, other)
	}
}

// TestNativeDownloadQueueHonorsMediaRetryAfter 验证「Native Download Queue Honors Media Retry After」回归场景。
func TestNativeDownloadQueueHonorsMediaRetryAfter(t *testing.T) {
	manager := downloadTestManager(t)
	manager.sourceConcurrency = 1
	var requests atomic.Int32
	events := make(chan time.Time, 2)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if requests.Add(1) == 1 {
			events <- time.Now()
			w.Header().Set("Retry-After", "1")
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		events <- time.Now()
		w.Header().Set("Content-Type", "video/mp4")
		_, _ = w.Write([]byte("queue fixture bytes, not decoder validation"))
	}))
	defer server.Close()
	manager.resolve = func(ctx context.Context, job nativeDownloadJob) (providerMedia, error) {
		return providerMedia{URL: server.URL + "/episode.mp4", Direct: true}, nil
	}
	if _, err := manager.enqueue(downloadTestInput(2)); err != nil {
		t.Fatal(err)
	}
	jobs := waitDownloads(t, manager, func(jobs []nativeDownloadJob) bool {
		if len(jobs) != 2 {
			return false
		}
		states := map[int]string{}
		for _, job := range jobs {
			states[job.Index] = job.State
		}
		return states[1] == "failed" && states[2] == "completed"
	})
	first, second := <-events, <-events
	if requests.Load() != 2 || second.Sub(first) < time.Second || jobs[0].Error == "" {
		t.Fatalf("retry-after ignored: calls=%d gap=%v", requests.Load(), second.Sub(first))
	}
}

// TestNativeDownloadQueueBackoffDeadlineAndNoBypass 验证「Native Download Queue Backoff Deadline And No Bypass」回归场景。
func TestNativeDownloadQueueBackoffDeadlineAndNoBypass(t *testing.T) {
	manager := downloadTestManager(t)
	ctx := context.WithValue(context.Background(), nativeDownloadSourceKey{}, "hongguo")
	until := time.Now().Add(40 * time.Minute).Truncate(time.Second)
	response := &http.Response{StatusCode: 503, Header: http.Header{"Retry-After": {until.UTC().Format(http.TimeFormat)}}}
	backoff := manager.observeDownloadBackoff(ctx, response)
	if backoff == nil || !backoff.until.Equal(until) {
		t.Fatal("server deadline shortened")
	}
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1) }))
	defer server.Close()
	_, _, err := manager.downloadFileAttempt(ctx, server.URL+"/never.mp4", "", filepath.Join(t.TempDir(), "never.mp4"), false, 0, nativeDownloadFileState{}, func(int64, int64) {})
	if err == nil || calls.Load() != 0 {
		t.Fatal("cooldown sent another request")
	}
	success := &http.Response{StatusCode: 200, Header: http.Header{"Retry-After": {"10"}}}
	if manager.observeDownloadBackoff(context.Background(), success) != nil {
		t.Fatal("successful media incorrectly rejected")
	}
}

// TestNativeDownloadQueuePlaylistBackoffStopsAlternateRequests 验证「Native Download Queue Playlist Backoff Stops Alternate Requests」回归场景。
func TestNativeDownloadQueuePlaylistBackoffStopsAlternateRequests(t *testing.T) {
	manager := downloadTestManager(t)
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Header().Set("Retry-After", "2")
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer server.Close()
	ctx := context.WithValue(context.Background(), nativeDownloadSourceKey{}, "hongguo")
	ctx = context.WithValue(ctx, nativeDownloadManagerKey{}, manager)
	for range 2 {
		_, _, err := manager.engine.downloader.fetchMediaPlaylist(ctx, server.URL+"/playlist.m3u8", "")
		var backoff *requestBackoff
		if !errors.As(err, &backoff) || backoff.status != 503 {
			t.Fatalf("playlist lost retry-after: %v", err)
		}
	}
	if calls.Load() != 1 {
		t.Fatalf("another request bypassed playlist cooldown: %d", calls.Load())
	}
}

// TestNativeDownloadQueueSettingsMigrationAndBounds 验证「Native Download Queue Settings Migration And Bounds」回归场景。
func TestNativeDownloadQueueSettingsMigrationAndBounds(t *testing.T) {
	manager := downloadTestManager(t)
	old := []byte(`{"proxyMode":"direct","proxyUrl":"","catalogConcurrency":4,"catalogIntervalMs":350,"downloadConcurrency":6,"downloadBySource":true}`)
	file := filepath.Join(manager.engine.directory, "resource-settings.json")
	if err := os.WriteFile(file, old, 0600); err != nil {
		t.Fatal(err)
	}
	manager.engine.loadResourceSettings()
	settings := manager.engine.resourceSettings()
	if settings.DownloadSourceConcurrency != 1 || settings.DownloadConcurrency != 6 || settings.CatalogConcurrency != 4 || !settings.DownloadBySource || settings.Warning != "" {
		t.Fatal(settings)
	}
	unchanged, _ := os.ReadFile(file)
	if string(unchanged) != string(old) {
		t.Fatal("migration rewrote existing user settings")
	}
	for _, value := range []int{1, 10} {
		settings.DownloadConcurrency, settings.DownloadSourceConcurrency = value, value
		if settings.validate() != nil {
			t.Fatalf("valid download bound rejected: %d", value)
		}
	}
	for _, value := range []int{0, 11} {
		settings = defaultResourceSettings()
		settings.DownloadConcurrency = value
		if settings.validate() == nil {
			t.Fatalf("invalid total accepted: %d", value)
		}
		settings = defaultResourceSettings()
		settings.DownloadSourceConcurrency = value
		if settings.validate() == nil {
			t.Fatalf("invalid source bound accepted: %d", value)
		}
	}
	var explicit nativeResourceSettings
	if err := json.Unmarshal([]byte(`{"proxyMode":"auto","catalogConcurrency":3,"catalogIntervalMs":250,"downloadConcurrency":2,"downloadSourceConcurrency":0}`), &explicit); err != nil || explicit.validate() == nil {
		t.Fatal("explicit zero accepted")
	}
}
