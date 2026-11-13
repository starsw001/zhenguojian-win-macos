package core

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"sort"
	"time"
)

type nativePlaybackChoice struct {
	dramaID       string
	episodeIndex  int
	checks        *nativeRouteChecks
	probeContext  context.Context
	probeCancel   context.CancelFunc
	danmakuSeries string
	danmakuVideo  string
	media         []providerMedia
	index         int
	qualities     []int
	streamSession string
	guardAccess   bool
	created       time.Time
}

// nativePlaybackChoices 为媒体计划提取可供播放切换的同集候选。
func nativePlaybackChoices(media providerMedia, quality int) nativePlaybackChoice {
	choice := nativePlaybackChoice{qualities: []int{}}
	seen := map[string]bool{}
	qualities := map[int]bool{}
	for _, option := range append([]providerMedia{media}, media.Variants...) {
		if option.pending == nil && !isProviderHTTPMediaURL(option.URL) {
			continue
		}
		identity := option.URL + "\x00" + option.Referer + "\x00" + hex.EncodeToString(option.CENCKey) + "\x00" + hex.EncodeToString(option.HLSKey)
		if seen[identity] {
			continue
		}
		seen[identity] = true
		if len(choice.media) >= nativePlaybackRouteLimit {
			break
		}
		option.Variants = nil
		choice.media = append(choice.media, option)
		if option.Quality > 0 {
			qualities[option.Quality] = true
		}
	}
	sort.SliceStable(choice.media, func(i, j int) bool { return choice.media[i].Quality > choice.media[j].Quality })
	for value := range qualities {
		choice.qualities = append(choice.qualities, value)
	}
	sort.Sort(sort.Reverse(sort.IntSlice(choice.qualities)))
	if quality > 0 && qualities[quality] {
		filtered := make([]providerMedia, 0, len(choice.media))
		for _, option := range choice.media {
			if option.Quality == quality {
				filtered = append(filtered, option)
			}
		}
		choice.media = filtered
	}
	return choice
}

// nativePlaybackChoicesForSource 按站源规则提取当前集的播放候选。
func nativePlaybackChoicesForSource(source string, media providerMedia, quality int) nativePlaybackChoice {

	choice := nativePlaybackChoices(media, 0)
	choice.guardAccess = true
	if quality > 0 {
		sort.SliceStable(choice.media, func(i, j int) bool {
			return choice.media[i].Quality == quality && choice.media[j].Quality != quality
		})
	} else if source == sourceHongguo && media.Direct {
		for index, option := range choice.media {
			if option.URL == media.URL && option.Referer == media.Referer &&
				bytes.Equal(option.CENCKey, media.CENCKey) && bytes.Equal(option.HLSKey, media.HLSKey) {
				copy(choice.media[1:index+1], choice.media[:index])
				choice.media[0] = option
				break
			}
		}
	}
	return choice
}

// nativeOpenPlayback 创建当前集的播放计划，并保存后续地址切换所需的会话状态。
func (engine *nativeEngine) nativeOpenPlayback(ctx context.Context, choice nativePlaybackChoice) (nativePlan, error) {
	if err := ctx.Err(); err != nil {
		return nativePlan{}, err
	}
	if choice.index < 0 || choice.index >= len(choice.media) {
		return nativePlan{}, errors.New("该集没有其他可用的播放线路")
	}
	if choice.checks == nil {
		choice.checks = &nativeRouteChecks{results: map[int]nativeRouteStatus{}}
	}
	choice.checks.mu.Lock()
	media, err := choice.checks.resolveLocked(ctx, engine.downloader, choice.media[choice.index], choice.index)
	choice.checks.mu.Unlock()
	if err != nil {
		return nativePlan{}, err
	}
	if !isProviderHTTPMediaURL(media.URL) {
		return nativePlan{}, errors.New("站源未返回有效的播放地址")
	}
	tokenBytes := make([]byte, 24)
	if _, err := rand.Read(tokenBytes); err != nil {
		return nativePlan{}, errors.New("无法初始化播放会话")
	}
	plan := nativePlan{
		DanmakuID: choice.danmakuVideo,
		URL:       media.URL, Headers: map[string]string{"User-Agent": userAgent, "Referer": media.Referer},
		Key: hex.EncodeToString(media.CENCKey), Quality: media.Quality, Qualities: choice.qualities,
		RouteIndex: choice.index, RouteCount: len(choice.media), Session: hex.EncodeToString(tokenBytes),
	}
	if media.credentials != nil && !media.credentials.expires.IsZero() {
		if !time.Now().Before(media.credentials.expires) {
			return nativePlan{}, errors.New("播放凭证已过期，请重新解析播放")
		}
		plan.ExpiresAt = media.credentials.expires.UnixMilli()
	}
	choice.streamSession = ""
	{
		engine.mu.Lock()
		if engine.stream == nil {
			engine.stream, err = newNativeStreamServer(engine.downloader)
		}
		stream := engine.stream
		engine.mu.Unlock()
		if err != nil {
			return nativePlan{}, err
		}
		plan.URL, choice.streamSession = stream.nativeOpenWithAccessGuard(media, choice.guardAccess)
	}
	if choice.checks == nil {
		choice.checks = &nativeRouteChecks{results: map[int]nativeRouteStatus{}}
	}
	choice.probeContext, choice.probeCancel = context.WithCancel(context.Background())
	if choice.guardAccess {
		for index, option := range choice.media {
			status := nativeRouteStatus{Index: index, Quality: option.Quality, Status: "unknown"}
			choice.checks.mu.Lock()
			if cached, ok := choice.checks.results[index]; ok && time.Since(time.UnixMilli(cached.CheckedAt)) < 90*time.Second {
				status = cached
			}
			choice.checks.mu.Unlock()
			plan.Routes = append(plan.Routes, status)
		}
	}
	choice.created = time.Now()
	engine.mu.Lock()
	if engine.playbacks == nil {
		engine.playbacks = map[string]nativePlaybackChoice{}
	}
	var expired []string
	for token, old := range engine.playbacks {
		if time.Since(old.created) > 12*time.Hour {
			if old.probeCancel != nil {
				old.probeCancel()
			}
			expired = append(expired, old.streamSession)
			delete(engine.playbacks, token)
		}
	}
	if len(engine.playbacks) >= 8 {
		oldest := ""
		for token, old := range engine.playbacks {
			if oldest == "" || old.created.Before(engine.playbacks[oldest].created) {
				oldest = token
			}
		}
		if cancel := engine.playbacks[oldest].probeCancel; cancel != nil {
			cancel()
		}
		expired = append(expired, engine.playbacks[oldest].streamSession)
		delete(engine.playbacks, oldest)
	}
	engine.playbacks[plan.Session] = choice
	stream := engine.stream
	engine.mu.Unlock()
	if stream != nil {
		for _, token := range expired {
			stream.nativeRelease(token)
		}
	}
	if err := ctx.Err(); err != nil {
		engine.nativeReleasePlayback(plan.Session)
		return nativePlan{}, err
	}
	return plan, nil
}

// nativeNextPlayback 为当前播放会话选择后续同集候选地址。
func (engine *nativeEngine) nativeNextPlayback(ctx context.Context, session string) (nativePlan, error) {
	return engine.nativeSelectPlaybackRoute(ctx, session, -1)
}

// nativeSelectPlaybackRoute 切换到指定同集候选地址并返回相应播放计划。
func (engine *nativeEngine) nativeSelectPlaybackRoute(ctx context.Context, session string, index int) (nativePlan, error) {
	engine.mu.Lock()
	choice, exists := engine.playbacks[session]
	stream := engine.stream
	engine.mu.Unlock()
	if !exists {
		return nativePlan{}, errors.New("播放线路已失效，请重新解析播放")
	}
	if choice.guardAccess && stream != nil {
		stream.mu.Lock()
		active := stream.sessions[choice.streamSession]
		stream.mu.Unlock()
		if active == nil {
			return nativePlan{}, errors.New("播放线路已失效，请重新解析播放")
		}
		if err := active.routeAccessError(); err != nil {
			return nativePlan{}, err
		}
	}
	if index == -1 {
		index = choice.index + 1
	}
	if index < 0 || index >= len(choice.media) {
		return nativePlan{}, errors.New("该集没有这个候选地址")
	}
	choice.index = index
	return engine.nativeOpenPlayback(ctx, choice)
}

// nativeReleasePlayback 释放播放会话及其关联资源。
func (engine *nativeEngine) nativeReleasePlayback(session string) {
	engine.mu.Lock()
	choice, exists := engine.playbacks[session]
	delete(engine.playbacks, session)
	stream := engine.stream
	engine.mu.Unlock()
	if exists && choice.probeCancel != nil {
		choice.probeCancel()
	}
	if exists && stream != nil && choice.streamSession != "" {
		stream.nativeRelease(choice.streamSession)
	}
}
