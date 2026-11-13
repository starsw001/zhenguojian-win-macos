package core

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync/atomic"
	"time"
)

type directProxyRetryKey struct{}

type hongguoMetadataReadKey struct{}

type autoProxyFallbackTransport struct {
	base         http.RoundTripper
	router       *proxyRouter
	record       func(diagnosticEvent)
	proxyTimeout time.Duration
}

// newAutoProxyFallbackTransport 创建Auto代理回退传输层。
func newAutoProxyFallbackTransport(base http.RoundTripper, router *proxyRouter, record func(diagnosticEvent)) *autoProxyFallbackTransport {
	return &autoProxyFallbackTransport{base: base, router: router, record: record, proxyTimeout: 8 * time.Second}
}

type proxyResponseBody struct {
	io.ReadCloser
	cancel context.CancelFunc
}

// Close 关闭代理响应正文并释放其持有的资源。
func (body *proxyResponseBody) Close() error {
	defer body.cancel()
	return body.ReadCloser.Close()
}

// canRetry 判断是否允许重试。
func (transport *autoProxyFallbackTransport) canRetry(request *http.Request) bool {
	if transport.router == nil || request.Context().Value(directProxyRetryKey{}) == true || !replayableProviderRead(request) {
		return false
	}
	transport.router.mu.RLock()
	mode := transport.router.mode
	transport.router.mu.RUnlock()
	if mode == "manual" || mode == "direct" {
		return false
	}
	proxy, err := transport.router.proxy(request)
	return err == nil && proxy != nil
}

// proxyTransportFailure 判断失败是否属于可以尝试代理回退的连接错误。
func proxyTransportFailure(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
		return true
	}
	var networkError net.Error
	if errors.As(err, &networkError) && networkError.Timeout() {
		return true
	}
	text := strings.ToLower(err.Error())
	return strings.HasSuffix(text, ": eof") || strings.Contains(text, "unexpected eof") ||
		strings.Contains(text, "tls handshake timeout") || strings.Contains(text, "connection reset by peer")
}

// replayableProviderRead 判断站源读取请求是否可以安全重放。
func replayableProviderRead(request *http.Request) bool {
	if request.Method == http.MethodGet || request.Method == http.MethodHead {
		return request.Body == nil || request.Body == http.NoBody || request.GetBody != nil
	}
	if request.Method != http.MethodPost || request.GetBody == nil {
		return false
	}
	if known, _ := request.Context().Value(hongguoMetadataReadKey{}).(bool); known {
		address, _ := url.Parse(hongguoMultiVideoAPI)
		return request.URL.Scheme == address.Scheme && request.URL.Host == address.Host &&
			request.URL.Path == address.Path && request.URL.User == nil &&
			request.Header.Get("Authorization") == "" && request.Header.Get("Cookie") == ""
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
	eligible := transport.canRetry(request)
	first := request
	var timer *time.Timer
	var cancel context.CancelFunc
	var expired atomic.Bool
	if eligible {
		attemptContext, stop := context.WithCancel(request.Context())
		cancel = stop
		first = request.Clone(attemptContext)
		budget := transport.proxyTimeout
		if deadline, ok := request.Context().Deadline(); ok && time.Until(deadline)/2 < budget {
			budget = time.Until(deadline) / 2
		}
		timer = time.AfterFunc(budget, func() { expired.Store(true); stop() })
	}
	response, err := transport.base.RoundTrip(first)
	if timer != nil {
		timer.Stop()
		if err == nil && response != nil && response.Body != nil {
			response.Body = &proxyResponseBody{ReadCloser: response.Body, cancel: cancel}
		} else {
			cancel()
		}
	}
	if err == nil || response != nil || request.Context().Err() != nil || !eligible || (!expired.Load() && !proxyTransportFailure(err)) {
		return response, err
	}
	if !transport.canRetry(request) {
		return nil, err
	}
	if expired.Load() {
		err = fmt.Errorf("代理连接等待超时：%w", context.DeadlineExceeded)
	}
	retry := request.Clone(context.WithValue(request.Context(), directProxyRetryKey{}, true))
	if request.Body != nil && request.Body != http.NoBody {
		body, bodyErr := request.GetBody()
		if bodyErr != nil {
			return nil, err
		}
		retry.Body = body
	}
	if transport.record != nil {
		transport.record(diagnosticEvent{Event: "network.auto_proxy_transport_failed", Level: "warning", Host: request.URL.Hostname(), Attempt: 1,
			Message: fmt.Sprintf("自动代理传输失败，同一地址直连复核：%v", publicError(err))})
	}
	recovered, directErr := transport.base.RoundTrip(retry)
	if transport.record != nil {
		event := diagnosticEvent{Event: "network.auto_proxy_direct_result", Level: "info", Host: request.URL.Hostname(), Attempt: 2, Message: "自动模式同一地址直连复核完成；未修改网络设置"}
		if recovered != nil {
			event.HTTPStatus = recovered.StatusCode
		}
		if directErr != nil {
			event.Level, event.Message = "warning", fmt.Sprintf("自动模式直连复核仍失败：%v", publicError(directErr))
		}
		transport.record(event)
	}
	if directErr != nil {
		return recovered, fmt.Errorf("自动代理传输失败（%v），同址直连仍失败：%w", publicError(err), publicError(directErr))
	}
	return recovered, nil
}

// CloseIdleConnections 关闭当前传输层持有的空闲连接。
func (transport *autoProxyFallbackTransport) CloseIdleConnections() {
	if closer, ok := transport.base.(interface{ CloseIdleConnections() }); ok {
		closer.CloseIdleConnections()
	}
}
