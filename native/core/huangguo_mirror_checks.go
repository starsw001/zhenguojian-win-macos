package core

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"
)

// checkHuangguoMirror 检查黄果镜像。
func (d *Downloader) checkHuangguoMirror(parent context.Context, origin string) (result huangguoMirrorRecord) {
	r := d.huangguoMirrorStore()
	select {
	case r.checkGate <- struct{}{}:
		defer func() { <-r.checkGate }()
	case <-parent.Done():
		return huangguoMirrorRecord{Origin: origin, State: "pending", Message: "检测已取消，原记录保留"}
	}
	r.mu.Lock()
	index := r.appendLocked(origin, "", "地址核验")
	if index < 0 {
		r.mu.Unlock()
		return huangguoMirrorRecord{Origin: origin, State: "failed", Message: "无效地址或地址登记已达上限"}
	}
	previous := r.disk.Mirrors[index]
	result = previous
	result.DuplicateOf = ""
	result.Poster, result.PosterWidth, result.PosterHeight, result.PosterBytes = false, 0, 0, 0
	result.PosterMessage = "尚未取得可核验海报"
	result.Browse, result.Selection, result.Playback, result.MediaReachable = false, false, false, false
	result.CatalogCount, result.EpisodeCount, result.Width, result.Height, result.PositionMS = 0, 0, 0, 0, 0
	result.SampleID = ""
	result.State, result.Message = "checking", "正在检查真实海报、浏览、分集和实际解码"
	r.disk.Mirrors[index] = result
	r.mu.Unlock()
	ctx, cancel := context.WithTimeout(parent, 90*time.Second)
	defer cancel()
	preservePrevious := false
	defer func() {
		if preservePrevious {
			r.mu.Lock()
			r.disk.Mirrors[index] = previous
			r.mu.Unlock()
			result = previous
			return
		}
		result.CheckedAt = time.Now()
		if result.State != "ready" && result.State != "connected" {
			result.State = "failed"
			result.LastError = result.Message
			result.RetryAt = time.Now().Add(5 * time.Minute)
		}
		if parent.Err() != nil {
			result = previous
			if result.State == "checking" {
				result.State = "pending"
			}
			result.Message = "检测已取消，地址和上次结果保留"
		}
		r.mu.Lock()
		r.disk.Mirrors[index] = result
		_ = r.saveLocked()
		result = r.disk.Mirrors[index]
		r.mu.Unlock()
	}()
	fail := func(stage string, err error) {
		result.Message = stage + "：" + publicError(err).Error()
		if len(result.Message) > 1600 {
			result.Message = truncate(result.Message, 1600)
		}
	}
	body, effective, err := d.fetchHuangguoMirrorRoot(ctx, origin)
	if err != nil {
		fail("首页访问失败", err)
		return
	}
	u, err := url.Parse(effective)
	if err != nil {
		fail("首页地址无效", err)
		return
	}
	result.EffectiveOrigin = providerMediaOrigin(u)
	ai := parseHuangguoAIDramaCards(body, effective, "")
	video := parseHuangguoVideoCards(body, effective)
	if len(ai) > 0 && len(video) == 0 {
		result.Kind = sourceHuangguoAI
	} else if len(video) > 0 && len(ai) == 0 {
		result.Kind = sourceHuangguoVideo
	} else {
		if len(parseHuangguoPublication(body)) > 0 && strings.Contains(body, "最新地址") && strings.Contains(body, "发布") {
			result.Kind = "publication"
			fail("不是可播放站点", errors.New("这是地址发布或导航页，不提供剧集目录和分集；请提供直接的黄果AI或视频站根地址"))
			return
		}
		fail("无法判断站点类型", errors.New("未得到唯一匹配的黄果AI或黄果视频页面结构"))
		return
	}
	onlyConnection, _ := parent.Value(sourceConnectivityOnlyKey{}).(bool)
	if onlyConnection && result.Kind != sourceHuangguoVideo {
		preservePrevious = true
		result = previous
		return
	}
	cfg := d.cfg
	cfg.HuangguoAIURL, cfg.HuangguoVideoURL = origin, origin
	probe := &Downloader{cfg: cfg, client: d.client, proxyRouter: d.proxyRouter, limiter: d.limiter, providerHosts: map[string]string{}, diagnostics: d.diagnostics}
	ctx = context.WithValue(ctx, huangguoProbeOriginKey{}, origin)
	var items []Drama
	if result.Kind == sourceHuangguoAI {
		items, _, err = probe.fetchHuangguoAICatalogPage(ctx, 1, "ai-duanju")
	} else {
		var catalog string
		catalog, effective, err = probe.fetchHuangguoDirectText(ctx, origin+"/videos?page=1", origin+"/")
		if err == nil {
			items = parseHuangguoVideoCards(catalog, effective)
		}
	}
	if err != nil || len(items) == 0 {
		if err == nil {
			err = errors.New("客户端目录解析为空，不能仅凭首页可访问判定通过")
		}
		fail("浏览失败", err)
		return
	}
	result.Browse, result.CatalogCount = true, len(items)
	var last error
	lastStage := "选集失败"
	for _, drama := range items[:min(3, len(items))] {
		if drama.VIP != nil && *drama.VIP || strings.Contains(drama.CategoryName, "换脸") {
			continue
		}
		result.SampleID = drama.SourceID
		result.Selection, result.Playback, result.Poster = false, false, false
		result.EpisodeCount, result.Width, result.Height, result.PositionMS = 0, 0, 0, 0
		result.PosterWidth, result.PosterHeight, result.PosterBytes = 0, 0, 0
		if d.huangguoPoster == nil {
			last, lastStage = errors.New("当前宿主未提供真实海报解码器，不自动启用"), "真实海报失败"
			result.PosterMessage = last.Error()
			break
		}
		poster, posterErr := d.huangguoPoster(ctx, drama, result.EffectiveOrigin)
		if posterErr == nil && (poster.Width <= 0 || poster.Height <= 0 || poster.Bytes <= 0) {
			posterErr = errors.New("海报未返回有效实际解码尺寸和数据量")
		}
		if posterErr != nil {
			last, lastStage = posterErr, "真实海报失败"
			result.PosterMessage = publicError(posterErr).Error()
			continue
		}
		result.Poster, result.PosterWidth, result.PosterHeight, result.PosterBytes = true, poster.Width, poster.Height, poster.Bytes
		result.PosterMessage = poster.Message
		lastStage = "选集失败"
		var detail Drama
		var chapters []Chapter
		if result.Kind == sourceHuangguoAI {
			detail, chapters, err = probe.fetchHuangguoAIDetail(ctx, drama.SourceID)
		} else {
			detail, chapters, err = probe.fetchHuangguoVideoDetail(ctx, drama.SourceID)
		}
		if err != nil || len(chapters) == 0 {
			if err == nil {
				err = errors.New("未取得可选分集")
			}
			last = err
			continue
		}
		if detail.ID != drama.ID {
			last = errors.New("目录和详情的剧集标识不一致，未接入备用切换")
			continue
		}
		result.Selection, result.EpisodeCount, result.SampleID = true, len(chapters), drama.SourceID
		lastStage = "实际播放失败"
		for chapterIndex, chapter := range chapters[:min(2, len(chapters))] {
			if chapter.VIP {
				last = errors.New("分集需要合法授权，未绕过付费")
				continue
			}
			media, mediaErr := probe.resolveProviderMedia(ctx, Task{DramaID: drama.ID, DramaTitle: drama.DisplayTitle(), Chapter: chapter, Index: chapterIndex + 1})
			if mediaErr != nil {
				last = mediaErr
				continue
			}
			if onlyConnection {
				probeContext := providerMediaContext(ctx, media.credentials)
				key, segment, connectionErr := probe.sourceProbeResources(probeContext, media)
				if connectionErr == nil && key != "" && !strings.HasPrefix(key, "data:") {
					var keyBytes []byte
					keyBytes, connectionErr = probe.sourceProbeBytes(probeContext, key, media.Referer, true)
					if connectionErr == nil && len(keyBytes) != 16 {
						connectionErr = errors.New("播放密钥长度无效")
					}
				}
				if connectionErr == nil {
					_, connectionErr = probe.sourceProbeBytes(probeContext, segment, media.Referer, false)
				}
				if connectionErr != nil {
					last, lastStage = connectionErr, "线路连接失败"
					continue
				}
				result.MediaReachable, result.State, result.RetryAt = true, "connected", time.Time{}
				result.Message = "真实海报、浏览、选集及媒体连接通过；未启动播放器、未解码，待手动实播"
				return
			}
			if d.huangguoDecode == nil {
				last = errors.New("当前宿主未提供随包解码器，已保留取流结果但不自动启用")
				break
			}
			decoded, decodeErr := d.huangguoDecode(ctx, media)
			if decodeErr != nil {
				last = decodeErr
				continue
			}
			if decoded.Width <= 0 || decoded.Height <= 0 || decoded.PositionMS < 400 {
				last = errors.New("解码器未返回有效视频尺寸和前进进度")
				continue
			}
			result.Playback, result.Width, result.Height, result.PositionMS = true, decoded.Width, decoded.Height, decoded.PositionMS
			result.State = "ready"
			result.RetryAt = time.Time{}
			result.Message = fmt.Sprintf("海报%d×%d真实解码；浏览%d部、选集%d集；视频解码%d×%d，进度%dms；四项通过", result.PosterWidth, result.PosterHeight, result.CatalogCount, result.EpisodeCount, result.Width, result.Height, result.PositionMS)
			return
		}
		if ctx.Err() != nil {
			last = ctx.Err()
			break
		}
	}
	if last == nil {
		last = errors.New("未取得可合法免费验证的分集，不绕过登录、付费或验证码")
	}
	fail(lastStage, last)
	return
}

// fetchHuangguoMirrorRoot 获取黄果镜像Root。
func (d *Downloader) fetchHuangguoMirrorRoot(ctx context.Context, origin string) (string, string, error) {
	for attempt := 0; ; attempt++ {
		body, effective, err := d.fetchHuangguoDirectText(ctx, origin+"/", origin+"/")
		if err == nil || attempt == 1 || !recoverableHuangguoError(err) {
			return body, effective, err
		}
		d.client.CloseIdleConnections()
		select {
		case <-ctx.Done():
			return "", "", ctx.Err()
		case <-time.After(400 * time.Millisecond):
		}
	}
}
