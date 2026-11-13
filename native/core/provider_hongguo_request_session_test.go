package core

import (
	"bytes"
	"context"
	"crypto/md5"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestHongguoRequestContract147 验证「Hongguo Request Contract 147」回归场景。
func TestHongguoRequestContract147(t *testing.T) {
	checks := []bool{}
	registrations := 0
	const content = "synthetic-unencrypted-media-fixture"
	engine := sourceFixtureEngine(t, func(req *http.Request) (*http.Response, error) {
		switch req.URL.Hostname() {
		case "log5-applog-lf.fqnovel.com":
			registrations++
			res := sourceFixtureResponse(req, 200, `{"device_id_str":"123456789","install_id_str":"987654321"}`)
			for _, name := range []string{"install_id", "ttreq", "store-region", "store-region-src"} {
				res.Header.Add("Set-Cookie", name+"=fixture; Domain=.fqnovel.com; Path=/; Secure; Max-Age=3600")
			}
			return res, nil
		case "api5-normal-sinfonlineb.fqnovel.com":
			body, _ := io.ReadAll(req.Body)
			q := req.URL.Query()
			var v map[string]any
			json.Unmarshal(body, &v)
			cookies := map[string]bool{}
			for _, c := range req.Cookies() {
				cookies[c.Name] = true
			}
			ok := q.Get("device_id") == "123456789" && q.Get("iid") == "987654321" && q.Get("version_code") == "73532" && strings.Contains(req.UserAgent(), "read/73532") && bytes.Contains(body, []byte(`"device_level":1`))
			ok = ok && req.Header.Get("Accept") == "application/json" && req.Header.Get("Content-Type") == "application/json; charset=utf-8" && req.Header.Get("X-XS-From-Web") == "0" && req.Header.Get("Sdk-Version") == "2"
			for _, name := range []string{"install_id", "ttreq", "store-region", "store-region-src"} {
				ok = ok && cookies[name]
			}
			for _, name := range []string{"X-Gorgon", "X-Argus", "X-Ladon", "X-Khronos", "X-SS-Req-Ticket"} {
				ok = ok && req.Header.Get(name) != ""
			}
			ok = ok && req.Header.Get("X-SS-STUB") == fmt.Sprintf("%X", md5.Sum(body))
			checks = append(checks, ok)
			if !ok {
				return sourceFixtureResponse(req, 200, ""), nil
			}
			return sourceFixtureResponse(req, 200, `{"code":0,"data":{"5678":{"video_model":{"video_list":{"video_1":{"main_url":"https://fixture.qznovelvod.com/5678.mp4","vwidth":1080,"vheight":1920,"codec_type":"h264"}}}}}}`), nil
		case "fixture.qznovelvod.com":
			if req.Header.Get("Cookie") != "" || req.Header.Get("X-Argus") != "" {
				t.Fatal("metadata credentials leaked to media")
			}
			res := sourceFixtureResponse(req, 200, content)
			res.Header.Set("Content-Type", "video/mp4")
			return res, nil
		default:
			t.Fatalf("unexpected route %s", req.URL.Hostname())
			return nil, nil
		}
	})
	ctx := context.Background()
	out := map[string]any{"playbackBytes": false, "downloadBytes": false, "persistedSession": false}
	media, err := engine.downloader.resolveHongguoMultiMedia(ctx, "5678")
	if err == nil {
		plan, e := engine.nativeOpenPlayback(ctx, nativePlaybackChoicesForSource(sourceHongguo, media, 1080))
		if e != nil {
			t.Fatal(e)
		}
		defer engine.stream.server.Close()
		c := &http.Client{Transport: &http.Transport{}}
		res, e := c.Get(plan.URL)
		if e != nil {
			t.Fatal(e)
		}
		body, _ := io.ReadAll(res.Body)
		res.Body.Close()
		out["playbackBytes"] = string(body) == content
		engine.downloads.resolve = func(context.Context, nativeDownloadJob) (providerMedia, error) { return media, nil }
		job := nativeDownloadJob{ID: "request147", Drama: nativeDrama{ID: "hongguo:1234"}, Index: 1, Quality: 1080}
		_, e = engine.downloads.transfer(ctx, job)
		if e != nil {
			t.Fatal(e)
		}
		body, e = os.ReadFile(filepath.Join(engine.downloads.jobDirectory(job), "media.mp4"))
		out["downloadBytes"] = e == nil && string(body) == content
	}
	reopened := sourceFixtureDownloader(t, engine.downloader.client.Transport.(sourceFixtureTransport))
	reopened.cfg.dataDir = engine.downloader.cfg.dataDir
	_, err2 := reopened.resolveHongguoMultiMedia(ctx, "5678")
	out["persistedSession"] = err2 == nil && registrations == 1
	out["contractAccepted"] = checks
	out["registrationCalls"] = registrations
	out["resolved"] = err == nil
	b, _ := json.Marshal(out)
	fmt.Printf("HG147_FIXTURE=%s\n", b)
	expected := os.Getenv("HG147_EXPECT")
	if expected == "" {
		expected = "modified"
	}
	if expected == "original" {
		if err == nil || registrations != 1 {
			t.Fatal("unexpected original behavior")
		}
	} else if expected == "modified" {
		if err != nil || err2 != nil || out["playbackBytes"] != true || out["downloadBytes"] != true || out["persistedSession"] != true {
			t.Fatalf("contract failed %s", b)
		}
	}
}

// TestHongguoPlaybackCookieScopeExpiryAndDeletion 验证「Hongguo Playback Cookie Scope Expiry And Deletion」回归场景。
func TestHongguoPlaybackCookieScopeExpiryAndDeletion(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	origin, _ := http.NewRequest("POST", hongguoPlaybackRegisterURL, nil)
	response := sourceFixtureResponse(origin, 200, `{}`)
	response.Header.Add("Set-Cookie", "ttreq=fixture; Domain=.fqnovel.com; Path=/; Secure; Max-Age=60")
	response.Header.Add("Set-Cookie", "store-region=cn; Path=/; Secure")
	response.Header.Add("Set-Cookie", "unrelated=secret; Domain=.fqnovel.com; Path=/; Secure")
	response.Header.Add("Set-Cookie", "install_id=wrong; Domain=.com; Path=/; Secure")
	response.Header.Add("Set-Cookie", "store-region-src=wrong; Domain=unrelated.example; Path=/; Secure")
	cookies := hongguoMergePlaybackCookies(nil, origin, response, now)
	if len(cookies) != 2 {
		t.Fatalf("unexpected accepted cookie count: %d", len(cookies))
	}
	device := hongguoPlaybackDevice{Version: 1, DeviceID: "123", InstallID: "456", Cookies: cookies}
	if !device.valid() {
		t.Fatal("valid scoped cookies rejected")
	}
	for _, address := range []string{hongguoMultiVideoAPI, "https://media.qznovelvod.com/test", "https://unrelated.fqnovel.com/test", "http://api5-normal-sinfonlineb.fqnovel.com/test"} {
		req, _ := http.NewRequest("POST", address, nil)
		hongguoApplyPlaybackCookies(req, device, now)
		if address == hongguoMultiVideoAPI {
			if len(req.Cookies()) != 1 || req.Cookies()[0].Name != "ttreq" {
				t.Fatal("domain scope not respected")
			}
		} else if len(req.Cookies()) != 0 {
			t.Fatal("session leaked outside allowed HTTPS metadata host")
		}
	}
	req, _ := http.NewRequest("POST", hongguoMultiVideoAPI, nil)
	hongguoApplyPlaybackCookies(req, device, now.Add(61*time.Second))
	if len(req.Cookies()) != 0 {
		t.Fatal("expired cookie reused")
	}
	response.Header = http.Header{}
	response.Header.Add("Set-Cookie", "ttreq=; Domain=.fqnovel.com; Path=/; Max-Age=0")
	deleted := hongguoMergePlaybackCookies(cookies, origin, response, now)
	for _, cookie := range deleted {
		if cookie.Cookie.Name == "ttreq" {
			t.Fatal("deleted cookie survived")
		}
	}
	clone := cloneHongguoPlaybackDevice(device)
	clone.Cookies[0].Cookie.Value = "other"
	if device.Cookies[0].Cookie.Value == "other" {
		t.Fatal("shared mutable cookie storage")
	}
}

// TestHongguoPlaybackOldIdentityIsNotReplacedForMissingCookies 验证「Hongguo Playback Old Identity Is Not Replaced For Missing Cookies」回归场景。
func TestHongguoPlaybackOldIdentityIsNotReplacedForMissingCookies(t *testing.T) {
	calls := 0
	d := sourceFixtureDownloader(t, func(req *http.Request) (*http.Response, error) {
		calls++
		if req.URL.Hostname() != "api5-normal-sinfonlineb.fqnovel.com" {
			t.Fatal("existing device replaced")
		}
		return sourceFixtureResponse(req, 200, ""), nil
	})
	p := filepath.Join(d.cfg.dataDir, "hongguo-playback-device.json")
	original := []byte(`{"version":1,"deviceId":"123456789","installId":"987654321"}`)
	if err := os.WriteFile(p, original, 0600); err != nil {
		t.Fatal(err)
	}
	_, err := d.resolveHongguoMultiMedia(context.Background(), "5678")
	if err == nil || calls != 1 {
		t.Fatal("empty response triggered new registration or retries")
	}
	current, _ := os.ReadFile(p)
	if !bytes.Equal(original, current) {
		t.Fatal("existing identity overwritten")
	}
}
