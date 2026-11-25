package core

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

const (
	soraniSiteBaseURL = "https://www.sorani.net"
	soraniAPIBaseURL  = "https://api.sorani.cc/sorani-cms/api"
	soraniUserAgent   = "Mozilla/5.0 (Linux; Android 13) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/126.0.0.0 Mobile Safari/537.36"
)

type soraniAPIClient struct {
	downloader *Downloader
	base       string
	site       string
	once       sync.Once
	authOnce   sync.Once
	authMu     sync.Mutex
	refreshMu  sync.Mutex
	auth       *soraniAccountSession
	authEpoch  uint64
}

// soraniClient 取得Sorani请求使用的客户端。
func (d *Downloader) soraniClient() *soraniAPIClient {
	d.soraniOnce.Do(func() {
		d.sorani = &soraniAPIClient{
			downloader: d,
			base:       strings.TrimRight(firstNonEmpty(d.cfg.SoraniAPIURL, soraniAPIBaseURL), "/"),
			site:       d.providerBaseURL(sourceSorani),
		}
	})
	return d.sorani
}

// get 从Sorani API 客户端中取得指定条目。
func (client *soraniAPIClient) get(ctx context.Context, route string, query url.Values, limit int64) (any, error) {
	session := client.accountSnapshot()
	value, err := client.request(ctx, http.MethodGet, route, query, limit, session)
	if errors.Is(err, errSoraniLoginRequired) && session != nil {
		session, err = client.refreshAccount(ctx, session)
		if err != nil {
			return nil, err
		}
		return client.request(ctx, http.MethodGet, route, query, limit, session)
	}
	return value, err
}

func (client *soraniAPIClient) request(ctx context.Context, method, route string, query url.Values, limit int64, session *soraniAccountSession) (any, error) {
	if limit <= 0 {
		limit = 4 << 20
	}
	base, err := url.Parse(client.base)
	if err != nil || !isProviderHTTPMediaURL(client.base) || base.User != nil || base.RawQuery != "" || base.Fragment != "" {
		return nil, errors.New("青空次元接口地址无效")
	}
	timeout := 15 * time.Second
	background, _ := ctx.Value(backgroundCatalogKey{}).(bool)
	refresh, _ := ctx.Value(sourceRuleRefreshKey{}).(bool)
	if background || refresh {
		timeout = 30 * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	address := client.base + route
	if len(query) > 0 {
		address += "?" + query.Encode()
	}
	request, err := http.NewRequestWithContext(ctx, method, address, nil)
	if err != nil {
		return nil, errors.New("无法创建青空次元请求")
	}
	request.Header.Set("User-Agent", soraniUserAgent)
	request.Header.Set("Accept", "application/json, text/plain, */*")
	request.Header.Set("Accept-Language", "zh-CN,zh;q=0.9")
	request.Header.Set("Referer", client.site+"/")
	request.Header.Set("Origin", client.site)

	d := client.downloader
	if d.limiter != nil {
		release, err := d.limiter.acquire(ctx, request)
		if err != nil {
			return nil, err
		}
		defer release()
	}
	// 与黄剧客户端一致：接口请求不携带共享 Cookie Jar，且只允许同源重定向。
	transport := *d.client
	transport.Jar = nil
	if session != nil {
		if !client.accountOriginAllowed() {
			return nil, errors.New("青空登录状态只能用于原站 HTTPS 接口")
		}
		request.Header.Set("Authorization", "Bearer "+session.Token)
		if session.UserAgent != "" {
			request.Header.Set("User-Agent", session.UserAgent)
		}
		request.Header.Set("Referer", soraniSiteBaseURL+"/")
		request.Header.Set("Origin", soraniSiteBaseURL)
		transport.Jar = session.jar
	}
	previousRedirect := transport.CheckRedirect
	transport.CheckRedirect = func(next *http.Request, via []*http.Request) error {
		if session != nil && method != http.MethodGet {
			return errors.New("青空账号请求不接受重定向，请重新登录")
		}
		if len(via) >= 5 || providerMediaOrigin(next.URL) != providerMediaOrigin(base) || session != nil && !strings.HasPrefix(next.URL.Path, "/sorani-cms/api/") {
			return errors.New("青空次元接口重定向地址异常")
		}
		if previousRedirect != nil {
			return previousRedirect(next, via)
		}
		return nil
	}
	if fresh, _ := ctx.Value(providerTextNoCacheKey{}).(bool); fresh {
		request.Header.Set("Cache-Control", "no-cache")
		request.Header.Set("Pragma", "no-cache")
	}
	var data []byte
	for attempt := 0; attempt < 2; attempt++ {
		response, err := transport.Do(request.Clone(ctx))
		if err != nil {
			return nil, fmt.Errorf("青空次元连接失败：%w", publicError(err))
		}
		var readErr error
		data, readErr = io.ReadAll(io.LimitReader(response.Body, limit+1))
		response.Body.Close()
		if d.limiter != nil {
			d.limiter.observe(request, response)
		}
		observeSourceResponse(ctx, response)
		if response.StatusCode == http.StatusUnauthorized {
			return nil, errSoraniLoginRequired
		}
		if response.StatusCode == http.StatusForbidden && (route == "/auth/profile" || route == "/auth/refresh") {
			return nil, errSoraniLoginRequired
		}
		blocked := catalogResponseBlockReason(response, data) != ""
		if response.StatusCode < 200 || response.StatusCode >= 300 || blocked {
			return nil, d.catalogResponseError(request, response, data)
		}
		if int64(len(data)) > limit {
			return nil, fmt.Errorf("青空次元响应超过读取上限（%d字节）", limit)
		}
		if readErr == nil {
			break
		}
		var networkError net.Error
		transient := errors.Is(readErr, io.ErrUnexpectedEOF) || errors.As(readErr, &networkError)
		if method != http.MethodGet || attempt > 0 || !transient || ctx.Err() != nil {
			return nil, fmt.Errorf("青空次元响应读取中断（已读%d字节）：%w", len(data), publicError(readErr))
		}
		select {
		case <-ctx.Done():
			return nil, fmt.Errorf("青空次元响应读取超时：%w", ctx.Err())
		case <-time.After(250 * time.Millisecond):
		}
	}
	var decoded any
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	if err := decoder.Decode(&decoded); err != nil {
		return nil, errors.New("青空次元返回的数据格式无效")
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return nil, errors.New("青空次元返回的数据格式无效")
	}
	// 上游统一用 code=200 表示成功；非 200 一律视为业务错误。
	if row, ok := decoded.(map[string]any); ok {
		code := nativeText(row["code"])
		if code == "401" || soraniNeedsLogin(mapString(row, "message", "msg")) {
			return nil, errSoraniLoginRequired
		}
		if payload, ok := row["data"].(map[string]any); ok && soraniNeedsLogin(mapString(payload, "message", "msg")) {
			return nil, errSoraniLoginRequired
		}
		if code := nativeText(row["code"]); code != "" && code != "200" {
			if session != nil {
				return nil, errors.New("青空账号接口未完成操作，请在原站确认账号状态后重试")
			}
			message := mapString(row, "message", "msg")
			if message == "" {
				message = "上游返回错误码 " + code
			}
			return nil, errors.New("青空次元接口错误：" + truncate(message, 120))
		}
	}
	return decoded, nil
}
