package core

import (
	"bytes"
	"context"
	"fmt"
	"net/url"
	"time"
)

const hongguoCDNRouteLimit = 4

func (d *Downloader) expandHongguoCDNRoutes(ctx context.Context, media providerMedia) providerMedia {
	merged := mergeHongguoMediaRoutes([]providerMedia{media})
	if d.cdn == nil || len(merged.Variants) == 0 || len(merged.Variants) >= hongguoCDNRouteLimit {
		return merged
	}
	base := merged.Variants[0]
	parsed, err := url.Parse(base.URL)
	if err != nil || !base.Direct || parsed.Scheme != "https" || parsed.Hostname() == "" || !protectedCDNHost(parsed.Hostname()) {
		return merged
	}
	lookupContext, cancel := context.WithTimeout(ctx, 8*time.Second)
	entry, err := d.cdn.resolver.lookupEntry(lookupContext, parsed.Hostname(), "")
	cancel()
	if err != nil {
		d.recordDiagnostic(diagnosticEvent{Event: "playback.hongguo_cdn_routes", Level: "warning", Source: sourceHongguo, Host: parsed.Hostname(), Message: "红果正片 CDN 地址解析失败：" + publicError(err).Error()})
		return merged
	}
	seen := map[string]bool{}
	for _, option := range merged.Variants {
		seen[option.routeAddress] = true
	}
	candidates := make([]providerMedia, 0, min(8, len(entry.addresses)))
	for _, address := range entry.addresses {
		if seen[address] || validateCDNAddresses([]string{address}) != nil {
			continue
		}
		seen[address] = true
		candidate := base
		candidate.routeAddress = address
		candidate.Variants = nil
		candidates = append(candidates, candidate)
		if len(candidates) >= 8 {
			break
		}
	}
	type result struct {
		index int
		ok    bool
	}
	results := make(chan result, len(candidates))
	for index, candidate := range candidates {
		go func(index int, candidate providerMedia) {
			probeContext, stop := context.WithTimeout(ctx, 8*time.Second)
			defer stop()
			body, err := d.sourceProbeBytes(providerMediaRequestContext(probeContext, candidate), candidate.URL, candidate.Referer, false)
			recognized := len(body) >= 8 && (bytes.Equal(body[4:8], []byte("ftyp")) || bytes.Equal(body[4:8], []byte("styp"))) || bytes.HasPrefix(bytes.TrimSpace(bytes.TrimPrefix(body, []byte{0xef, 0xbb, 0xbf})), []byte("#EXTM3U"))
			results <- result{index: index, ok: err == nil && recognized}
		}(index, candidate)
	}
	passed := make([]bool, len(candidates))
	for range candidates {
		item := <-results
		passed[item.index] = item.ok
	}
	routes := append([]providerMedia(nil), merged.Variants...)
	for index, candidate := range candidates {
		if passed[index] {
			routes = append(routes, candidate)
			if len(routes) >= hongguoCDNRouteLimit {
				break
			}
		}
	}
	resultMedia := mergeHongguoMediaRoutes(routes)
	d.recordDiagnostic(diagnosticEvent{Event: "playback.hongguo_cdn_routes", Level: "info", Source: sourceHongguo, Host: parsed.Hostname(), Message: fmt.Sprintf("红果官网正片已验证 %d 条可切换网络地址", len(resultMedia.Variants))})
	return resultMedia
}
