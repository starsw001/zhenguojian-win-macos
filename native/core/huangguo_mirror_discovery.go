package core

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"golang.org/x/net/html"
)

type huangguoProbeOriginKey struct{}

type huangguoFetchError struct {
	err         error
	recoverable bool
	blocked     bool
}

// Error 返回当前错误的可显示文本。
func (e *huangguoFetchError) Error() string { return e.err.Error() }

// Unwrap 返回底层错误，供 errors.Is 和 errors.As 继续匹配。
func (e *huangguoFetchError) Unwrap() error { return e.err }

// recoverableHuangguoError 判断recoverable黄果错误是否满足当前规则。
func recoverableHuangguoError(err error) bool {
	var failure *huangguoFetchError
	return errors.As(err, &failure) && failure.recoverable && !failure.blocked
}

// fetchHuangguoDirectText 获取黄果Direct文本。
func (d *Downloader) fetchHuangguoDirectText(ctx context.Context, address, referer string) (string, string, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, address, nil)
	if err != nil {
		return "", "", err
	}
	request.Header.Set("User-Agent", userAgent)
	request.Header.Set("Accept", "text/html,application/json;q=0.9,*/*;q=0.5")
	request.Header.Set("Accept-Language", "zh-CN,zh;q=0.9")
	if noCache, _ := ctx.Value(providerTextNoCacheKey{}).(bool); noCache {
		request.Header.Set("Cache-Control", "no-cache")
		request.Header.Set("Pragma", "no-cache")
	}
	if referer != "" {
		request.Header.Set("Referer", providerRefererForURL(address, referer))
	}
	timeout := 8 * time.Second
	if playback, _ := ctx.Value(huangguoPlaybackPageReadKey{}).(bool); playback {
		timeout = 30 * time.Second
	}
	response, err := d.doCatalogRequestWithTimeout(request, timeout)
	if err != nil {
		var backoff *requestBackoff
		blocked := errors.As(err, &backoff)
		return "", "", &huangguoFetchError{err: err, recoverable: ctx.Err() == nil && !blocked, blocked: blocked}
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, 2*1024*1024+1))
	response.Body.Close()
	if err != nil {
		return "", "", &huangguoFetchError{err: err, recoverable: ctx.Err() == nil}
	}
	if len(body) > 2*1024*1024 {
		return "", "", errors.New("黄果页面超过2MiB上限，未继续读取")
	}
	reason := catalogResponseBlockReason(response, body)
	if response.StatusCode < 200 || response.StatusCode >= 300 || reason != "" {
		blocked := reason != "" || response.StatusCode == 401 || response.StatusCode == 402 || response.StatusCode == 403 || response.StatusCode == 429
		rootGone := (request.URL.Path == "" || request.URL.Path == "/") && (response.StatusCode == 404 || response.StatusCode == 410)
		return "", "", &huangguoFetchError{err: d.catalogResponseError(request, response, body), blocked: blocked,
			recoverable: !blocked && (response.StatusCode >= 500 || response.StatusCode == 408 || rootGone)}
	}
	effective := address
	if response.Request != nil {
		effective = response.Request.URL.String()
	}
	rememberPlaybackResponseURL(ctx, address, response)
	return string(body), effective, nil
}

// fetchHuangguoWithMirrors 获取黄果包含镜像列表。
func (d *Downloader) fetchHuangguoWithMirrors(ctx context.Context, source, raw, referer string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 40*time.Second)
	defer cancel()
	tried := map[string]bool{}
	var last error
	failed := false
	for round := 0; round < 2; round++ {
		for _, address := range d.huangguoCandidates(source, raw) {
			if tried[address] || len(tried) >= 6 {
				continue
			}
			if ctx.Err() != nil {
				return "", ctx.Err()
			}
			tried[address] = true
			body, effective, err := d.fetchHuangguoDirectText(ctx, address, referer)
			if err == nil {
				if responses, _ := ctx.Value(playbackResponseURLsKey{}).(*playbackResponseURLs); responses != nil {
					responses.values.Store(raw, effective)
				}
				parsed, _ := url.Parse(effective)
				d.providerMu.Lock()
				if d.providerHosts == nil {
					d.providerHosts = map[string]string{}
				}
				d.providerHosts[source] = providerMediaOrigin(parsed)
				d.providerMu.Unlock()
				if failed {
					d.scheduleHuangguoDiscovery()
				}
				return body, nil
			}
			last = err
			if !recoverableHuangguoError(err) {
				return "", err
			}
			failed = true
			d.markHuangguoFailure(address, err)
		}
		if round == 0 && failed && ctx.Err() == nil {
			d.recoverHuangguoMirrors(ctx, source)
		}
	}
	if last == nil {
		last = errors.New("未取得通过实播验证的同类备用地址")
	}
	return "", fmt.Errorf("黄果请求失败，地址及原因已保留：%w", last)
}

// scheduleHuangguoDiscovery 调度黄果发现。
func (d *Downloader) scheduleHuangguoDiscovery() {
	r := d.huangguoMirrorStore()
	r.mu.Lock()
	needed := !r.refreshing && time.Since(r.lastDiscovery) >= 15*time.Minute
	r.mu.Unlock()
	if !needed {
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		_, _ = d.discoverHuangguoMirrors(ctx, false)
	}()
}

// recoverHuangguoMirrors 恢复黄果镜像列表。
func (d *Downloader) recoverHuangguoMirrors(ctx context.Context, source string) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	r := d.huangguoMirrorStore()
	r.mu.Lock()
	if time.Since(r.lastRecovery) < time.Minute {
		r.mu.Unlock()
		return
	}
	r.lastRecovery = time.Now()
	r.mu.Unlock()
	if source == sourceHuangguoVideo {
		ctx = context.WithValue(ctx, sourceConnectivityOnlyKey{}, true)
	}
	_, _ = d.discoverHuangguoMirrors(ctx, false)
	entries, _, _ := r.snapshot()
	checked := 0
	seenRoutes := map[string]bool{}
	for index := len(entries) - 1; index >= 0 && checked < 2; index-- {
		entry := entries[index]
		if ctx.Err() != nil || entry.Kind == "publication" || usableHuangguoMirror(entry, source) || huangguoSource(entry.Kind) && entry.Kind != source || time.Now().Before(entry.RetryAt) {
			continue
		}
		route := firstNonEmpty(entry.EffectiveOrigin, entry.Origin)
		if seenRoutes[route] {
			continue
		}
		seenRoutes[route] = true
		checked++
		result := d.checkHuangguoMirror(ctx, entry.Origin)
		if usableHuangguoMirror(result, source) {
			return
		}
	}
}

// fetchHuangguoPublicationText 获取黄果地址发布页文本。
func (d *Downloader) fetchHuangguoPublicationText(ctx context.Context, address, referer string) (string, error) {
	for attempt := 0; ; attempt++ {
		body, _, err := d.fetchHuangguoDirectText(ctx, address, referer)
		if err == nil || attempt == 1 || !recoverableHuangguoError(err) {
			return body, err
		}
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(300 * time.Millisecond):
		}
	}
}

// discoverHuangguoMirrors 发现黄果镜像列表。
func (d *Downloader) discoverHuangguoMirrors(ctx context.Context, force bool) (int, error) {
	added, _, err := d.discoverHuangguoCatalogCandidates(ctx, force)
	return added, err
}

// discoverHuangguoCatalogCandidates 发现黄果目录候选列表。
func (d *Downloader) discoverHuangguoCatalogCandidates(ctx context.Context, force bool) (int, []string, error) {
	r := d.huangguoMirrorStore()
	r.mu.Lock()
	if r.refreshing || !force && time.Since(r.lastDiscovery) < 15*time.Minute {
		r.mu.Unlock()
		return 0, nil, nil
	}
	r.refreshing, r.lastDiscovery = true, time.Now()
	r.mu.Unlock()
	defer func() { r.mu.Lock(); r.refreshing = false; r.mu.Unlock() }()
	added := 0
	var discovered []string
	var failures []error
	for _, publication := range huangguoPublicationURLs {
		if ctx.Err() != nil {
			return added, discovered, ctx.Err()
		}
		result := huangguoPublicationStatus{URL: publication, CheckedAt: time.Now(), State: "failed"}
		body, err := d.fetchHuangguoPublicationText(ctx, publication+"/", "")
		origins := []string{}
		if err == nil {
			origins = parseHuangguoPublication(body)
			for _, script := range huangguoPublicationScripts(body, publication) {
				js, scriptErr := d.fetchHuangguoPublicationText(ctx, script, publication+"/")
				if scriptErr != nil {
					err = scriptErr
					continue
				}
				origins = append(origins, parseHuangguoPublication(js)...)
			}
		}
		r.mu.Lock()
		discovered = append(discovered, origins...)
		for _, origin := range origins {
			old := len(r.disk.Mirrors)
			r.appendLocked(origin, time.Now().Format("2006-01-02"), publication)
			if len(r.disk.Mirrors) > old {
				added++
				result.Added++
			}
		}
		if err == nil && len(origins) > 0 {
			result.State, result.Message = "ok", fmt.Sprintf("读取%d个地址，新增%d个；旧地址全部保留", len(origins), result.Added)
		} else {
			if err == nil {
				err = errors.New("发布页未提供可识别的站点根地址")
			}
			result.Message = publicError(err).Error()
			failures = append(failures, err)
		}
		for i := range r.disk.Publications {
			if r.disk.Publications[i].URL == publication {
				r.disk.Publications[i] = result
			}
		}
		_ = r.saveLocked()
		r.mu.Unlock()
	}
	return added, discovered, errors.Join(failures...)
}

var huangguoPublicationArray = regexp.MustCompile(`(?s)\b(?:var|const|let)\s+(subdomains|urls)\s*=\s*\[(.*?)\]`)
var huangguoPublicationLiteral = regexp.MustCompile(`(?s)//[^\r\n]*|/\*.*?\*/|["']([a-zA-Z0-9./:_-]+)["']`)
var huangguoPublicationRoot = regexp.MustCompile(`["'](https://[a-zA-Z0-9.-]+(?::\d+)?/?)['"]`)

// acceptedHuangguoPublicationOrigin 判断候选来源是否满足已声明的地址发布页约束。
func acceptedHuangguoPublicationOrigin(raw string) string {
	origin := publicHuangguoOrigin(raw)
	if origin == "" {
		return ""
	}
	u, _ := url.Parse(origin)
	for _, excluded := range []string{"x.com", "twitter.com", "t.me", "telegram.org", "github.com", "youtube.com", "google.com", "mc.yandex.ru", "huangguoai.pages.dev", "huang-guo.pages.dev", "huangguoai.com", "huangguoai.ai"} {
		if u.Hostname() == excluded || strings.HasSuffix(u.Hostname(), "."+excluded) {
			return ""
		}
	}
	return origin
}

// parseHuangguoPublication 解析黄果地址发布页。
func parseHuangguoPublication(body string) []string {
	result := []string{}
	seen := map[string]bool{}
	add := func(raw string) {
		origin := acceptedHuangguoPublicationOrigin(raw)
		if origin != "" && !seen[origin] && len(result) < 128 {
			seen[origin] = true
			result = append(result, origin)
		}
	}
	if document, err := html.Parse(strings.NewReader(body)); err == nil {
		type publicationLink struct {
			href      string
			label     string
			preferred bool
		}
		var links []publicationLink
		var text func(*html.Node) string
		text = func(node *html.Node) string {
			if node.Type == html.TextNode {
				return node.Data
			}
			value := ""
			for child := node.FirstChild; child != nil; child = child.NextSibling {
				value += text(child)
			}
			return value
		}
		var visit func(*html.Node)
		visit = func(node *html.Node) {
			if node.Type == html.ElementNode && node.Data == "a" {
				link := publicationLink{label: strings.ToLower(text(node))}
				for _, a := range node.Attr {
					if a.Key == "href" {
						link.href = a.Val
					}
					if a.Key == "class" {
						for _, className := range strings.Fields(a.Val) {
							link.preferred = link.preferred || className == "use-btn"
						}
					}
				}
				if link.href != "" {
					links = append(links, link)
				}
			}
			for child := node.FirstChild; child != nil; child = child.NextSibling {
				visit(child)
			}
		}
		visit(document)
		preferred := false
		for _, link := range links {
			preferred = preferred || link.preferred
		}
		for _, link := range links {
			if preferred && !link.preferred || strings.Contains(link.label, "下载") || strings.Contains(link.label, "app") || strings.Contains(link.label, "隐私") || strings.Contains(link.label, "协议") {
				continue
			}
			add(link.href)
		}
	}
	arrays := map[string][]string{}
	for _, match := range huangguoPublicationArray.FindAllStringSubmatch(body, -1) {
		for _, literal := range huangguoPublicationLiteral.FindAllStringSubmatch(match[2], -1) {
			if literal[1] != "" {
				arrays[match[1]] = append(arrays[match[1]], literal[1])
			}
		}
	}
	for _, prefix := range arrays["subdomains"] {
		if len(prefix) > 63 || strings.ContainsAny(prefix, ".:/_") {
			continue
		}
		for _, domain := range arrays["urls"] {
			if strings.HasPrefix(domain, "https://") {
				u, err := url.Parse(domain)
				if err != nil || publicHuangguoOrigin(domain) == "" {
					continue
				}
				domain = u.Host
			}
			add("https://" + prefix + "." + strings.TrimRight(domain, "/"))
		}
	}
	if len(arrays) > 0 {
		for _, match := range huangguoPublicationRoot.FindAllStringSubmatch(body, -1) {
			add(match[1])
		}
	}
	return result
}

// huangguoPublicationScripts 返回黄果地址发布页脚本列表。
func huangguoPublicationScripts(body, origin string) []string {
	result := []string{}
	u, _ := url.Parse(origin)
	document, err := html.Parse(strings.NewReader(body))
	if err != nil {
		return result
	}
	var visit func(*html.Node)
	visit = func(node *html.Node) {
		if node.Type == html.ElementNode && node.Data == "script" && len(result) < 2 {
			for _, a := range node.Attr {
				if a.Key != "src" {
					continue
				}
				candidate, err := u.Parse(a.Val)
				if err == nil && candidate.Scheme == u.Scheme && candidate.Host == u.Host && candidate.Path == "/publish.js" {
					result = append(result, candidate.String())
				}
			}
		}
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			visit(child)
		}
	}
	visit(document)
	return result
}
