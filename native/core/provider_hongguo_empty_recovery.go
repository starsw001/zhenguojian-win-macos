package core

import (
	"context"
	"errors"
	"net/http"
	"sync/atomic"
	"time"
)

type hongguoEpisodeEmptyKey struct{}

type hongguoEpisodeEmptyRead struct {
	request    *http.Request
	body       []byte
	at         time.Time
	proxyTried atomic.Bool
}

var errHongguoWebEpisodeNotFound = errors.New("红果官网没有本集页面")

func (state *hongguoEpisodeEmptyRead) remember(request *http.Request, body []byte) {
	if state == nil || state.request != nil || state.proxyTried.Load() || request.Method != http.MethodPost ||
		!hongguoRequestAt(request, hongguoMobileV5API) || len(body) > 65536 || !replayableProviderRead(request) {
		return
	}
	state.request = request.Clone(context.Background())
	state.request.Body = nil
	state.body = append([]byte(nil), body...)
	state.at = time.Now()
}

func (state *hongguoEpisodeEmptyRead) retry(ctx context.Context, d *Downloader, episodeID string) (providerMedia, bool, error) {
	if state.request == nil || d.proxyRouter == nil || time.Since(state.at) > 45*time.Second || ctx.Err() != nil {
		return providerMedia{}, false, nil
	}
	mode, _ := d.proxyRouter.requestMode(state.request)
	proxy, err := d.proxyRouter.proxy(state.request)
	if mode != "auto" || err != nil || proxy == nil || !state.proxyTried.CompareAndSwap(false, true) {
		return providerMedia{}, false, nil
	}
	ctx = context.WithValue(ctx, hongguoFallbackProxyKey{}, proxy)
	d.recordDiagnostic(diagnosticEvent{Event: "playback.hongguo_empty_read_proxy", Source: sourceHongguo, Level: "info", Host: state.request.URL.Hostname(), Message: "直连播放接口均未返回正文，使用已配置的代理复核同一只读请求一次"})
	result, err := d.hongguoMultiFetchJSON(ctx, state.request.URL.String(), http.MethodPost, nil, state.body, func(r *http.Request) { r.Header = state.request.Header.Clone() })
	if err != nil {
		return providerMedia{}, true, err
	}
	model, err := hongguoMobileV5Model(result, episodeID, false)
	if err != nil {
		return providerMedia{}, true, err
	}
	media, err := selectHongguoAppMedia(model)
	return media, true, err
}
