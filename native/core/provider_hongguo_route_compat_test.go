package core

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestHongguoRouteBudgetKeepsLegacyBackups(t *testing.T) {
	const primary = `{"code":0,"data":{"1004":{"5678":{"video_model":{"video_list":[{"main_url":"https://main.qznovelvod.com/57.mp4","video_meta":{"definition":"720p","codec_type":"h264"}}]}}}}}`
	const multi = `{"code":0,"data":{"5678":{"video_model":{"video_list":{"video_1":{"main_url":"https://a.qznovelvod.com/57.mp4","backup_url":"https://b.qznovelvod.com/57.mp4","vwidth":720,"vheight":1280,"codec_type":"h264"}}}}}}`
	var multiReads int
	d := sourceFixtureDownloader(t, func(r *http.Request) (*http.Response, error) {
		switch {
		case strings.Contains(r.URL.Path, "device_register"):
			return sourceFixtureResponse(r, 200, `{"device_id_str":"123456789","install_id_str":"987654321"}`), nil
		case r.URL.Hostname() == "api5-normal-sinfonlinea.fqnovel.com":
			return sourceFixtureResponse(r, 200, primary), nil
		case r.URL.Path == hongguoVideoModelPath:
			timer := time.NewTimer(4500 * time.Millisecond)
			defer timer.Stop()
			select {
			case <-r.Context().Done():
				return nil, r.Context().Err()
			case <-timer.C:
			}
			return sourceFixtureResponse(r, 200, `{"code":0,"data":{}}`), nil
		case r.URL.Hostname() == "hongguoduanju.com":
			return sourceFixtureResponse(r, 404, ""), nil
		default:
			if r.Method != http.MethodPost || !strings.HasPrefix(hongguoMultiVideoAPI, "https://"+r.URL.Host+r.URL.Path) {
				t.Fatalf("unexpected fixture read %s %s", r.Method, r.URL.Path)
			}
			multiReads++
			return sourceFixtureResponse(r, 200, multi), nil
		}
	})
	c := d.hongguoClient()
	c.state.DeviceID, c.state.InstallID, c.state.Registered = "123456789", "987654321", true
	m, err := d.resolveHongguoMedia(context.Background(), Task{DramaID: "hongguo:1234", Index: 57, Chapter: Chapter{VideoURL: "hongguo-cenc://5678"}})
	var addresses []string
	for _, v := range nativePlaybackChoicesForSource(sourceHongguo, m, 0).media {
		addresses = append(addresses, v.URL)
	}
	t.Logf("episode=57 slow_single_ms=4500 legacy_multi_reads=%d selectable_addresses=%d", multiReads, len(addresses))
	if err != nil || multiReads != 1 || len(addresses) != 3 {
		t.Fatalf("lost advertised same-episode backups: %v %v", addresses, err)
	}
	for _, u := range []string{"https://main.qznovelvod.com/57.mp4", "https://a.qznovelvod.com/57.mp4", "https://b.qznovelvod.com/57.mp4"} {
		found := false
		for _, a := range addresses {
			found = found || a == u
		}
		if !found {
			t.Fatal("missing issued URL", u)
		}
	}
}

func TestHongguoHistoricalIssuedHTTPBackup(t *testing.T) {
	main := "https://main.qznovelvod.com/e57?piece=a%2Fb"
	backup := "http://backup.qznovelvod.com/e57?piece=b%2Fc"
	got := hongguoMediaAddresses(map[string]any{"main_url": main, "backup_url": base64.StdEncoding.EncodeToString([]byte(backup))})
	t.Logf("advertised=2 parsed=%d", len(got))
	if len(got) != 2 || got[0] != main || strings.TrimPrefix(strings.TrimPrefix(got[1], "https://"), "http://") != strings.TrimPrefix(backup, "http://") {
		t.Fatalf("missing original CDN/path/query: %v", got)
	}
}

func TestHongguoLegacyMediaURLsStayOnApprovedHTTPS(t *testing.T) {
	issued := []any{"http://backup.qznovelvod.com:80/e?x=a%2Fb+x", "//two.fqnovelvod.com/e?x=%2b", "https://two.fqnovelvod.com/e?x=%2b", "http://127.0.0.1/a", "http://example.test/a", "http://a.qznovelvod.com.bad.invalid/a", "http://user@a.qznovelvod.com/a", "http://a.qznovelvod.com:8080/a", "//x.invalid/a", "#no", "file:///x"}
	want := []string{"https://backup.qznovelvod.com/e?x=a%2Fb+x", "https://two.fqnovelvod.com/e?x=%2b"}
	if got := hongguoMediaAddresses(map[string]any{"backup_urls": issued}); !reflect.DeepEqual(want, got) {
		t.Fatal(got)
	}
}

func TestHongguoBothModelEnvelopesRequireExactEpisode(t *testing.T) {
	entry := map[string]any{"item_id": "57", "video_model": map[string]any{"video_list": []any{map[string]any{"main_url": "https://one.qznovelvod.com/57.mp4", "backup_url": "https://two.qznovelvod.com/57.mp4"}}}}
	wrong := map[string]any{"item_id": "99", "video_model": map[string]any{}}
	for _, tab := range []bool{false, true} {
		main, alternate := "1004", "video_model_data"
		if tab {
			main, alternate = alternate, main
		}
		data := map[string]any{main: map[string]any{"99": wrong}, alternate: map[string]any{"57": entry}}
		response := map[string]any{"code": 0, "data": data}
		model, err := hongguoMobileV5Model(response, "57", tab)
		if err != nil {
			t.Fatal(err)
		}
		m, err := selectHongguoAppMedia(model)
		if err != nil || len(m.Variants) != 2 {
			t.Fatal("lost same-episode alternatives", err)
		}
		data[main] = map[string]any{"57": wrong}
		if _, err := hongguoMobileV5Model(response, "57", tab); err == nil {
			t.Fatal("accepted mismatched explicit episode id")
		}
		if _, err := hongguoMobileV5Model(response, "113", tab); err == nil {
			t.Fatal("used a different episode")
		}
	}
}

func TestHongguoSingleModelKeepsExplicitEpisodeIdentity(t *testing.T) {
	for _, tc := range []struct {
		item, asset string
		ok          bool
	}{{"57", "asset-57", true}, {"99", "57", false}, {"", "99", false}} {
		out, _ := json.Marshal(map[string]any{"code": 0, "data": map[string]any{"item_id": tc.item, "video_id": tc.asset, "video_model": map[string]any{"video_list": []any{map[string]any{"main_url": "https://a.qznovelvod.com/ep57", "backup_url": "http://b.qznovelvod.com/ep57"}}}}})
		d := sourceFixtureDownloader(t, func(r *http.Request) (*http.Response, error) { return sourceFixtureResponse(r, 200, string(out)), nil })
		c := d.hongguoClient()
		c.state.DeviceID, c.state.InstallID, c.state.Registered = "123456789", "987654321", true
		m, err := d.resolveHongguoAppMedia(context.Background(), "57")
		if (err == nil) != tc.ok || tc.ok && len(m.Variants) != 2 {
			t.Fatal(tc, len(m.Variants), err)
		}
	}
}

func TestHongguoPublicCanonicalIsOnlyForExactFirstEpisode(t *testing.T) {
	for _, tc := range []struct {
		name     string
		index    int
		returned string
		wantOK   bool
		wantPath string
	}{
		{"canonical first", 1, "5678", true, "/player/1234"},
		{"wrong id on canonical", 1, "99", false, "/player/1234"},
		{"later episode", 57, "5678", true, "/player/1234/5678"},
		{"canonical is not a later episode fallback", 57, "99", false, "/player/1234/5678"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var pages []string
			d := sourceFixtureDownloader(t, func(r *http.Request) (*http.Response, error) {
				if r.URL.Hostname() != "hongguoduanju.com" {
					return sourceFixtureResponse(r, 200, `{"code":0,"data":{}}`), nil
				}
				pages = append(pages, r.URL.Path)
				p := map[string]any{"isSuccess": true, "series_id": "1234", "vid": tc.returned, "video_player_info": map[string]any{"main_url": "https://one.fqnovelvod.com/57.mp4", "backup_url": "https://two.fqnovelvod.com/57.mp4"}}
				b, _ := json.Marshal(map[string]any{"loaderData": map[string]any{"player_page": p}})
				return sourceFixtureResponse(r, 200, "<script>_ROUTER_DATA="+string(b)+"</script>"), nil
			})
			c := d.hongguoClient()
			c.state.DeviceID, c.state.InstallID, c.state.Registered = "123456789", "987654321", true
			m, err := d.resolveHongguoMedia(context.Background(), Task{DramaID: "hongguo:1234", Index: tc.index, Chapter: Chapter{VideoURL: "hongguo-cenc://5678"}})
			if !reflect.DeepEqual(pages, []string{tc.wantPath}) || (err == nil) != tc.wantOK || tc.wantOK && len(m.Variants) != 2 {
				t.Fatal(pages, len(m.Variants), err)
			}
		})
	}
}

func TestHongguoRouteBudgetPreservesCallerDeadlineAndDenial(t *testing.T) {
	const multi = `{"code":0,"data":{"5678":{"video_model":{"video_list":[{"main_url":"https://a.qznovelvod.com/a","backup_url":"https://b.qznovelvod.com/b","codec_type":"h264","vheight":720}]}}}}`
	for _, denied := range []bool{false, true} {
		var reads int
		d := sourceFixtureDownloader(t, func(r *http.Request) (*http.Response, error) {
			if r.URL.Hostname() == "api5-normal-sinfonlinea.fqnovel.com" {
				if denied {
					return sourceFixtureResponse(r, 403, ""), nil
				}
				<-r.Context().Done()
				return nil, r.Context().Err()
			}
			if r.URL.Hostname() == "hongguoduanju.com" {
				return sourceFixtureResponse(r, 404, ""), nil
			}
			if r.URL.Path == hongguoVideoModelPath {
				return sourceFixtureResponse(r, 200, `{"code":0,"data":{}}`), nil
			}
			reads++
			return sourceFixtureResponse(r, 200, multi), nil
		})
		c := d.hongguoClient()
		c.state.DeviceID, c.state.InstallID, c.state.Registered = "123456789", "987654321", true
		task := Task{DramaID: "hongguo:1234", Index: 57, Chapter: Chapter{VideoURL: "hongguo-cenc://5678"}}
		ctx, cancel := context.WithTimeout(context.Background(), 2200*time.Millisecond)
		m, err := d.resolveHongguoMedia(ctx, task)
		cancel()
		if denied && (reads != 0 || err == nil) || !denied && (reads != 1 || err != nil || len(m.Variants) != 2) {
			t.Fatal(denied, reads, len(m.Variants), err)
		}
		if _, err := d.resolveHongguoMedia(ctx, task); !errors.Is(err, context.Canceled) {
			t.Fatal("cancellation ignored", err)
		}
	}
}
