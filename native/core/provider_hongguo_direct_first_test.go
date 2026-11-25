package core

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"
)

func TestHongguoFreshSessionPreservesEpisodeRoutes(t *testing.T) {
	var reads, registrations int
	d := sourceFixtureDownloader(t, func(r *http.Request) (*http.Response, error) {
		if r.URL.Hostname() == "log5-applog-lf.fqnovel.com" {
			registrations++
			return sourceFixtureResponse(r, 200, `{"code":0,"device_id_str":"123456789","install_id_str":"987654321"}`), nil
		}
		if r.URL.Query().Get("device_id") != "123456789" || r.URL.Query().Get("iid") != "987654321" {
			t.Fatal("saved session not used")
		}
		reads++
		switch {
		case r.Method == http.MethodPost && r.URL.Hostname() == "api5-normal-sinfonlinea.fqnovel.com":
			return sourceFixtureResponse(r, 200, `{"code":0,"data":{"video_model_data":{"5678":{"video_model":{"video_list":[{"main_url":"https://one.qznovelvod.com/5678","backup_url":"https://two.qznovelvod.com/5678","video_meta":{"definition":"720p","codec_type":"h264"}}]}}}}}`), nil
		default:
			return nil, fmt.Errorf("unexpected endpoint %s", r.URL.Path)
		}
	})
	media, err := d.resolveHongguoMobileV5Media(context.Background(), "5678")
	choices := nativePlaybackChoicesForSource(sourceHongguo, media, 720)
	t.Logf("new_registrations=%d metadata_reads=%d selectable_addresses=%d err=%v", registrations, reads, len(choices.media), err)
	if err != nil || registrations != 1 || reads != 1 || len(choices.media) != 2 || choices.media[0].URL != "https://one.qznovelvod.com/5678" || choices.media[1].URL != "https://two.qznovelvod.com/5678" {
		t.Fatal("same-episode primary and backup not available")
	}
}

func TestHongguoLoopbackPlayerUsesWorkingRouteAndRestartsDirectOnSwitch(t *testing.T) {
	content := make([]byte, 2048)
	copy(content[4:], "ftyp")
	var directDials, proxyRequests atomic.Int32
	var directUp atomic.Bool
	serve := func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/11" && r.URL.Path != "/backup11" {
			t.Errorf("wrong episode: %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "video/mp4")
		start := 0
		if r.Header.Get("Range") == "bytes=1024-2047" {
			start = 1024
		} else if r.Header.Get("Range") != "bytes=0-1023" {
			t.Error("missing Range")
		}
		w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/2048", start, start+1023))
		w.Header().Set("Content-Length", "1024")
		w.WriteHeader(http.StatusPartialContent)
		_, _ = w.Write(content[start : start+1024])
	}
	origin := httptest.NewServer(http.HandlerFunc(serve))
	defer origin.Close()
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !r.URL.IsAbs() || r.URL.Hostname() != "unit.qznovelvod.com" {
			t.Error("not the configured HTTP proxy path")
		}
		proxyRequests.Add(1)
		serve(w, r)
	}))
	defer proxy.Close()
	e, err := newNativeEngine(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(e.downloads.close)
	router := e.downloader.proxyRouter
	router.systemOverride = true
	router.system = nativeSystemProxy{HTTP: proxy.URL}
	base := &http.Transport{Proxy: router.proxy, DisableKeepAlives: true, DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
		switch addr {
		case "unit.qznovelvod.com:80":
			directDials.Add(1)
			if !directUp.Load() {
				return nil, &net.OpError{Op: "dial", Net: "tcp", Err: syscall.ECONNREFUSED}
			}
			addr = origin.Listener.Addr().String()
		case proxy.Listener.Addr().String():
		default:
			return nil, errors.New("not a local fixture route")
		}
		return (&net.Dialer{}).DialContext(ctx, network, addr)
	}}
	e.downloader.client.Transport = newAutoProxyFallbackTransport(base, router, nil)
	media := providerMedia{URL: "http://unit.qznovelvod.com/11", Direct: true, Variants: []providerMedia{{URL: "http://unit.qznovelvod.com/backup11", Direct: true}}}
	plan, err := e.nativeOpenPlayback(context.Background(), nativePlaybackChoicesForSource(sourceHongguo, media, 0))
	if err != nil {
		t.Fatal(err)
	}
	defer e.stream.server.Close()
	defer e.nativeReleasePlayback(plan.Session)
	if plan.RouteCount != 2 {
		t.Fatal("backup route missing")
	}
	player := &http.Client{Transport: &http.Transport{}}
	defer player.CloseIdleConnections()
	read := func(plan nativePlan, start int) {
		t.Helper()
		r, _ := http.NewRequest(http.MethodGet, plan.URL, nil)
		r.Header.Set("Range", fmt.Sprintf("bytes=%d-%d", start, start+1023))
		res, err := player.Do(r)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		data, err := io.ReadAll(res.Body)
		if err != nil || res.StatusCode != 206 || !bytes.Equal(data, content[start:start+1024]) {
			t.Fatal("unexpected range", res.StatusCode, len(data), err)
		}
	}
	read(plan, 0)
	read(plan, 1024)
	if directDials.Load() != 1 || proxyRequests.Load() != 2 {
		t.Fatalf("range did not stay on the working proxy: direct=%d proxy=%d", directDials.Load(), proxyRequests.Load())
	}
	directUp.Store(true)
	next, err := e.nativeSelectPlaybackRoute(context.Background(), plan.Session, 1)
	if err != nil {
		t.Fatal(err)
	}
	defer e.nativeReleasePlayback(next.Session)
	read(next, 0)
	if directDials.Load() != 2 || proxyRequests.Load() != 2 {
		t.Fatal("switch did not reset to direct")
	}
	proxy.Close()
	read(plan, 1024)
	if directDials.Load() != 3 || proxyRequests.Load() != 2 {
		t.Fatal("stopped proxy did not return to direct")
	}
	t.Log("two selectable same-episode addresses; first direct failed -> proxy 206/1024; seek proxy 206/1024; switched address direct 206/1024; old proxy stopped -> direct 206/1024")
}

func TestHongguoIssuedAddressesUseOrderedValidBackups(t *testing.T) {
	raw := `{"Code":0,"data":{"1004":{"5678":{"video_model":{"video_list":[{"mainUrl":"https://one.qznovelvod.com/11","backup_url_12":"https://twelve.qznovelvod.com/11","backup_url_3":"https://three.qznovelvod.com/11","backupUrlList":["https://three.qznovelvod.com/11","https://four.qznovelvod.com/11","https://bad.invalid/11"]}]}}},"9999":{"video_model":{"video_list":[{"main_url":"https://wrong.qznovelvod.com/99"}]}}}}`
	d := sourceFixtureDownloader(t, func(r *http.Request) (*http.Response, error) {
		if r.URL.Hostname() == "log5-applog-lf.fqnovel.com" {
			return sourceFixtureResponse(r, 200, `{"code":0,"device_id_str":"123456789","install_id_str":"987654321"}`), nil
		}
		return sourceFixtureResponse(r, 200, raw), nil
	})
	media, err := d.resolveHongguoMobileV5Media(context.Background(), "5678")
	if err != nil {
		t.Fatal(err)
	}
	var hosts []string
	for _, m := range nativePlaybackChoicesForSource(sourceHongguo, media, 0).media {
		u, _ := url.Parse(m.URL)
		hosts = append(hosts, u.Host)
	}
	if !reflect.DeepEqual(hosts, []string{"one.qznovelvod.com", "three.qznovelvod.com", "twelve.qznovelvod.com", "four.qznovelvod.com"}) {
		t.Fatal(hosts)
	}
}

func TestHongguoLaterEpisodesSkipWebSupplement(t *testing.T) {
	webReads := 0
	d := sourceFixtureDownloader(t, func(request *http.Request) (*http.Response, error) {
		if request.URL.Hostname() == "hongguoduanju.com" {
			webReads++
			return sourceFixtureResponse(request, http.StatusNotFound, ""), nil
		}
		if request.URL.Hostname() == "log5-applog-lf.fqnovel.com" {
			return sourceFixtureResponse(request, http.StatusOK, `{"code":0,"device_id_str":"123456789","install_id_str":"987654321"}`), nil
		}
		if request.Method == http.MethodPost && hongguoRequestAt(request, hongguoMobileV5API) {
			return sourceFixtureResponse(request, http.StatusOK, `{"Code":0,"data":{"1004":{"5678":{"video_model":{"video_list":[{"main_url":"https://one.qznovelvod.com/5678.mp4","video_meta":{"codec_type":"h264","definition":"720p"},"encrypt_info":{"encrypt":false}}]}}}}}`), nil
		}
		return sourceFixtureResponse(request, http.StatusOK, ""), nil
	})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	media, err := d.resolveHongguoMediaRoutes(ctx, 6, "1234", "5678")
	if err != nil || webReads != 0 || media.URL != "https://one.qznovelvod.com/5678.mp4" {
		t.Fatalf("webReads=%d media=%+v err=%v", webReads, media, err)
	}
}

func TestHongguoDirectFirstEmptyEpisodeReplay(t *testing.T) {
	const model = `{"Code":0,"data":{"1004":{"5678":{"video_model":{"video_list":[{"main_url":"https://one.qznovelvod.com/5678","backup_url":"https://two.qznovelvod.com/5678"}]}}}}}`
	for _, tc := range []struct {
		name                   string
		hasProxy, validDirect  bool
		webStatus, proxyStatus int
		wantProxy, wantRoutes  int
	}{
		{"direct_has_routes", true, true, 404, 200, 0, 2},
		{"all_empty_retry_same_read", true, false, 404, 200, 1, 2},
		{"proxy_also_empty", true, false, 404, 204, 1, 0},
		{"no_configured_proxy", false, false, 404, 200, 0, 0},
		{"upstream_denial", true, false, 403, 200, 0, 0},
		{"upstream_rate_limit", true, false, 429, 200, 0, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			router := autoProxyFixtureRouter("auto")
			router.hongguoMode = "auto"
			if !tc.hasProxy {
				router.system = nativeSystemProxy{Bypass: []string{"*"}}
			}
			d := sourceFixtureDownloader(t, nil)
			d.proxyRouter = router
			proxied, direct, web := 0, 0, 0
			var first *http.Request
			var firstData []byte
			d.client.Transport = newAutoProxyFallbackTransport(sourceFixtureTransport(func(r *http.Request) (*http.Response, error) {
				var b []byte
				if r.Body != nil {
					b, _ = io.ReadAll(r.Body)
					r.Body.Close()
				}
				p, e := router.proxy(r)
				if e != nil {
					t.Fatal(e)
				}
				if p != nil {
					proxied++
					if first == nil || web != 1 || first.Method != r.Method || first.URL.String() != r.URL.String() || !reflect.DeepEqual(first.Header, r.Header) || !bytes.Equal(firstData, b) {
						t.Fatal("proxy did not replay the exact metadata request after all direct sources")
					}
					if tc.proxyStatus == 200 {
						return sourceFixtureResponse(r, 200, model), nil
					}
					return sourceFixtureResponse(r, 200, ""), nil
				}
				direct++
				if r.URL.Hostname() == "log5-applog-lf.fqnovel.com" {
					return sourceFixtureResponse(r, 200, `{"code":0,"device_id_str":"123456789","install_id_str":"987654321"}`), nil
				}
				if r.URL.Host == "hongguoduanju.com" {
					web++
					return sourceFixtureResponse(r, tc.webStatus, ""), nil
				}
				if r.Method == "POST" && hongguoRequestAt(r, hongguoMobileV5API) {
					first = r.Clone(context.Background())
					firstData = b
					if tc.validDirect {
						return sourceFixtureResponse(r, 200, model), nil
					}
				}
				return sourceFixtureResponse(r, 200, ""), nil
			}), router, nil)
			m, err := d.resolveHongguoMedia(context.Background(), Task{DramaID: "hongguo:1234", Index: 3, Chapter: Chapter{VideoURL: "hongguo-cenc://5678"}})
			n := len(nativePlaybackChoicesForSource(sourceHongguo, m, 0).media)
			t.Logf("direct=%d proxy=%d selectable_addresses=%d", direct, proxied, n)
			if proxied != tc.wantProxy || n != tc.wantRoutes || (err == nil) != (n > 0) {
				t.Fatal(err)
			}
		})
	}
}

func TestHongguoDirectFirstWirePolicy(t *testing.T) {
	for _, tc := range []struct {
		name, address, method        string
		hasProxy, failDirect, marked bool
		status                       int
		want                         []string
	}{
		{"direct usable", hongguoMobileV5API, "POST", true, false, true, 200, []string{"direct"}},
		{"direct failed", hongguoMobileV5API, "POST", true, true, true, 200, []string{"direct", "proxy"}},
		{"original multi", hongguoMultiVideoAPI, "POST", true, true, true, 200, []string{"direct", "proxy"}},
		{"no proxy", hongguoMobileV5API, "POST", false, true, true, 0, []string{"direct"}},
		{"unmarked POST", hongguoMultiVideoAPI, "POST", true, true, false, 0, []string{"direct"}},
		{"registration not replayed on ambiguous EOF", hongguoPlaybackRegisterURL, "POST", true, true, false, 0, []string{"direct"}},
		{"media range", "https://one.qznovelvod.com/e11", "GET", true, true, false, 206, []string{"direct", "proxy"}},
		{"authenticated denial", hongguoMobileV5API, "POST", true, false, true, 403, []string{"direct"}},
		{"rate limit", hongguoMobileV5API, "POST", true, false, true, 429, []string{"direct"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			router := autoProxyFixtureRouter("auto")
			router.hongguoMode = "auto"
			if !tc.hasProxy {
				router.system = nativeSystemProxy{Bypass: []string{"*"}}
			}
			ctx := context.WithValue(context.Background(), hongguoMetadataReadKey{}, tc.marked)
			var payload io.Reader
			if tc.method == "POST" {
				payload = strings.NewReader("fixture")
			}
			req, _ := http.NewRequestWithContext(ctx, tc.method, tc.address, payload)
			if payload == nil {
				req.Header.Set("Range", "bytes=0-1023")
			}
			var routes []string
			rt := newAutoProxyFallbackTransport(sourceFixtureTransport(func(r *http.Request) (*http.Response, error) {
				p, err := router.proxy(r)
				if err != nil {
					return nil, err
				}
				if p == nil {
					routes = append(routes, "direct")
				} else {
					routes = append(routes, "proxy")
				}
				if r.URL.String() != req.URL.String() || r.Header.Get("Range") != req.Header.Get("Range") {
					t.Fatal("request was rewritten")
				}
				if r.Body != nil {
					raw, _ := io.ReadAll(r.Body)
					r.Body.Close()
					if string(raw) != "fixture" {
						t.Fatal("request body changed")
					}
				}
				if tc.failDirect && p == nil {
					return nil, io.ErrUnexpectedEOF
				}
				return sourceFixtureResponse(r, tc.status, "ok"), nil
			}), router, nil)
			response, err := rt.RoundTrip(req)
			if response != nil {
				response.Body.Close()
			}
			if !reflect.DeepEqual(routes, tc.want) || (tc.status == 0) != (err != nil) || (tc.status != 0 && (response == nil || response.StatusCode != tc.status)) {
				t.Fatalf("paths=%v status=%d err=%v", routes, tc.status, err)
			}
		})
	}
}
