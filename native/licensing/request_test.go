package licensing

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// TestOnlyRetryBeforeSendingCredentials 验证「Only Retry Before Sending Credentials」回归场景。
func TestOnlyRetryBeforeSendingCredentials(t *testing.T) {
	for _, mode := range []string{"before-connection", "after-submission", "http-denial", "certificate"} {
		t.Run(mode, func(t *testing.T) {
			var dials, seen atomic.Int32
			srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				seen.Add(1)
				if mode == "after-submission" {
					c, _, e := w.(http.Hijacker).Hijack()
					if e == nil {
						c.Close()
					}
					return
				}
				if mode == "http-denial" {
					w.WriteHeader(429)
					return
				}
				w.WriteHeader(200)
			}))
			defer srv.Close()
			tr := srv.Client().Transport.(*http.Transport).Clone()
			tr.Proxy = nil
			if mode == "certificate" {
				tr.TLSClientConfig = nil
			}
			tr.DialContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
				n := dials.Add(1)
				if n == 1 && mode == "before-connection" {
					return nil, &net.OpError{Op: "dial", Net: "tcp", Err: io.ErrUnexpectedEOF}
				}
				return (&net.Dialer{Timeout: time.Second}).DialContext(ctx, network, addr)
			}
			defer tr.CloseIdleConnections()
			c := &http.Client{Transport: tr, Timeout: 3 * time.Second}
			req, _ := http.NewRequest("POST", srv.URL, strings.NewReader("neutral-fixture-proof"))
			res, err := sendLicenseRequest(c, req)
			if res != nil {
				res.Body.Close()
			}
			switch mode {
			case "before-connection":
				if err != nil || res.StatusCode != 200 || dials.Load() != 2 || seen.Load() != 1 {
					t.Fatal("single safe reconnect", dials.Load(), seen.Load(), err)
				}
			case "http-denial":
				if err != nil || res.StatusCode != 429 || seen.Load() != 1 || dials.Load() != 1 {
					t.Fatal("replayed request after server refusal")
				}
			case "after-submission":
				if err == nil || seen.Load() != 1 || dials.Load() != 1 {
					t.Fatal("replayed a credential request", seen.Load(), dials.Load(), err)
				}
			case "certificate":
				if err == nil || seen.Load() != 0 || dials.Load() != 1 {
					t.Fatal("retried or accepted an untrusted certificate")
				}
			}
		})
	}
}
