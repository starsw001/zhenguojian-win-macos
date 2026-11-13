package licensing

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

// TestLicenseRoutesUseComputerSettings 验证「License Routes Use Computer Settings」回归场景。
func TestLicenseRoutesUseComputerSettings(t *testing.T) {
	for _, k := range []string{"HTTP_PROXY", "HTTPS_PROXY", "ALL_PROXY", "NO_PROXY", "REQUEST_METHOD"} {
		t.Setenv(k, "")
	}
	req, _ := http.NewRequest("GET", "https://license.example.test/api/metadata", nil)
	check := func(system string, want string, lookups int) {
		t.Helper()
		calls := 0
		p, e := licenseProxy(func(r *http.Request) (*url.URL, error) {
			calls++
			return windowsProxyForURL(r.URL, system, "*.corp.example;<local>")
		})(req)
		val := ""
		if p != nil {
			val = p.String()
		}
		if e != nil || val != want || calls != lookups {
			t.Fatalf("route %q lookups=%d err=%v", val, calls, e)
		}
	}
	check("", "", 1)
	check("127.0.0.1:8080", "http://127.0.0.1:8080", 1)
	check("http=proxy1:80;https=proxy2:81", "http://proxy2:81", 1)
	t.Setenv("NO_PROXY", "license.example.test")
	check("system:80", "", 0)
	t.Setenv("NO_PROXY", "")
	t.Setenv("HTTP_PROXY", "http://env:81")
	check("system:80", "http://env:81", 0)
	t.Setenv("HTTPS_PROXY", "http://tlsenv:82")
	check("system:80", "http://tlsenv:82", 0)
	for _, host := range []string{"node", "host.corp.example"} {
		u, _ := url.Parse("https://" + host)
		v, e := windowsProxyForURL(u, "system:80", "*.corp.example;<local>")
		if e != nil || v != nil {
			t.Fatal("Windows bypass was ignored")
		}
	}
}

// TestUniversalLoginCountsOnlyRejectedProofs 验证「Universal Login Counts Only Rejected Proofs」回归场景。
func TestUniversalLoginCountsOnlyRejectedProofs(t *testing.T) {
	for _, kind := range []string{"dns", "service", "schema", "invalid_login", "429"} {
		t.Run(kind, func(t *testing.T) {
			f := newFixture(t)
			m := Open(f.config)
			m.distribution.AccountMode = "universal"
			requests := 0
			m.config.Client.Transport = transportFunc(func(r *http.Request) (*http.Response, error) {
				requests++
				status := 200
				headers := make(http.Header)
				var out any
				if kind == "dns" {
					return nil, &net.DNSError{Err: "unknown host", Name: "private-user.example"}
				}
				if kind == "service" {
					status = 503
					out = map[string]bool{"unavailable": true}
				} else if kind == "429" {
					status = 429
					headers.Set("Retry-After", "180")
					out = map[string]string{"code": "login_rate_limited"}
				} else if strings.HasSuffix(r.URL.Path, "-challenge") {
					out = map[string]any{"challengeId": strings.Repeat("a", 32), "salt": base64.RawURLEncoding.EncodeToString([]byte(strings.Repeat("s", 24))), "expiresAt": f.now.Unix() + 120, "iterations": 310000}
					if kind == "schema" {
						out = map[string]any{}
					}
				} else {
					status = 403
					out = map[string]string{"code": "invalid_login"}
				}
				b, _ := json.Marshal(out)
				return &http.Response{StatusCode: status, Header: headers, Body: io.NopCloser(strings.NewReader(string(b)))}, nil
			})
			var s Status
			for i := 0; i < 5; i++ {
				s = m.Login("sample1@fixture.com", "Abc123@#")
			}
			if s.Ready || m.loggedIn || m.state.Lease.Payload != "" {
				t.Fatal("access improperly granted")
			}
			if kind == "invalid_login" {
				if requests != 10 || m.state.FailedLogins != 5 || s.RetryLoginRemainingSeconds != 300 {
					t.Fatal("5 wrong proofs must lock login")
				}
				m.Login("sample1@fixture.com", "Abc123@#")
				if requests != 10 {
					t.Fatal("request sent during cooldown")
				}
				return
			}
			if kind == "429" {
				if requests != 1 || s.RetryNetworkRemainingSeconds != 180 {
					t.Fatal("server wait not respected")
				}
			} else if requests != 5 {
				t.Fatalf("expected one request per click: %d", requests)
			}
			if m.state.FailedLogins != 0 || s.RetryLoginRemainingSeconds != 0 {
				t.Fatal("availability failure counted as incorrect password")
			}
			if kind == "dns" && (!strings.Contains(s.Message, "AUTH_DNS") || strings.Contains(s.Message, "private-user")) {
				t.Fatal("unsafe or ambiguous diagnosis")
			}
		})
	}
}

// TestLicenseErrorsPreserveSecurityAndPrivacy 验证「License Errors Preserve Security And Privacy」回归场景。
func TestLicenseErrorsPreserveSecurityAndPrivacy(t *testing.T) {
	for _, v := range []struct {
		e    error
		code string
	}{{context.DeadlineExceeded, "AUTH_TIMEOUT"}, {errLicenseProxy, "AUTH_PROXY"}, {errLicenseRedirect, "AUTH_REDIRECT"}, {errors.New("some-sensitive-proxy-credential"), "AUTH_CONNECT"}} {
		msg := licenseConnectionError(v.e).Error()
		if !strings.Contains(msg, v.code) || strings.Contains(msg, "credential") {
			t.Fatal(msg)
		}
	}
}
