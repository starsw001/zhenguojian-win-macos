package core

import (
	"context"
	"crypto/tls"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

// TestCDNKeyEOFUsesVerifiedDNSAndKeepsMediaIdentity 验证「CDN Key EOF Uses Verified DNS And Keeps Media Identity」回归场景。
func TestCDNKeyEOFUsesVerifiedDNSAndKeepsMediaIdentity(t *testing.T) {
	var direct, lookups atomic.Int32
	server := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Host != "tp.tuafjz.cn" || request.TLS.ServerName != "tp.tuafjz.cn" || request.URL.RequestURI() != "/videos/synthetic/crypt.key?signature=a%2Fb" || request.Header.Get("Referer") != "https://huangguoai.com/watch/synthetic/" {
			t.Error("DNS recovery changed SNI, host, signature or referer", request.Host, request.URL, request.TLS.ServerName)
		}
		_, _ = writer.Write([]byte("0123456789abcdef"))
	}))
	defer server.Close()
	base := http.DefaultTransport.(*http.Transport).Clone()
	base.Proxy = nil
	base.TLSClientConfig = &tls.Config{InsecureSkipVerify: true}
	base.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		if address == "tp.tuafjz.cn:443" {
			direct.Add(1)
			return nil, io.EOF
		}
		if address != "93.184.216.34:443" {
			t.Error("unexpected DNS destination", address)
			return nil, io.EOF
		}
		return (&net.Dialer{}).DialContext(ctx, network, server.Listener.Addr().String())
	}
	resolver := newDNSResolver(base)
	resolver.client.Transport = sourceFixtureTransport(func(request *http.Request) (*http.Response, error) {
		lookups.Add(1)
		if request.URL.Query().Get("name") != "tp.tuafjz.cn" {
			t.Error("unexpected DNS query")
		}
		return sourceFixtureResponse(request, 200, `{"Status":0,"Answer":[{"type":1,"TTL":120,"data":"93.184.216.34"}]}`), nil
	})
	transport := newCDNTransport(base, resolver)
	defer transport.CloseIdleConnections()
	d := &Downloader{client: &http.Client{Transport: transport}}
	for index := 0; index < 2; index++ {
		request, _ := http.NewRequest(http.MethodGet, "https://tp.tuafjz.cn/videos/synthetic/crypt.key?signature=a%2Fb", nil)
		request.Header.Set("Referer", "https://huangguoai.com/watch/synthetic/")
		response, err := d.doMediaRequest(request)
		if err != nil {
			t.Fatal(err)
		}
		body, err := io.ReadAll(response.Body)
		response.Body.Close()
		if err != nil || string(body) != "0123456789abcdef" || response.Request.URL.Host != "tp.tuafjz.cn" {
			t.Fatal("media key did not recover", err)
		}
	}
	if direct.Load() != 1 || lookups.Load() != 1 {
		t.Fatal("working CDN route was not reused", direct.Load(), lookups.Load())
	}
	for _, addresses := range [][]string{{"127.0.0.1"}, {"198.18.0.1"}, {"93.184.216.34", "10.0.0.1"}} {
		if validateCDNAddresses(addresses) == nil {
			t.Fatal("unsafe DNS answer was accepted", addresses)
		}
	}
}

// TestCoverDNSRecoveryAcceptsNewImageHosts 验证「Cover DNS Recovery Accepts New Image Hosts」回归场景。
func TestCoverDNSRecoveryAcceptsNewImageHosts(t *testing.T) {
	var direct, lookups atomic.Int32
	server := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Host != "new-cover.example.test" || request.TLS.ServerName != "new-cover.example.test" || request.URL.RequestURI() != "/poster/synthetic.img?signature=a%2Fb" || request.Header.Get("Referer") != "https://huangguoai.com/watch/synthetic/" {
			t.Error("DNS recovery changed SNI, host, signature or referer", request.Host, request.URL, request.TLS.ServerName)
		}
		_, _ = writer.Write([]byte("0123456789abcdef"))
	}))
	defer server.Close()
	base := http.DefaultTransport.(*http.Transport).Clone()
	base.Proxy = nil
	base.TLSClientConfig = &tls.Config{InsecureSkipVerify: true}
	base.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		if address == "new-cover.example.test:443" {
			direct.Add(1)
			return nil, io.EOF
		}
		if address != "93.184.216.34:443" {
			t.Error("unexpected DNS destination", address)
			return nil, io.EOF
		}
		return (&net.Dialer{}).DialContext(ctx, network, server.Listener.Addr().String())
	}
	resolver := newDNSResolver(base)
	resolver.client.Transport = sourceFixtureTransport(func(request *http.Request) (*http.Response, error) {
		lookups.Add(1)
		if request.URL.Query().Get("name") != "new-cover.example.test" {
			t.Error("unexpected DNS query")
		}
		return sourceFixtureResponse(request, 200, `{"Status":0,"Answer":[{"type":1,"TTL":120,"data":"93.184.216.34"}]}`), nil
	})
	transport := newCDNTransport(base, resolver)
	defer transport.CloseIdleConnections()
	d := &Downloader{client: &http.Client{Transport: transport}}
	for index := 0; index < 2; index++ {
		request, _ := http.NewRequestWithContext(context.WithValue(context.Background(), nativeCoverNetworkKey{}, true), http.MethodGet, "https://new-cover.example.test/poster/synthetic.img?signature=a%2Fb", nil)
		request.Header.Set("Referer", "https://huangguoai.com/watch/synthetic/")
		response, err := d.doMediaRequest(request)
		if err != nil {
			t.Fatal(err)
		}
		body, err := io.ReadAll(response.Body)
		response.Body.Close()
		if err != nil || string(body) != "0123456789abcdef" || response.Request.URL.Host != "new-cover.example.test" {
			t.Fatal("media key did not recover", err)
		}
	}
	if direct.Load() != 1 || lookups.Load() != 1 {
		t.Fatal("working CDN route was not reused", direct.Load(), lookups.Load())
	}
	for _, addresses := range [][]string{{"127.0.0.1"}, {"198.18.0.1"}, {"93.184.216.34", "10.0.0.1"}} {
		if validateCDNAddresses(addresses) == nil {
			t.Fatal("unsafe DNS answer was accepted", addresses)
		}
	}
}

func TestHongguoMediaHostsUseCDNRecovery(t *testing.T) {
	for _, host := range []string{"v5-reading-video-b.qznovelvod.com", "video.fqnovelvod.com", "video.douyinvod.com"} {
		if !protectedCDNHost(host) {
			t.Fatal("Hongguo media host did not use CDN recovery", host)
		}
	}
	for _, host := range []string{"qznovelvod.com.invalid", "fqnovelvod.com.example", "douyinvod.com.test"} {
		if protectedCDNHost(host) {
			t.Fatal("lookalike host used CDN recovery", host)
		}
	}
}

func TestExplicitCDNRouteKeepsHostAndSelectsAddress(t *testing.T) {
	first := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		_, _ = writer.Write([]byte("first"))
	}))
	defer first.Close()
	second := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Host != "v26-hgweb.qznovelvod.com" || request.TLS.ServerName != "v26-hgweb.qznovelvod.com" {
			t.Fatal("explicit route changed host or SNI", request.Host, request.TLS.ServerName)
		}
		_, _ = writer.Write([]byte("second"))
	}))
	defer second.Close()
	base := http.DefaultTransport.(*http.Transport).Clone()
	base.Proxy = nil
	base.TLSClientConfig = &tls.Config{InsecureSkipVerify: true}
	base.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		switch address {
		case "93.184.216.34:443":
			return (&net.Dialer{}).DialContext(ctx, network, first.Listener.Addr().String())
		case "93.184.216.35:443":
			return (&net.Dialer{}).DialContext(ctx, network, second.Listener.Addr().String())
		default:
			return nil, io.EOF
		}
	}
	transport := newCDNTransport(base, newDNSResolver(base))
	defer transport.CloseIdleConnections()
	d := &Downloader{client: &http.Client{Transport: transport}}
	media := providerMedia{URL: "https://v26-hgweb.qznovelvod.com/movie.mp4?signature=a%2Fb", Referer: "https://hongguoduanju.com/player/1", routeAddress: "93.184.216.35"}
	request, _ := http.NewRequestWithContext(providerMediaRequestContext(context.Background(), media), http.MethodGet, media.URL, nil)
	response, err := d.doMediaRequest(request)
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(response.Body)
	response.Body.Close()
	if err != nil || string(body) != "second" || response.Request.URL.Hostname() != "v26-hgweb.qznovelvod.com" {
		t.Fatal("explicit CDN route was not preserved", string(body), err)
	}
	firstMedia := media
	firstMedia.routeAddress = "93.184.216.34"
	if nativeRouteKey(firstMedia) == nativeRouteKey(media) {
		t.Fatal("different CDN addresses shared one route key")
	}
}
