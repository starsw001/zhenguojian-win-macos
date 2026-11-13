package secureupdate

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

type updateLimit struct {
	code  updateHTTPStatus
	until time.Time
}

// Unwrap 返回底层错误，供 errors.Is 和 errors.As 继续匹配。
func (e *updateLimit) Unwrap() error { return e.code }

// Error 返回当前错误的可显示文本。
func (e *updateLimit) Error() string {
	s := int(time.Until(e.until).Seconds())
	if s < 1 {
		s = 1
	}
	return fmt.Sprintf("GitHub 要求暂缓请求（HTTP %d），请在约 %d 分 %02d 秒后再试", int(e.code), s/60, s%60)
}

// backoff 根据响应状态与 Retry-After 计算下次允许请求的时间。
func backoff(code int, h http.Header, now time.Time) time.Time {
	var until time.Time
	if code != 403 && code != 429 && code != 503 {
		return until
	}
	raw := strings.TrimSpace(h.Get("Retry-After"))
	if n, e := strconv.ParseInt(raw, 10, 32); e == nil && n > 0 {
		until = now.Add(time.Duration(n) * time.Second)
	} else if dt, e := http.ParseTime(raw); e == nil && dt.After(now) {
		until = dt
	}
	if h.Get("X-RateLimit-Remaining") == "0" {
		if n, e := strconv.ParseInt(h.Get("X-RateLimit-Reset"), 10, 64); e == nil {
			dt := time.Unix(n, 0)
			if dt.After(now) && dt.After(until) {
				until = dt
			}
		}
	}
	if until.IsZero() && code == 429 {
		until = now.Add(time.Minute)
	}
	return until
}

type waitTransport struct {
	base    http.RoundTripper
	mu      sync.Mutex
	blocked map[string]*updateLimit
}

// NewPublicClient 创建更新元数据请求使用的公共 HTTP 客户端。
func NewPublicClient(base http.RoundTripper) *http.Client {
	return &http.Client{Transport: &waitTransport{base: base, blocked: map[string]*updateLimit{}}}
}

// RoundTrip 按更新限流传输层策略执行单次 HTTP 请求并返回响应或错误。
func (t *waitTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	host := strings.ToLower(r.URL.Hostname())
	now := time.Now()
	t.mu.Lock()
	wait := t.blocked[host]
	if wait != nil && now.Before(wait.until) {
		t.mu.Unlock()
		return nil, wait
	}
	delete(t.blocked, host)
	t.mu.Unlock()
	resp, e := t.base.RoundTrip(r)
	if e == nil {
		if until := backoff(resp.StatusCode, resp.Header, time.Now()); !until.IsZero() {
			t.mu.Lock()
			if old := t.blocked[host]; old == nil || old.until.Before(until) {
				t.blocked[host] = &updateLimit{updateHTTPStatus(resp.StatusCode), until}
			}
			t.mu.Unlock()
		}
	}
	return resp, e
}

// CloseIdleConnections 关闭当前传输层持有的空闲连接。
func (t *waitTransport) CloseIdleConnections() {
	if c, ok := t.base.(interface{ CloseIdleConnections() }); ok {
		c.CloseIdleConnections()
	}
}
