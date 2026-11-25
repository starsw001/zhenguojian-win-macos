package core

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestHongguoDownloadUsesOnlyLiveExactEpisodeSelection(t *testing.T) {
	e := routes149Engine(t)
	ctx := context.Background()
	manager := e.downloads
	manager.sourceStarts = map[string]time.Time{sourceHongguo: time.Now().Add(time.Hour)}
	issue := time.Now().Add(-time.Minute)
	media := func(host string) providerMedia {
		u := "https://" + host + ".qznovelvod.com/ep57?expire=" + fmt.Sprint(time.Now().Add(time.Hour).Unix())
		return providerMedia{URL: u, Direct: true, Quality: 720, Referer: hongguoBaseURL + "/player/1234/5678",
			credentials: &providerMediaCredentials{origin: "https://" + host + ".qznovelvod.com", expires: time.Now().Add(time.Hour)}}
	}
	opts := []providerMedia{media("first"), media("selected")}
	ch57 := Chapter{Source: sourceHongguo, VideoURL: "hongguo-cenc://5678"}
	ch58 := Chapter{Source: sourceHongguo, VideoURL: "hongguo-cenc://5679"}
	choice := nativePlaybackChoicesForSource(sourceHongguo, mergeHongguoMediaRoutes(opts), 0)
	choice.hongguoResolvedAt, choice.hongguoGeneration = issue, e.downloader.hongguoMediaCache.epoch()
	choice.dramaID, choice.episodeIndex, choice.danmakuSeries, choice.danmakuVideo = "hongguo:1234", 57, "1234", "5678"
	plan, err := e.nativeOpenPlayback(ctx, choice)
	if err != nil {
		t.Fatal(err)
	}
	defer e.nativeReleasePlayback(plan.Session)
	drama := nativeDrama{ID: choice.dramaID, Source: sourceHongguo, Title: "Fixture"}
	input := nativeInput{Drama: drama, Entries: []nativeDownloadEpisode{{Index: 57, Chapter: ch57}, {Index: 58, Chapter: ch58}}, Session: plan.Session, PreferredRoute: 2}
	if n, err := manager.enqueue(input); err != nil || n != 2 {
		t.Fatal("enqueue", n, err)
	}
	job := manager.jobs[nativeDownloadID(drama.ID, 57)].nativeDownloadJob
	other := manager.jobs[nativeDownloadID(drama.ID, 58)].nativeDownloadJob
	if job.hongguoPlaybackSession != plan.Session || job.PreferredRouteKey != nativeRouteKey(opts[1]) || other.hongguoPlaybackSession != "" || other.PreferredRoute != 0 || other.PreferredRouteKey != "" {
		t.Fatal("episode handoff identity mismatch")
	}
	disk, err := os.ReadFile(filepath.Join(manager.root, "index.json"))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(disk, []byte(plan.Session)) || bytes.Contains(disk, []byte("qznovelvod")) || bytes.Contains(disk, []byte("?expire=")) {
		t.Fatal("transient media or session leaked into the download index")
	}
	var restored []nativeDownloadRecord
	if json.Unmarshal(disk, &restored) != nil || len(restored) != 2 || restored[0].hongguoPlaybackSession != "" {
		t.Fatal("unreliable queue persistence")
	}
	data := routes149Body()
	var modelReads, mediaReads atomic.Int32
	e.downloader.client.Transport = sourceFixtureTransport(func(r *http.Request) (*http.Response, error) {
		if r.URL.Hostname() != "selected.qznovelvod.com" {
			modelReads.Add(1)
			return nil, errors.New("unwanted metadata/other media request")
		}
		mediaReads.Add(1)
		res := sourceFixtureResponse(r, 200, string(data))
		res.Header.Set("Content-Type", "video/mp4")
		res.ContentLength = int64(len(data))
		return res, nil
	})
	got, err := manager.transfer(ctx, job)
	if err != nil {
		t.Fatal(err)
	}
	file, err := os.ReadFile(filepath.Join(manager.jobDirectory(job), got.file))
	if err != nil || !bytes.Equal(file, data) || mediaReads.Load() == 0 || modelReads.Load() != 0 {
		t.Fatal("selected transfer mismatch", err)
	}
	t.Logf("episode=57 selected_address=2 synthetic_download_bytes=%d extra_metadata_requests=%d", len(file), modelReads.Load())
	copyJob := job
	copyJob.Chapter = ch58
	if _, ok, _ := e.hongguoDownloadFromPlayback(ctx, copyJob); ok {
		t.Fatal("same index but changed video accepted")
	}
	e.downloader.hongguoMediaCache.clear()
	if _, ok, _ := e.hongguoDownloadFromPlayback(ctx, job); ok {
		t.Fatal("network change did not retire the session handoff")
	}
	e.mu.Lock()
	live := e.playbacks[plan.Session]
	e.mu.Unlock()
	live.checks.mu.Lock()
	live.checks.blocked = true
	live.checks.mu.Unlock()
	if _, _, err := e.hongguoDownloadFromPlayback(ctx, job); !errors.Is(err, errNativeMediaAccessStop) {
		t.Fatal("access barrier ignored", err)
	}
}

func TestHongguoDownloadLeaseLimits(t *testing.T) {
	now := time.Now()
	base := providerMedia{URL: "https://public.qznovelvod.com/a"}
	check := func(m providerMedia, age time.Duration, want bool) {
		t.Helper()
		if got := hongguoReusableDownloadAddress(m, now.Add(-age), now); got != want {
			t.Fatal("unexpected eligibility", age, want)
		}
	}
	check(base, time.Second, true)
	check(base, 16*time.Second, false)
	m := base
	m.URL += "?expire=" + fmt.Sprint(now.Add(time.Hour).Unix())
	check(m, time.Minute, true)
	check(m, 11*time.Minute, false)
	m.URL += "&expire=" + fmt.Sprint(now.Add(-time.Second).Unix())
	check(m, time.Second, false)
	for _, q := range []string{"expire=bad", "expire=", "expire=%GG", "expire=1000000000"} {
		m := base
		m.URL += "?" + q
		check(m, time.Second, false)
	}
	m = base
	m.HLSKey = []byte{1}
	check(m, time.Second, false)
	m = base
	m.URL = "https://unapproved.invalid/ep57"
	check(m, time.Second, false)
	m = base
	u, _ := url.Parse(m.URL)
	m.credentials = &providerMediaCredentials{origin: providerMediaOrigin(u), cookie: "private"}
	check(m, time.Second, false)
	m.credentials = &providerMediaCredentials{origin: "https://wrong.invalid", expires: now.Add(time.Hour)}
	check(m, time.Second, false)
}

func TestHongguoGroupedBackupBoundaries(t *testing.T) {
	const first = "https://main.qznovelvod.com/57"
	const last = "https://last.qznovelvod.com/57?s=a%2Fb"
	var deep any = "https://deep.qznovelvod.com/57"
	for i := 0; i < 30; i++ {
		deep = []any{deep}
	}
	m := map[string]any{"main_url": first, "backup_url_list": []any{[]any{first, "http://127.0.0.1/cache", "https://bad.invalid/57"}, deep, map[string]any{"other_episode": last}, []any{last}}}
	got := hongguoMediaAddresses(m)
	if len(got) != 2 || got[0] != first || got[1] != last {
		t.Fatal("invalid depth, foreign host, duplicate or other episode", got)
	}
}

func TestHongguoSharedLookupCancellationAndGeneration(t *testing.T) {
	var c hongguoMediaCache
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	start, release := make(chan struct{}), make(chan struct{})
	var n atomic.Int32
	load := func(ctx context.Context) (providerMedia, error) {
		if n.Add(1) == 1 {
			close(start)
		}
		select {
		case <-ctx.Done():
			return providerMedia{}, ctx.Err()
		case <-release:
			return providerMedia{URL: "https://ok.qznovelvod.com/1"}, nil
		}
	}
	first := make(chan error, 1)
	go func() { _, e := c.resolve(ctx, "a", false, load); first <- e }()
	<-start
	waitCtx, stop := context.WithCancel(context.Background())
	stop()
	if _, e := c.resolve(waitCtx, "a", false, load); !errors.Is(e, context.Canceled) {
		t.Fatal("canceled consumer started work")
	}
	c.clear()
	if err := <-first; !errors.Is(err, context.Canceled) {
		t.Fatal("old in-flight response survived reset")
	}
	close(release)
	if m, err := c.resolve(context.Background(), "a", false, load); err != nil || !strings.HasPrefix(m.URL, "https://ok.") || n.Load() != 2 {
		t.Fatal("fresh generation did not resolve", err)
	}
}
