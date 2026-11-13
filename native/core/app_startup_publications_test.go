package core

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

// TestNewerVerifiedRoutePrecedesPreviouslyPreferredRoute 验证「Newer Verified Route Precedes Previously Preferred Route」回归场景。
func TestNewerVerifiedRoutePrecedesPreviouslyPreferredRoute(t *testing.T) {
	d := sourceFixtureDownloader(t, nil)
	d.cfg.HuangguoAIURL = "https://old.example"
	d.providerHosts = map[string]string{sourceHuangguoAI: "https://old.example"}
	old := mirrorFixtureRecord("https://old.example", sourceHuangguoAI)
	newer := mirrorFixtureRecord("https://new.example", sourceHuangguoAI)
	newer.AddedOn = "2026-10-26"
	pending := mirrorFixtureRecord("https://pending.example", sourceHuangguoAI)
	pending.AddedOn, pending.Playback = "2026-10-27", false
	setMirrorFixtures(d, old, newer, pending)
	got := d.huangguoCandidates(sourceHuangguoAI, "https://old.example/detail/1/")
	if len(got) != 2 || got[0] != "https://new.example/detail/1/" {
		t.Fatalf("route order: %v", got)
	}
}

// TestStartupPublicationSupportsInlineArraysWithoutCommentedAddresses 验证「Startup Publication Supports Inline Arrays Without Commented Addresses」回归场景。
func TestStartupPublicationSupportsInlineArraysWithoutCommentedAddresses(t *testing.T) {
	body := "const subdomains=['alpha', /* 'disabled' */ 'beta']; const urls=['https://example.org/'];"
	got := parseHuangguoPublication(body)
	seen := map[string]bool{}
	for _, origin := range got {
		seen[origin] = true
	}
	if !seen["https://alpha.example.org"] || !seen["https://beta.example.org"] || seen["https://disabled.example.org"] {
		t.Fatal(got)
	}
}

// TestRuntimePublicationRefreshIsOncePerProcess 验证「Runtime Publication Refresh Is Once Per Process」回归场景。
func TestRuntimePublicationRefreshIsOncePerProcess(t *testing.T) {
	var calls atomic.Int32
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		fmt.Fprint(w, `<a href="https://ordinary.example.org">entry</a>`)
	}))
	defer s.Close()
	previousURLs := huangguoPublicationURLs
	huangguoPublicationURLs = []string{s.URL}
	defer func() { huangguoPublicationURLs = previousURLs }()
	previousBuild := buildAllSources
	buildAllSources = "true"
	defer func() { buildAllSources = previousBuild }()
	e, err := newNativeEngine(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer e.downloads.close()
	e.downloader.client = s.Client()
	nativeState.Lock()
	previous := nativeState.engine
	nativeState.engine = e
	nativeState.Unlock()
	defer func() { nativeState.Lock(); nativeState.engine = previous; nativeState.Unlock() }()
	raw, _ := json.Marshal(map[string]any{"action": "initialize", "directory": e.directory, "refreshPublications": true})
	var input nativeInput
	json.Unmarshal(raw, &input)
	nativeDispatch(input)
	end := time.Now().Add(time.Second)
	for calls.Load() == 0 && time.Now().Before(end) {
		time.Sleep(10 * time.Millisecond)
	}
	registry := e.downloader.huangguoMirrorStore()
	for {
		registry.mu.Lock()
		running := registry.refreshing
		registry.mu.Unlock()
		if !running {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	nativeDispatch(input)
	time.Sleep(20 * time.Millisecond)
	entries, _, _ := registry.snapshot()
	found := false
	for _, row := range entries {
		if row.Origin == "https://ordinary.example.org" {
			found = true
		}
	}
	data := map[string]any{"requests": calls.Load(), "newAddressRetained": found}
	body, _ := json.Marshal(data)
	fmt.Println("STARTUP_BEHAVIOR=" + string(body))
	if calls.Load() != 1 || !found {
		t.Fatal("startup sync missing or duplicated")
	}
}
