package core

import (
	"bytes"
	"context"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptrace"
	"net/url"
	"strings"
	"sync/atomic"
	"time"
)

type directProxyAttemptKey struct{}

type hongguoMetadataReadKey struct{}
type hongguoFallbackProxyKey struct{}
type hongguoStreamPathKey struct{}
type hongguoStreamPath struct {
	address string
	proxy   atomic.Pointer[url.URL]
}

func hongguoRegistrationPost(r *http.Request) bool {
	if r.Method != http.MethodPost || r.GetBody == nil || r.URL.User != nil ||
		r.Header.Get("Cookie") != "" || r.Header.Get("Authorization") != "" {
		return false
	}
	return hongguoRequestAt(r, hongguoPlaybackRegisterURL) || hongguoRequestAt(r, hongguoDeviceRegisterURL)
}

func hongguoRequestAt(r *http.Request, endpoint string) bool {
	u, _ := url.Parse(endpoint)
	return r.URL.Scheme == "https" && strings.EqualFold(r.URL.Host, u.Host) && r.URL.EscapedPath() == u.EscapedPath() && r.URL.User == nil
}

type autoProxyFallbackTransport struct {
	base          http.RoundTripper
	router        *proxyRouter
	record        func(diagnosticEvent)
	directTimeout time.Duration
}

// newAutoProxyFallbackTransport 创建Auto代理回退传输层。
func newAutoProxyFallbackTransport(base http.RoundTripper, router *proxyRouter, record func(diagnosticEvent)) *autoProxyFallbackTransport {
	return &autoProxyFallbackTransport{base: base, router: router, record: record, directTimeout: 8 * time.Second}
}

type routeResponseBody struct {
	io.ReadCloser
	cancel context.CancelFunc
}

// Close 关闭响应正文并释放其持有的资源。
func (body *routeResponseBody) Close() error {
	defer body.cancel()
	return body.ReadCloser.Close()
}

// canRetry 判断是否允许重试。
func (transport *autoProxyFallbackTransport) canRetry(request *http.Request) bool {
	if transport.router == nil || request.Context().Value(directProxyAttemptKey{}) == true || (!replayableProviderRead(request) && !hongguoRegistrationPost(request)) {
		return false
	}
	mode, _ := transport.router.requestMode(request)
	if mode != "auto" {
		return false
	}
	proxy, err := transport.router.proxy(request)
	return err == nil && proxy != nil
}

// directTransportFailure 判断直连失败是否属于可以尝试代理回退的连接错误。
func directTransportFailure(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.Canceled) {
		return false
	}
	var unknownAuthority x509.UnknownAuthorityError
	var hostnameError x509.HostnameError
	var invalidCertificate x509.CertificateInvalidError
	if errors.As(err, &unknownAuthority) || errors.As(err, &hostnameError) || errors.As(err, &invalidCertificate) {
		return false
	}
	if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
		return true
	}
	var networkError net.Error
	if errors.As(err, &networkError) && networkError.Timeout() {
		return true
	}
	var operationError *net.OpError
	if errors.As(err, &operationError) {
		return true
	}
	text := strings.ToLower(err.Error())
	return strings.HasSuffix(text, ": eof") || strings.Contains(text, "unexpected eof") ||
		strings.Contains(text, "tls handshake timeout") || strings.Contains(text, "connection reset by peer") ||
		strings.Contains(text, "connection was forcibly closed") || strings.Contains(text, "connection refused") ||
		strings.Contains(text, "no such host") || strings.Contains(text, "network is unreachable")
}

// replayableProviderRead 判断站源读取请求是否可以安全重放。
func replayableProviderRead(request *http.Request) bool {
	if request.Method == http.MethodGet || request.Method == http.MethodHead {
		return request.Body == nil || request.Body == http.NoBody || request.GetBody != nil
	}
	if request.Method != http.MethodPost || request.GetBody == nil {
		return false
	}
	if known, _ := request.Context().Value(huangguoPlaybackReadKey{}).(bool); known {
		return replayableHuangguoPlaybackRead(request)
	}
	if known, _ := request.Context().Value(hongguoMetadataReadKey{}).(bool); known {
		return request.Header.Get("Authorization") == "" && request.Header.Get("Proxy-Authorization") == "" &&
			(hongguoRequestAt(request, hongguoAppBaseURL+hongguoVideoModelPath) ||
				hongguoRequestAt(request, hongguoMultiVideoAPI) || hongguoRequestAt(request, hongguoMobileV5API))
	}
	if request.URL.RawQuery != "" {
		return false
	}
	field := ""
	for address, candidate := range map[string]string{yaguoLoginURL: "device", yaguoBaseURL + "/v3/search": "text"} {
		expected, _ := url.Parse(address)
		if request.URL.Scheme == expected.Scheme && strings.EqualFold(request.URL.Host, expected.Host) && request.URL.Path == expected.Path {
			field = candidate
			break
		}
	}
	if field == "" || (field == "device" && (request.Header.Get("Authorization") != "" || request.Header.Get("Cookie") != "")) {
		return false
	}
	body, err := request.GetBody()
	if err != nil {
		return false
	}
	defer body.Close()
	data, err := io.ReadAll(io.LimitReader(body, 4097))
	if err != nil || len(data) > 4096 {
		return false
	}
	var values map[string]any
	if json.Unmarshal(data, &values) != nil || len(values) != 1 {
		return false
	}
	value, ok := values[field].(string)
	return ok && strings.TrimSpace(value) != "" && len(value) <= 512
}

// RoundTrip 按auto代理回退传输层策略执行单次 HTTP 请求并返回响应或错误。
func (transport *autoProxyFallbackTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	hongguo := transport.router != nil && hongguoProxyHost(request.URL.Hostname())
	if pinned, _ := request.Context().Value(hongguoFallbackProxyKey{}).(*url.URL); hongguo && pinned != nil && hongguoRequestAt(request, hongguoMobileV5API) {
		return transport.base.RoundTrip(request)
	}
	eligible := transport.canRetry(request)
	bootstrap := eligible && hongguoRegistrationPost(request)
	if path, _ := request.Context().Value(hongguoStreamPathKey{}).(*hongguoStreamPath); eligible && hongguo && path != nil &&
		request.Method == http.MethodGet && request.URL.String() == path.address {
		if saved := path.proxy.Load(); saved != nil {
			proxy, _ := transport.router.proxy(request)
			if proxy != nil && proxy.String() == saved.String() {
				proxied := request.WithContext(context.WithValue(request.Context(), hongguoFallbackProxyKey{}, proxy))
				res, err := transport.base.RoundTrip(proxied)
				if err == nil || res != nil || request.Context().Err() != nil || !directTransportFailure(err) {
					return res, err
				}
				path.proxy.CompareAndSwap(saved, nil)
				return transport.base.RoundTrip(request.WithContext(context.WithValue(request.Context(), directProxyAttemptKey{}, true)))
			}
			path.proxy.CompareAndSwap(saved, nil)
		}
	}
	first := request
	var timer *time.Timer
	var cancel context.CancelFunc
	var expired atomic.Bool
	var dialing, acquired atomic.Bool
	if eligible {
		attemptContext, stop := context.WithCancel(request.Context())
		cancel = stop
		first = request.Clone(context.WithValue(attemptContext, directProxyAttemptKey{}, true))
		if bootstrap {
			trace := &httptrace.ClientTrace{
				DNSStart:     func(httptrace.DNSStartInfo) { dialing.Store(true) },
				ConnectStart: func(string, string) { dialing.Store(true) },
				GotConn:      func(httptrace.GotConnInfo) { acquired.Store(true) },
			}
			first = first.WithContext(httptrace.WithClientTrace(first.Context(), trace))
		}
		budget := transport.directTimeout
		if deadline, ok := request.Context().Deadline(); ok && time.Until(deadline)/2 < budget {
			budget = time.Until(deadline) / 2
		}
		timer = time.AfterFunc(budget, func() { expired.Store(true); stop() })
	} else if hongguo {
		first = request.WithContext(context.WithValue(request.Context(), directProxyAttemptKey{}, true))
	}
	response, err := transport.base.RoundTrip(first)
	if eligible && hongguo && !bootstrap && err == nil && response != nil && response.Body != nil &&
		request.Method != http.MethodHead && (response.StatusCode == http.StatusOK || response.StatusCode == http.StatusPartialContent) &&
		response.ContentLength > 0 && response.Header.Get("Retry-After") == "" && !nativeMediaAccessStopsSwitch(response, nil) {
		var prefix [1]byte
		if _, readErr := io.ReadFull(response.Body, prefix[:]); readErr != nil {
			response.Body.Close()
			response, err = nil, readErr
		} else {
			response.Body = struct {
				io.Reader
				io.Closer
			}{io.MultiReader(bytes.NewReader(prefix[:]), response.Body), response.Body}
		}
	}
	if timer != nil {
		timer.Stop()
		if err == nil && response != nil && response.Body != nil {
			response.Body = &routeResponseBody{ReadCloser: response.Body, cancel: cancel}
		} else {
			cancel()
		}
	}
	if err == nil || response != nil || request.Context().Err() != nil || !eligible || (!expired.Load() && !directTransportFailure(err)) {
		return response, err
	}
	if bootstrap {
		var op *net.OpError
		var dns *net.DNSError
		if acquired.Load() || !(errors.As(err, &dns) || errors.As(err, &op) && op.Op == "dial" || expired.Load() && dialing.Load()) {
			return nil, err
		}
	}
	if !transport.canRetry(request) {
		return nil, err
	}
	if expired.Load() {
		err = fmt.Errorf("直连等待超时：%w", context.DeadlineExceeded)
	}
	retry := request.Clone(request.Context())
	if hongguo {
		proxy, proxyErr := transport.router.proxy(request)
		if proxyErr != nil || proxy == nil {
			return nil, err
		}
		if state, _ := request.Context().Value(hongguoEpisodeEmptyKey{}).(*hongguoEpisodeEmptyRead); state != nil {
			state.proxyTried.Store(true)
		}
		retry = retry.WithContext(context.WithValue(retry.Context(), hongguoFallbackProxyKey{}, proxy))
	}
	if request.Body != nil && request.Body != http.NoBody {
		body, bodyErr := request.GetBody()
		if bodyErr != nil {
			return nil, err
		}
		retry.Body = body
	}
	label := "系统代理"
	if hongguo {
		label = "已配置的代理"
	}
	if transport.record != nil {
		transport.record(diagnosticEvent{Event: "network.auto_direct_transport_failed", Level: "warning", Host: request.URL.Hostname(), Attempt: 1,
			Message: fmt.Sprintf("自动模式直连失败，同一地址改走%s：%v", label, publicError(err))})
	}
	recovered, proxyErr := transport.base.RoundTrip(retry)
	if hongguo && recovered != nil && recovered.Request == nil {
		recovered.Request = retry
	}
	if transport.record != nil {
		event := diagnosticEvent{Event: "network.auto_proxy_result", Level: "info", Host: request.URL.Hostname(), Attempt: 2, Message: "自动模式同一地址" + label + "请求完成；未修改网络设置"}
		if recovered != nil {
			event.HTTPStatus = recovered.StatusCode
		}
		if proxyErr != nil {
			event.Level, event.Message = "warning", fmt.Sprintf("自动模式%s请求仍失败：%v", label, publicError(proxyErr))
		}
		transport.record(event)
	}
	if proxyErr != nil {
		return recovered, fmt.Errorf("自动模式直连失败（%v），同址%s仍失败：%w", publicError(err), label, publicError(proxyErr))
	}
	return recovered, nil
}

// CloseIdleConnections 关闭当前传输层持有的空闲连接。
func (transport *autoProxyFallbackTransport) CloseIdleConnections() {
	if closer, ok := transport.base.(interface{ CloseIdleConnections() }); ok {
		closer.CloseIdleConnections()
	}
}
