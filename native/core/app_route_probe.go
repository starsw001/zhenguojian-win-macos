package core

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"
)

type nativeRouteStatus struct {
	Index      int    `json:"index"`
	Quality    int    `json:"quality"`
	Status     string `json:"status"`
	Message    string `json:"message,omitempty"`
	HTTPStatus int    `json:"httpStatus,omitempty"`
	BytesRead  int    `json:"bytesRead"`
	ElapsedMS  int64  `json:"elapsedMs"`
	CheckedAt  int64  `json:"checkedAt,omitempty"`
	Stop       bool   `json:"stop,omitempty"`
}

type nativeRouteChecks struct {
	mu       sync.Mutex
	results  map[int]nativeRouteStatus
	blocked  bool
	resolved map[int]nativeResolvedRoute
}

// nativeRouteKey 返回原生线路键。
func nativeRouteKey(media providerMedia) string {
	return fmt.Sprintf("%x", sha256.Sum256([]byte(media.URL+"\x00"+media.Referer+fmt.Sprint(media.Quality))))
}

// nativeDownloadRouteOrder 根据选中地址生成当前分集的下载候选顺序。
func nativeDownloadRouteOrder(choices []providerMedia, preferred int, all bool, key string) []providerMedia {
	ordered := append([]providerMedia(nil), choices...)
	if key != "" {
		for index, media := range ordered {
			if nativeRouteKey(media) == key {
				preferred = index + 1
				break
			}
		}
	}
	if preferred > 0 && preferred <= len(ordered) {
		ordered = append(ordered[preferred-1:], ordered[:preferred-1]...)
	}
	if !all {
		ordered = ordered[:min(3, len(ordered))]
	}
	return ordered
}

// nativeCheckPlaybackRoute 检查指定同集候选的连接状态并返回探测结果。
func (engine *nativeEngine) nativeCheckPlaybackRoute(ctx context.Context, session string, index int) (nativeRouteStatus, error) {
	engine.mu.Lock()
	choice, exists := engine.playbacks[session]
	engine.mu.Unlock()
	if !exists || !choice.guardAccess || index < 0 || index >= len(choice.media) || choice.checks == nil {
		return nativeRouteStatus{}, errors.New("该集候选地址已失效，请重新进入播放页")
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	stop := context.AfterFunc(choice.probeContext, cancel)
	defer stop()
	checks := choice.checks
	checks.mu.Lock()
	defer checks.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return nativeRouteStatus{}, err
	}
	if old, ok := checks.results[index]; ok && time.Since(time.UnixMilli(old.CheckedAt)) < 90*time.Second {
		return old, nil
	}
	result := nativeRouteStatus{Index: index, Quality: choice.media[index].Quality, Status: "unknown"}
	if checks.blocked {
		result.Stop = true
		result.Message = "授权或限流保护，检测已停止"
		return result, nil
	}
	start := time.Now()
	media, err := checks.resolveLocked(ctx, engine.downloader, choice.media[index], index)
	if err == nil {
		result, err = engine.probePlaybackRoute(ctx, media, result)
	}
	if ctx.Err() != nil && choice.probeContext.Err() != nil {
		return nativeRouteStatus{}, context.Canceled
	}
	result.CheckedAt = time.Now().UnixMilli()
	result.ElapsedMS = time.Since(start).Milliseconds()
	if err != nil {
		result.Status = "failed"
		result.Message = "连接失败或超时"
		if nativeRouteMustStop(err) {
			result.Stop = true
			result.Message = "站源要求暂停，检测已停止"
		}
	}
	if result.Stop {
		checks.blocked = true
	}
	checks.results[index] = result
	return result, nil
}

// probePlaybackRoute 按探测预算检查候选媒体或播放列表的响应。
func (engine *nativeEngine) probePlaybackRoute(ctx context.Context, media providerMedia, result nativeRouteStatus) (nativeRouteStatus, error) {
	if !isProviderHTTPMediaURL(media.URL) {
		return result, errors.New("无效媒体地址")
	}
	ctx = providerMediaContext(ctx, media.credentials)
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, media.URL, nil)
	if err != nil {
		return result, err
	}
	request.Header.Set("Range", "bytes=0-1023")
	request.Header.Set("Referer", media.Referer)
	response, err := engine.downloader.doMediaRequest(request)
	if err != nil {
		return result, err
	}
	defer response.Body.Close()
	result.HTTPStatus = response.StatusCode
	body, err := io.ReadAll(io.LimitReader(response.Body, 1024))
	result.BytesRead = len(body)
	if err != nil {
		return result, err
	}
	result.Status = "failed"
	if nativeMediaAccessStopsSwitch(response, body) || response.Header.Get("Retry-After") != "" {
		result.Stop = true
		result.Message = fmt.Sprintf("HTTP %d，授权或限流保护", response.StatusCode)
		_ = engine.downloader.catalogResponseError(request, response, body)
		return result, nil
	}
	if response.StatusCode != 200 && response.StatusCode != 206 {
		result.Message = fmt.Sprintf("HTTP %d", response.StatusCode)
		return result, nil
	}
	kind := strings.ToLower(response.Header.Get("Content-Type"))
	recognized := len(body) >= 8 && bytes.Equal(body[4:8], []byte("ftyp")) || bytes.HasPrefix(bytes.TrimSpace(bytes.TrimPrefix(body, []byte{0xef, 0xbb, 0xbf})), []byte("#EXTM3U")) || len(body) >= 188 && body[0] == 0x47 || len(body) >= 9 && bytes.HasPrefix(body, []byte("FLV"))
	if strings.Contains(kind, "text/html") || strings.Contains(kind, "application/json") || !recognized {
		result.Message = "未取得可识别媒体开头"
		return result, nil
	}
	result.Status = "passed"
	result.Message = "媒体开头连接通过，不代表解码或整集下载成功"
	if bytes.Contains(body, []byte("#EXTM3U")) {
		result.Message = "播放列表连接通过，尚未验证全部分片或解码"
	}
	return result, nil
}
