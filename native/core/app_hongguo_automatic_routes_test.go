package core

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"sync/atomic"
	"testing"
)

// TestNativeHongguoRenewedAddressChecksRepresentation 验证「Native Hongguo Renewed Address Checks Representation」回归场景。
func TestNativeHongguoRenewedAddressChecksRepresentation(t *testing.T) {
	for _, tc := range []struct {
		name              string
		first             int
		tag, contentRange string
		wantRanges        []string
		preserved         bool
		stop              bool
	}{
		{"matching", 206, `"old"`, "bytes 5-9/10", []string{"bytes=5-"}, false, false},
		{"different tag", 206, `"different"`, "bytes 5-9/10", []string{"bytes=5-", ""}, true, false},
		{"missing tag", 206, "", "bytes 5-9/10", []string{"bytes=5-", ""}, true, false},
		{"different total", 206, `"old"`, "bytes 5-9/11", []string{"bytes=5-", ""}, true, false},
		{"full response", 200, `"different"`, "", []string{"bytes=5-", ""}, true, false},
		{"unauthorized", 401, "", "", []string{"bytes=5-"}, false, true},
		{"forbidden", 403, "", "", []string{"bytes=5-"}, false, true},
		{"rate limited", 429, "", "", []string{"bytes=5-"}, false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			const oldURL = "https://v9-reading-video.qznovelvod.com/video/fixture?ticket=old"
			const newURL = "https://v9-reading-video.qznovelvod.com/video/fixture?ticket=new"
			ranges := []string{}
			engine := sourceFixtureEngine(t, func(req *http.Request) (*http.Response, error) {
				if req.URL.String() != newURL {
					t.Fatal("unexpected route")
				}
				ranges = append(ranges, req.Header.Get("Range"))
				if len(ranges) == 1 {
					data := "vwxyz"
					if tc.first == 200 {
						data = "0123456789"
					}
					if tc.first == 206 && tc.tag == `"old"` && tc.contentRange == "bytes 5-9/10" {
						data = "fghij"
					}
					res := sourceFixtureResponse(req, tc.first, data)
					res.Header.Set("Content-Type", "video/mp4")
					if tc.tag != "" {
						res.Header.Set("ETag", tc.tag)
					}
					if tc.contentRange != "" {
						res.Header.Set("Content-Range", tc.contentRange)
					}
					return res, nil
				}
				res := sourceFixtureResponse(req, 200, "0123456789")
				res.Header.Set("Content-Type", "video/mp4")
				return res, nil
			})
			job := nativeDownloadJob{ID: "renewal-test", Drama: nativeDrama{ID: "hongguo:1234"}, Index: 1}
			manager := engine.downloads
			dir := manager.jobDirectory(job)
			if e := os.MkdirAll(dir, 0700); e != nil {
				t.Fatal(e)
			}
			target := filepath.Join(dir, "media.mp4")
			state, _ := json.Marshal(nativeDownloadFileState{URL: oldURL, Validator: `"old"`, Total: 10})
			hash := sha256.Sum256([]byte("media.mp4\x00" + oldURL + "\x00\x00720"))
			for path, value := range map[string][]byte{target + ".part": []byte("abcde"), target + ".json": state, filepath.Join(dir, ".bundle"): []byte(hex.EncodeToString(hash[:]))} {
				if e := os.WriteFile(path, value, 0600); e != nil {
					t.Fatal(e)
				}
			}
			_, err := manager.transferMedia(context.Background(), job, providerMedia{URL: newURL, Direct: true, Quality: 720})
			if !reflect.DeepEqual(ranges, tc.wantRanges) || (err != nil) != tc.stop {
				t.Fatalf("requests=%v err=%v", ranges, err)
			}
			if tc.stop {
				data, e := os.ReadFile(target + ".part")
				if e != nil || string(data) != "abcde" {
					t.Fatalf("denial modified partial: %q %v", data, e)
				}
				return
			}
			final, e := os.ReadFile(target)
			want := "abcdefghij"
			if tc.preserved {
				want = "0123456789"
			}
			if e != nil || string(final) != want {
				t.Fatalf("combined different representations: %q %v", final, e)
			}
			if tc.preserved {
				backups, e := filepath.Glob(filepath.Join(manager.root, ".replaced-downloads", "*", "original", "media.mp4.part"))
				if e != nil || len(backups) != 1 {
					t.Fatal("original partial not backed up", backups, e)
				}
				b, e := os.ReadFile(backups[0])
				if e != nil || string(b) != "abcde" {
					t.Fatal("backed up bytes changed", e)
				}
			}
		})
	}
}

// TestNativeHongguoAutomaticSwitchStopsOnAccessResponse 验证「Native Hongguo Automatic Switch Stops On Access Response」回归场景。
func TestNativeHongguoAutomaticSwitchStopsOnAccessResponse(t *testing.T) {
	for _, status := range []int{404, 401, 403, 429, 503, 200} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			media := providerMedia{URL: "https://primary.qznovelvod.com/fixture", Direct: true, Quality: 1080, Variants: []providerMedia{{URL: "https://alternate.qznovelvod.com/fixture", Direct: true, Quality: 720}}}
			var calls atomic.Int32
			engine := sourceFixtureEngine(t, func(req *http.Request) (*http.Response, error) {
				calls.Add(1)
				if req.URL.Hostname() == "alternate.qznovelvod.com" {
					return sourceFixtureResponse(req, 200, "media"), nil
				}
				text := "fixture failure"
				if status == 200 {
					text = "<title>Just a moment</title> cloudflare"
				}
				res := sourceFixtureResponse(req, status, text)
				if status == 200 {
					res.Header.Set("Content-Type", "text/html")
				}
				if status == 503 || status == 429 {
					res.Header.Set("Retry-After", "60")
				}
				return res, nil
			})
			ctx := context.Background()
			plan, err := engine.nativeOpenPlayback(ctx, nativePlaybackChoicesForSource(sourceHongguo, media, 1080))
			if err != nil {
				t.Fatal(err)
			}
			defer engine.stream.server.Close()
			response, err := http.Get(plan.URL)
			if err != nil {
				t.Fatal(err)
			}
			_, _ = io.Copy(io.Discard, response.Body)
			response.Body.Close()
			next, err := engine.nativeNextPlayback(ctx, plan.Session)
			if status == 404 {
				if err != nil || next.Quality != 720 {
					t.Fatalf("soft failure did not switch to same episode: %v", err)
				}
				alt, e := http.Get(next.URL)
				if e != nil || alt.StatusCode != 200 {
					t.Fatalf("alternate %v %v", alt, e)
				}
				alt.Body.Close()
			} else if !errors.Is(err, errNativeMediaAccessStop) || calls.Load() != 1 {
				t.Fatalf("protected playback triggered a switch: %v %v", calls.Load(), err)
			}
			engine.downloads.resolve = func(context.Context, nativeDownloadJob) (providerMedia, error) { return media, nil }
			calls.Store(0)
			job := nativeDownloadJob{ID: "switch-guard", Drama: nativeDrama{ID: "hongguo:1234"}, Index: 1, Quality: 1080}
			result, err := engine.downloads.transfer(ctx, job)
			if status == 404 {
				if err != nil || result.quality != 720 || calls.Load() != 2 {
					t.Fatalf("download did not switch: %v %v", calls.Load(), err)
				}
			} else if err == nil || calls.Load() != 1 {
				t.Fatalf("protected download triggered a switch: %v %v", calls.Load(), err)
			}
		})
	}
}
