package core

import (
	"context"
	"crypto/tls"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestHongguoCDNAddressesBecomeVerifiedPlaybackRoutes(t *testing.T) {
	mediaBody := append([]byte{0, 0, 0, 24}, []byte("ftypisom00000000")...)
	server := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Host != "v26-hgweb.qznovelvod.com" || request.TLS.ServerName != "v26-hgweb.qznovelvod.com" || request.Header.Get("Range") != "bytes=0-1023" {
			t.Fatal("CDN probe changed signed request identity", request.Host, request.TLS.ServerName, request.Header.Get("Range"))
		}
		writer.Header().Set("Content-Type", "video/mp4")
		writer.WriteHeader(http.StatusPartialContent)
		_, _ = writer.Write(mediaBody)
	}))
	defer server.Close()
	base := http.DefaultTransport.(*http.Transport).Clone()
	base.Proxy = nil
	base.TLSClientConfig = &tls.Config{InsecureSkipVerify: true}
	base.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		if address == "93.184.216.34:443" || address == "93.184.216.35:443" {
			return (&net.Dialer{}).DialContext(ctx, network, server.Listener.Addr().String())
		}
		return nil, io.EOF
	}
	resolver := newDNSResolver(base)
	resolver.client.Transport = sourceFixtureTransport(func(request *http.Request) (*http.Response, error) {
		return sourceFixtureResponse(request, 200, `{"Status":0,"Answer":[{"type":1,"TTL":120,"data":"93.184.216.34"},{"type":1,"TTL":120,"data":"93.184.216.35"}]}`), nil
	})
	cdn := newCDNTransport(base, resolver)
	defer cdn.CloseIdleConnections()
	d := &Downloader{client: &http.Client{Transport: cdn}, cdn: cdn}
	media := providerMedia{Direct: true, URL: "https://v26-hgweb.qznovelvod.com/movie.mp4?signature=a%2Fb", Referer: "https://hongguoduanju.com/player/1"}
	resolved := d.expandHongguoCDNRoutes(context.Background(), media)
	choices := nativePlaybackChoicesForSource(sourceHongguo, resolved, 0)
	if len(choices.media) != 3 {
		t.Fatalf("expected default plus two verified CDN routes, got %d", len(choices.media))
	}
	if choices.media[0].routeAddress != "" || choices.media[1].routeAddress == "" || choices.media[2].routeAddress == "" || choices.media[1].routeAddress == choices.media[2].routeAddress {
		t.Fatal("verified CDN route identities were not preserved")
	}
}
