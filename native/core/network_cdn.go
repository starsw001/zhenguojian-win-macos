package core

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

type cdnRoute struct {
	address string
	expires time.Time
}

type cdnRouteContextKey struct{}

type cdnRouteContextValue struct {
	host    string
	address string
}

func cdnRouteContext(ctx context.Context, host, address string) context.Context {
	return context.WithValue(ctx, cdnRouteContextKey{}, cdnRouteContextValue{host: strings.ToLower(strings.TrimSuffix(host, ".")), address: address})
}

type cdnTransport struct {
	base       *http.Transport
	resolver   *dnsResolver
	mu         sync.Mutex
	transports map[string]*http.Transport
	preferred  map[string]cdnRoute
}

// newCDNTransport 创建CDN传输层。
func newCDNTransport(base *http.Transport, resolver *dnsResolver) *cdnTransport {
	return &cdnTransport{base: base, resolver: resolver, transports: map[string]*http.Transport{}, preferred: map[string]cdnRoute{}}
}

// RoundTrip 按CDN传输层策略执行单次 HTTP 请求并返回响应或错误。
func (transport *cdnTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	if request.URL.Scheme != "https" || (!protectedCDNHost(request.URL.Hostname()) && request.Context().Value(nativeCoverNetworkKey{}) != true) || (request.Method != http.MethodGet && request.Method != http.MethodHead) {
		return transport.base.RoundTrip(request)
	}
	var proxyURL *url.URL
	var err error
	if transport.base.Proxy != nil {
		proxyURL, err = transport.base.Proxy(request)
		if err != nil {
			return nil, err
		}
	}
	routeKey := request.URL.Host
	if proxyURL != nil {
		routeKey += "|" + proxyURL.String()
	}
	if pinned, ok := request.Context().Value(cdnRouteContextKey{}).(cdnRouteContextValue); ok &&
		pinned.host == strings.ToLower(strings.TrimSuffix(request.URL.Hostname(), ".")) {
		if err := validateCDNAddresses([]string{pinned.address}); err != nil {
			return nil, fmt.Errorf("CDN 指定地址无效: %w", err)
		}
		return transport.roundTripAddress(request, proxyURL, routeKey+"|explicit", pinned.address)
	}
	transport.mu.Lock()
	preferred := transport.preferred[routeKey]
	transport.mu.Unlock()
	tried := map[string]bool{}
	var response *http.Response
	if preferred.address != "" && time.Now().Before(preferred.expires) {
		tried[preferred.address] = true
		response, err = transport.roundTripAddress(request, proxyURL, routeKey, preferred.address)
		if err == nil {
			return response, nil
		}
	} else {
		response, err = transport.base.RoundTrip(request)
		if err == nil {
			return response, nil
		}
	}
	if request.Context().Err() != nil {
		return nil, request.Context().Err()
	}
	lastErr := err
	subnets := []string{dnsAlternateSubnet, ""}
	for _, subnet := range subnets {
		entry, lookupErr := transport.resolver.lookupEntry(request.Context(), request.URL.Hostname(), subnet)
		if lookupErr != nil {
			if request.Context().Err() != nil {
				return nil, request.Context().Err()
			}
			continue
		}
		for _, address := range entry.addresses {
			if tried[address] || len(tried) >= 4 {
				continue
			}
			tried[address] = true
			response, err = transport.roundTripAddress(request, proxyURL, routeKey, address)
			if err == nil {
				transport.mu.Lock()
				transport.preferred[routeKey] = cdnRoute{address: address, expires: entry.expires}
				transport.mu.Unlock()
				return response, nil
			}
			if request.Context().Err() != nil {
				return nil, request.Context().Err()
			}
			lastErr = err
		}
	}
	return nil, fmt.Errorf("资源 %s 连接失败（已尝试备用 DNS 地址）: %w", request.URL.Hostname(), publicError(lastErr))
}

// roundTripAddress 执行请求处理地址。
func (transport *cdnTransport) roundTripAddress(request *http.Request, proxyURL *url.URL, routeKey, address string) (*http.Response, error) {
	pinned := transport.pinnedTransport(request.URL.Hostname(), proxyURL, routeKey, address)
	upstream := request.Clone(request.Context())
	upstream.Host = request.Host
	if upstream.Host == "" {
		upstream.Host = request.URL.Host
	}
	upstream.URL.Host = address
	if port := request.URL.Port(); port != "" {
		upstream.URL.Host = net.JoinHostPort(address, port)
	}
	response, err := pinned.RoundTrip(upstream)
	if response != nil {
		response.Request = request
	}
	return response, err
}

// pinnedTransport 返回固定传输层。
func (transport *cdnTransport) pinnedTransport(host string, proxyURL *url.URL, routeKey, address string) *http.Transport {
	transport.mu.Lock()
	defer transport.mu.Unlock()
	key := routeKey + "|" + address
	if existing := transport.transports[key]; existing != nil {
		return existing
	}
	pinned := transport.base.Clone()
	if pinned.TLSClientConfig == nil {
		pinned.TLSClientConfig = &tls.Config{}
	}
	pinned.TLSClientConfig.ServerName = host
	pinned.Proxy = http.ProxyURL(proxyURL)
	if proxyURL != nil && proxyURL.Scheme == "https" {
		plainProxy := *proxyURL
		plainProxy.Scheme = "http"
		if plainProxy.Port() == "" {
			plainProxy.Host = net.JoinHostPort(proxyURL.Hostname(), "443")
		}
		pinned.Proxy = http.ProxyURL(&plainProxy)
		pinned.DialContext = transport.tlsProxyDialer(proxyURL.Hostname())
	}
	if len(transport.transports) >= 32 {
		for expiredKey, expiredTransport := range transport.transports {
			expiredTransport.CloseIdleConnections()
			delete(transport.transports, expiredKey)
			break
		}
	}
	transport.transports[key] = pinned
	return pinned
}

// tlsProxyDialer 构造 TLS 连接使用的代理拨号器。
func (transport *cdnTransport) tlsProxyDialer(host string) func(context.Context, string, string) (net.Conn, error) {
	return func(ctx context.Context, network, address string) (net.Conn, error) {
		dial := transport.base.DialContext
		if dial == nil {
			dialer := &net.Dialer{Timeout: 10 * time.Second}
			dial = dialer.DialContext
		}
		connection, err := dial(ctx, network, address)
		if err != nil {
			return nil, err
		}
		config := &tls.Config{}
		if transport.base.TLSClientConfig != nil {
			config = transport.base.TLSClientConfig.Clone()
		}
		config.ServerName = host
		config.NextProtos = []string{"http/1.1"}
		secured := tls.Client(connection, config)
		if transport.base.TLSHandshakeTimeout > 0 {
			var cancel context.CancelFunc
			ctx, cancel = context.WithTimeout(ctx, transport.base.TLSHandshakeTimeout)
			defer cancel()
		}
		if err := secured.HandshakeContext(ctx); err != nil {
			connection.Close()
			return nil, err
		}
		return secured, nil
	}
}

// CloseIdleConnections 关闭当前传输层持有的空闲连接。
func (transport *cdnTransport) CloseIdleConnections() {
	transport.base.CloseIdleConnections()
	transport.resolver.client.CloseIdleConnections()
	transport.mu.Lock()
	defer transport.mu.Unlock()
	for key, pinned := range transport.transports {
		pinned.CloseIdleConnections()
		delete(transport.transports, key)
	}
	transport.preferred = map[string]cdnRoute{}
}
