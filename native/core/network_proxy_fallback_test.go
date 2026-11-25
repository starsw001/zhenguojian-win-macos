package core

import (
	"bytes"
	"context"
	"crypto/x509"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"
)

// autoProxyFixtureRouter 返回auto代理Fixture路由。
func autoProxyFixtureRouter(mode string) *proxyRouter {
	address, _ := url.Parse("http://proxy.example:8080")
	return &proxyRouter{mode: mode, manual: address, systemOverride: true,
		system: nativeSystemProxy{HTTP: address.String(), HTTPS: address.String()}}
}

// TestAutoProxyDirectEOFRetriesSameRequestThroughProxyWithoutChangingSettings 验证「Auto Proxy Direct EOF Retries Same Request Through Proxy Without Changing Settings」回归场景。
func TestAutoProxyDirectEOFRetriesSameRequestThroughProxyWithoutChangingSettings(t *testing.T) {
	router := autoProxyFixtureRouter("auto")
	request, _ := http.NewRequest(http.MethodGet, "https://api.example/catalog?signature=a%2Fb", nil)
	request.Header.Set("Referer", "https://site.example/")
	request.Header.Set("Authorization", "synthetic-read-token")
	calls := 0
	var events []diagnosticEvent
	base := sourceFixtureTransport(func(current *http.Request) (*http.Response, error) {
		calls++
		proxy, err := router.proxy(current)
		if err != nil || calls == 1 && proxy != nil || calls == 2 && proxy == nil {
			t.Fatal("unexpected route", calls, proxy, err)
		}
		if current.URL.String() != request.URL.String() || current.Header.Get("Authorization") != "synthetic-read-token" || current.Header.Get("Referer") != request.Header.Get("Referer") {
			t.Fatal("request identity or headers changed")
		}
		if calls == 1 {
			return nil, io.EOF
		}
		return sourceFixtureResponse(current, http.StatusOK, "ok"), nil
	})
	transport := newAutoProxyFallbackTransport(base, router, func(event diagnosticEvent) { events = append(events, event) })
	response, err := transport.RoundTrip(request)
	if err != nil || response.StatusCode != 200 || calls != 2 || len(events) != 2 {
		t.Fatal("proxy fallback did not complete", err, calls, events)
	}
	response.Body.Close()
	if request.Context().Value(directProxyAttemptKey{}) != nil || router.mode != "auto" {
		t.Fatal("fallback mutated the original request or user mode")
	}
}

// TestAutoProxyNeverRetriesAnyHTTPStatus 验证「Auto Proxy Never Retries Any HTTP Status」回归场景。
func TestAutoProxyNeverRetriesAnyHTTPStatus(t *testing.T) {
	for _, status := range []int{200, 401, 402, 403, 404, 429, 500, 503} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			calls := 0
			base := sourceFixtureTransport(func(request *http.Request) (*http.Response, error) {
				calls++
				return sourceFixtureResponse(request, status, "upstream response"), nil
			})
			request, _ := http.NewRequest(http.MethodGet, "https://site.example/", nil)
			response, err := newAutoProxyFallbackTransport(base, autoProxyFixtureRouter("auto"), nil).RoundTrip(request)
			if err != nil || response.StatusCode != status || calls != 1 {
				t.Fatal("HTTP response triggered a route change", err, calls)
			}
			response.Body.Close()
		})
	}
}

// TestAutoProxyExplicitRoutesAndNoProxyNeverFallback 验证「Auto Proxy Explicit Routes And No Proxy Never Fallback」回归场景。
func TestAutoProxyExplicitRoutesAndNoProxyNeverFallback(t *testing.T) {
	for _, mode := range []string{"manual", "direct", "no-system-proxy"} {
		t.Run(mode, func(t *testing.T) {
			router := autoProxyFixtureRouter(mode)
			if mode == "no-system-proxy" {
				router.mode = "auto"
				router.system = nativeSystemProxy{Bypass: []string{"site.example"}}
			}
			calls := 0
			base := sourceFixtureTransport(func(*http.Request) (*http.Response, error) { calls++; return nil, io.EOF })
			request, _ := http.NewRequest(http.MethodGet, "https://site.example/", nil)
			_, err := newAutoProxyFallbackTransport(base, router, nil).RoundTrip(request)
			if err == nil || calls != 1 {
				t.Fatal("explicit route was ignored", err, calls)
			}
		})
	}
}

// TestAutoProxyCertificateAndCancelledRequestsDoNotFallback 验证「Auto Proxy Certificate And Cancelled Requests Do Not Fallback」回归场景。
func TestAutoProxyCertificateAndCancelledRequestsDoNotFallback(t *testing.T) {
	for _, certificate := range []bool{false, true} {
		ctx, cancel := context.WithCancel(context.Background())
		if !certificate {
			cancel()
		}
		calls := 0
		base := sourceFixtureTransport(func(*http.Request) (*http.Response, error) {
			calls++
			if certificate {
				return nil, x509.UnknownAuthorityError{}
			}
			return nil, io.EOF
		})
		request, _ := http.NewRequestWithContext(ctx, http.MethodGet, "https://site.example/", nil)
		_, err := newAutoProxyFallbackTransport(base, autoProxyFixtureRouter("auto"), nil).RoundTrip(request)
		cancel()
		if err == nil || calls != 1 {
			t.Fatal("certificate/cancellation triggered retry", err, calls)
		}
	}
}

// TestAutoProxyReadOnlyPOSTReplaysTheExactBody 验证「Auto Proxy Read Only POST Replays The Exact Body」回归场景。
func TestAutoProxyReadOnlyPOSTReplaysTheExactBody(t *testing.T) {
	for address, payload := range map[string]string{yaguoLoginURL: `{"device":"synthetic-device"}`, yaguoBaseURL + "/v3/search": `{"text":"合成检索"}`} {
		calls := 0
		base := sourceFixtureTransport(func(request *http.Request) (*http.Response, error) {
			calls++
			body, _ := io.ReadAll(request.Body)
			request.Body.Close()
			if string(body) != payload {
				t.Fatal("POST body changed", string(body))
			}
			if calls == 1 {
				return nil, io.EOF
			}
			return sourceFixtureResponse(request, 200, "{}"), nil
		})
		request, _ := http.NewRequest(http.MethodPost, address, bytes.NewBufferString(payload))
		response, err := newAutoProxyFallbackTransport(base, autoProxyFixtureRouter("auto"), nil).RoundTrip(request)
		if err != nil || calls != 2 {
			t.Fatal("read-only request did not replay safely", err, calls)
		}
		response.Body.Close()
	}
}

// TestAutoProxyMutationAndCredentialPOSTNeverReplay 验证「Auto Proxy Mutation And Credential POST Never Replay」回归场景。
func TestAutoProxyMutationAndCredentialPOSTNeverReplay(t *testing.T) {
	for _, test := range []struct{ address, payload, authorization string }{
		{"https://site.example/pay", `{"coins":38}`, ""},
		{yaguoLoginURL, `{"device":"synthetic","password":"must-not-replay"}`, ""},
		{yaguoLoginURL, `{"device":"synthetic"}`, "account-session"},
		{yaguoBaseURL + "/v3/search", `{"text":"synthetic","delete":true}`, ""},
	} {
		calls := 0
		base := sourceFixtureTransport(func(*http.Request) (*http.Response, error) { calls++; return nil, io.EOF })
		request, _ := http.NewRequest(http.MethodPost, test.address, strings.NewReader(test.payload))
		request.Header.Set("Authorization", test.authorization)
		_, err := newAutoProxyFallbackTransport(base, autoProxyFixtureRouter("auto"), nil).RoundTrip(request)
		if err == nil || calls != 1 {
			t.Fatal("non-read-only POST was replayed", calls, err)
		}
	}
}

// TestAutoProxyFailureStopsAfterOneProxyRetry 验证「Auto Proxy Failure Stops After One Proxy Retry」回归场景。
func TestAutoProxyFailureStopsAfterOneProxyRetry(t *testing.T) {
	calls := 0
	base := sourceFixtureTransport(func(*http.Request) (*http.Response, error) { calls++; return nil, io.EOF })
	request, _ := http.NewRequest(http.MethodGet, "https://site.example/", nil)
	_, err := newAutoProxyFallbackTransport(base, autoProxyFixtureRouter("auto"), nil).RoundTrip(request)
	if err == nil || calls != 2 || !strings.Contains(err.Error(), "同址系统代理仍失败") {
		t.Fatal("unbounded or unreported retries", calls, err)
	}
}

// TestAutoProxyBodyReplayFailureDoesNotRetry 验证「Auto Proxy Body Replay Failure Does Not Retry」回归场景。
func TestAutoProxyBodyReplayFailureDoesNotRetry(t *testing.T) {
	calls := 0
	base := sourceFixtureTransport(func(*http.Request) (*http.Response, error) { calls++; return nil, io.EOF })
	request, _ := http.NewRequest(http.MethodGet, "https://site.example/", strings.NewReader("synthetic"))
	request.GetBody = func() (io.ReadCloser, error) { return nil, errors.New("body unavailable") }
	_, err := newAutoProxyFallbackTransport(base, autoProxyFixtureRouter("auto"), nil).RoundTrip(request)
	if err == nil || calls != 1 {
		t.Fatal("an unreplayable body was retried", err, calls)
	}
}

// TestAutoProxyReservesTimeForProxyBeforeCallerDeadline 验证「Auto Proxy Reserves Time For Proxy Before Caller Deadline」回归场景。
func TestAutoProxyReservesTimeForProxyBeforeCallerDeadline(t *testing.T) {
	calls := 0
	base := sourceFixtureTransport(func(request *http.Request) (*http.Response, error) {
		calls++
		if request.Context().Value(directProxyAttemptKey{}) == true {
			<-request.Context().Done()
			return nil, request.Context().Err()
		}
		return sourceFixtureResponse(request, 200, "recovered"), nil
	})
	transport := newAutoProxyFallbackTransport(base, autoProxyFixtureRouter("auto"), nil)
	transport.directTimeout = 10 * time.Millisecond
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	request, _ := http.NewRequestWithContext(ctx, http.MethodGet, "https://site.example/", nil)
	response, err := transport.RoundTrip(request)
	if err != nil || calls != 2 || ctx.Err() != nil {
		t.Fatal("direct attempt exhausted the caller deadline", err, calls, ctx.Err())
	}
	response.Body.Close()
}

// TestAutoProxyHeaderBudgetDoesNotCancelResponseBody 验证「Auto Proxy Header Budget Does Not Cancel Response Body」回归场景。
func TestAutoProxyHeaderBudgetDoesNotCancelResponseBody(t *testing.T) {
	var firstContext context.Context
	base := sourceFixtureTransport(func(request *http.Request) (*http.Response, error) {
		firstContext = request.Context()
		return sourceFixtureResponse(request, 200, "stream-body"), nil
	})
	transport := newAutoProxyFallbackTransport(base, autoProxyFixtureRouter("auto"), nil)
	transport.directTimeout = time.Millisecond
	request, _ := http.NewRequest(http.MethodGet, "https://site.example/", nil)
	response, err := transport.RoundTrip(request)
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(5 * time.Millisecond)
	if firstContext.Err() != nil {
		t.Fatal("response headers canceled an ongoing stream")
	}
	data, err := io.ReadAll(response.Body)
	response.Body.Close()
	if err != nil || string(data) != "stream-body" || firstContext.Err() == nil {
		t.Fatal("body was truncated or context was not released", string(data), err)
	}
}

func TestHongguoProxyModesChooseDirectAutoAndSystem(t *testing.T) {
	address := "https://v26-reading-videocdn302.qznovelvod.com/video.mp4"
	for _, test := range []struct {
		name          string
		mode          string
		globalMode    string
		expectedCalls int
		expectedRoute []bool
	}{
		{name: "direct", mode: "directOnly", globalMode: "auto", expectedCalls: 1, expectedRoute: []bool{false}},
		{name: "auto", mode: "auto", globalMode: "auto", expectedCalls: 2, expectedRoute: []bool{false, true}},
		{name: "old system switches to direct-first", mode: "system", globalMode: "direct", expectedCalls: 2, expectedRoute: []bool{false, true}},
	} {
		t.Run(test.name, func(t *testing.T) {
			router := autoProxyFixtureRouter(test.globalMode)
			router.hongguoMode = test.mode
			calls := 0
			base := sourceFixtureTransport(func(request *http.Request) (*http.Response, error) {
				proxy, err := router.proxy(request)
				if err != nil || (proxy != nil) != test.expectedRoute[calls] {
					t.Fatal("unexpected route", calls+1, proxy, err)
				}
				calls++
				if calls < test.expectedCalls || test.mode == "directOnly" {
					return nil, io.EOF
				}
				return sourceFixtureResponse(request, http.StatusOK, "ok"), nil
			})
			request, _ := http.NewRequest(http.MethodGet, address, nil)
			response, err := newAutoProxyFallbackTransport(base, router, nil).RoundTrip(request)
			if test.mode == "directOnly" {
				if err == nil || calls != 1 {
					t.Fatal("direct mode fell back to proxy", calls, err)
				}
				return
			}
			if err != nil || response == nil || response.StatusCode != http.StatusOK || calls != test.expectedCalls {
				t.Fatal("Hongguo route did not complete", calls, err)
			}
			response.Body.Close()
		})
	}
}

func TestHongguoDirectDoesNotChangeUnrelatedAutoFallback(t *testing.T) {
	router := autoProxyFixtureRouter("auto")
	router.hongguoMode = "directOnly"
	calls := 0
	base := sourceFixtureTransport(func(request *http.Request) (*http.Response, error) {
		calls++
		proxy, err := router.proxy(request)
		if err != nil || calls == 1 && proxy != nil || calls == 2 && proxy == nil {
			t.Fatal("unrelated auto route changed", calls, proxy, err)
		}
		if calls == 1 {
			return nil, io.EOF
		}
		return sourceFixtureResponse(request, http.StatusOK, "ok"), nil
	})
	request, _ := http.NewRequest(http.MethodGet, "https://example.invalid/catalog", nil)
	response, err := newAutoProxyFallbackTransport(base, router, nil).RoundTrip(request)
	if err != nil || response == nil || calls != 2 {
		t.Fatal("unrelated auto fallback stopped working", calls, err)
	}
	response.Body.Close()
}
