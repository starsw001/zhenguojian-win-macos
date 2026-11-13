package core

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

type nativeHealthStep struct {
	Name         string `json:"name"`
	State        string `json:"state"`
	Message      string `json:"message"`
	Host         string `json:"host,omitempty"`
	HTTPStatus   int    `json:"httpStatus,omitempty"`
	ElapsedMS    int64  `json:"elapsedMs"`
	CFRay        string `json:"cfRay,omitempty"`
	requestRoute string
}

type nativeSourceHealth struct {
	DownloadCheck *nativeHealthStep  `json:"downloadCheck,omitempty"`
	NetworkMode   string             `json:"networkMode,omitempty"`
	CheckedAt     time.Time          `json:"checkedAt"`
	State         string             `json:"state"`
	Sample        string             `json:"sample"`
	Steps         []nativeHealthStep `json:"steps"`
}

// nativeRememberSourceHealth 记录原生站源健康状态。
func nativeRememberSourceHealth(record *nativeSourceRecord) {
	if record.Health == nil || record.Health.State == "checking" {
		return
	}
	record.HealthHistory = append(record.HealthHistory, *record.Health)
	if len(record.HealthHistory) > 16 {
		record.HealthHistory = record.HealthHistory[len(record.HealthHistory)-16:]
	}
}

// saveSourceHealth 保存站源健康状态。
func (engine *nativeEngine) saveSourceHealth(source string, health *nativeSourceHealth) (nativeSourceStatus, error) {
	source = canonicalProviderSource(source)
	if !nativeSourceAvailable(source) || health == nil || len(health.Steps) != 4 || len(health.Sample) > 2048 {
		return nativeSourceStatus{}, errors.New("站源实播检测记录无效")
	}
	if health.State != "decoded" && health.State != "partial" && health.State != "failed" {
		return nativeSourceStatus{}, errors.New("站源实播检测状态无效")
	}
	if health.NetworkMode != "auto" && health.NetworkMode != "direct" && health.NetworkMode != "manual" {
		return nativeSourceStatus{}, errors.New("站源实播检测网络模式无效")
	}
	if health.DownloadCheck != nil && (len(health.DownloadCheck.Message) > 8192 ||
		(health.DownloadCheck.State != "ok" && health.DownloadCheck.State != "failed" && health.DownloadCheck.State != "skipped")) {
		return nativeSourceStatus{}, errors.New("站源下载检测记录无效")
	}
	names := []string{"浏览", "搜索", "选集", "实际播放"}
	for index, step := range health.Steps {
		if step.Name != names[index] || health.State == "decoded" && step.State != "ok" {
			return nativeSourceStatus{}, errors.New("站源实播检测步骤不完整")
		}
		if len(step.Name) > 128 || len(step.Message) > 8192 || len(step.Host) > 253 || step.ElapsedMS < 0 || step.ElapsedMS > 600000 {
			return nativeSourceStatus{}, errors.New("站源实播检测步骤无效")
		}
		if step.State != "ok" && step.State != "failed" && step.State != "skipped" {
			return nativeSourceStatus{}, errors.New("站源实播检测步骤状态无效")
		}
	}
	engine.mu.Lock()
	defer engine.mu.Unlock()
	record := engine.sourceRecords[source]
	if record.Running {
		return nativeSourceStatus{}, errors.New("当前站源任务尚未完成，请稍后保存检测结果")
	}
	copy := *health
	copy.Steps = append([]nativeHealthStep(nil), health.Steps...)
	if copy.CheckedAt.IsZero() {
		copy.CheckedAt = time.Now()
	} else if copy.CheckedAt.Year() < 2000 || copy.CheckedAt.After(time.Now().Add(5*time.Minute)) {
		return nativeSourceStatus{}, errors.New("站源实播检测时间无效")
	}
	if health.DownloadCheck != nil {
		download := *health.DownloadCheck
		copy.DownloadCheck = &download
	}
	nativeRememberSourceHealth(&record)
	record.Health = &copy
	engine.sourceRecords[source] = record
	if err := engine.saveSourceRecordsLocked(); err != nil {
		return nativeSourceStatus{}, err
	}
	return engine.sourceStatusLocked(source), nil
}

type sourceResponseTrace struct {
	mu   sync.Mutex
	last nativeHealthStep
}

type sourceTraceKey struct{}

// observeSourceResponse 记录站源响应。
func observeSourceResponse(ctx context.Context, response *http.Response) {
	if response == nil {
		return
	}
	if manager, _ := ctx.Value(nativeDownloadManagerKey{}).(*nativeDownloads); manager != nil {
		manager.observeDownloadBackoff(ctx, response)
	}
	trace, _ := ctx.Value(sourceTraceKey{}).(*sourceResponseTrace)
	if trace == nil || response == nil {
		return
	}
	step := nativeHealthStep{HTTPStatus: response.StatusCode, CFRay: truncate(response.Header.Get("Cf-Ray"), 128)}
	if response.Request != nil && response.Request.URL != nil {
		address := response.Request.URL
		step.Host = address.Hostname()
		if step.Host == "api5-normal-sinfonlineb.fqnovel.com" && address.Path == "/novel/player/multi_video_model/v1/" {
			step.requestRoute = "/novel/player/multi_video_model/v1/"
		} else if step.Host == "hongguoduanju.com" && strings.HasPrefix(address.Path, "/player/") {
			step.requestRoute = "官网同集播放页"
		}
	}
	trace.mu.Lock()
	trace.last = step
	trace.mu.Unlock()
}

// checkSource 检查站源。
func (engine *nativeEngine) checkSource(ctx context.Context, source string, selected nativeDrama, playback bool) error {
	if source != sourceHuangguoAI {
		ctx = context.WithValue(ctx, sourceConnectivityOnlyKey{}, true)
		ctx = context.WithValue(ctx, sourceRuleRefreshKey{}, true)
	}
	report := nativeSourceHealth{NetworkMode: engine.resourceSettings().ProxyMode, CheckedAt: time.Now(), State: "checking", Steps: []nativeHealthStep{}}
	trace := &sourceResponseTrace{}
	ctx = context.WithValue(ctx, sourceTraceKey{}, trace)
	remembered := false
	publish := func() {
		copy := report
		copy.Steps = append([]nativeHealthStep{}, report.Steps...)
		engine.changeSourceRecord(source, func(record *nativeSourceRecord) {
			if !remembered {
				nativeRememberSourceHealth(record)
				remembered = true
			}
			record.Health = &copy
		})
	}
	var failure error
	defer func() {
		report.CheckedAt = time.Now()
		if failure != nil {
			report.State = "failed"
		} else if playback {
			report.State = "ok"
		} else {
			report.State = "catalogOnly"
		}
		publish()
	}()
	run := func(name string, action func() (string, error)) bool {
		engine.changeSourceRecord(source, func(record *nativeSourceRecord) {
			record.Stage, record.Completed = name, len(report.Steps)
			record.Total = 1
			if playback {
				record.Total = 5
				if source != sourceHuangguoAI {
					record.Total = 7
				}
			}
		})
		trace.mu.Lock()
		trace.last = nativeHealthStep{}
		trace.mu.Unlock()
		start := time.Now()
		message, err := action()
		trace.mu.Lock()
		step := trace.last
		trace.mu.Unlock()
		step.Name, step.State, step.Message, step.ElapsedMS = name, "ok", message, time.Since(start).Milliseconds()
		if err != nil {
			failure = err
			step.State, step.Message = "failed", publicError(err).Error()
			var backoff *requestBackoff
			if errors.As(err, &backoff) {
				step.HTTPStatus, step.Host = backoff.status, backoff.host
			}
		}
		if source == sourceHongguo && name == "播放地址与播放列表" && step.requestRoute != "" {
			step.Message += "；取流入口：" + step.requestRoute
		}
		report.Steps = append(report.Steps, step)
		publish()
		return err == nil
	}
	var page nativeCatalogResult
	if !run("入口与目录", func() (string, error) {
		var err error
		page, err = engine.nativeCatalog(ctx, nativeInput{Source: source, Page: 1, Force: true})
		if err == nil && page.Warning != "" {
			err = errors.New(page.Warning)
		}
		if err == nil && len(page.Items) == 0 {
			err = errors.New("入口可达，但没有解析到有效剧集")
		}
		return fmt.Sprintf("已解析 %d 部剧", len(page.Items)), err
	}) || !playback {
		return failure
	}
	drama := selected
	if drama.ID == "" {
		drama = page.Items[0]
		for _, item := range page.Items {
			if item.VIP == nil || !*item.VIP {
				drama = item
				break
			}
		}
	}
	report.Sample = drama.Title
	if source != sourceHuangguoAI {
		run("搜索（本地优先）", func() (string, error) {
			result, err := engine.nativeCatalog(ctx, nativeInput{Source: source, Query: drama.Title, Page: 1})
			if err == nil && len(result.Items) == 0 {
				err = errors.New("样本未检索到；已检查本地和线上")
			}
			mode := "线上"
			if result.LocalSearch {
				mode = "本地索引"
			}
			return fmt.Sprintf("%s匹配%d部；本地为空时才请求线上", mode, len(result.Items)), err
		})
		run("真实海报", func() (string, error) {
			cover, err := engine.loadCover(ctx, drama, true)
			if err != nil {
				return "", err
			}
			return fmt.Sprintf("真实站源海报已读取：%v", cover["path"]), nil
		})
	}
	var chapter Chapter
	if !run("分集目录", func() (string, error) {
		_, id, _ := splitProviderDramaID(drama.ID)
		_, chapters, err := engine.downloader.sourceCheckChapters(ctx, source, id)
		if err != nil {
			return "", err
		}
		for _, item := range chapters {
			if !item.VIP {
				chapter = item
				return fmt.Sprintf("%d 集，检测第 %s 集", len(chapters), item.EpisodeString(1)), nil
			}
		}
		return "", errors.New("没有可用于检测的免费分集")
	}) {
		return failure
	}
	index, err := strconv.Atoi(chapter.EpisodeString(1))
	if err != nil || index < 1 {
		index = 1
	}
	task := Task{DramaID: drama.ID, DramaTitle: drama.Title, Chapter: chapter, Index: index}
	if seriesID, videoID, valid := hongguoPlaybackIDs(task); valid {
		report.Sample = fmt.Sprintf("%s（第%d集；作品 ID %s；分集 ID %s）", drama.Title, index, seriesID, videoID)
	}
	var media providerMedia
	var keyURL, segmentURL string
	if !run("播放地址与播放列表", func() (string, error) {
		var err error
		mediaCtx := ctx
		if source == sourceHongguo {
			mediaCtx = context.WithValue(mediaCtx, hongguoFreshMediaKey{}, true)
		}
		media, err = engine.downloader.resolveProviderMedia(mediaCtx, task)
		if err != nil {
			return "", err
		}
		ctx = providerMediaContext(ctx, media.credentials)
		keyURL, segmentURL, err = engine.downloader.sourceProbeResources(ctx, media)
		if source == sourceHongguo && media.Quality > 0 {
			return fmt.Sprintf("已取得可识别的播放地址（%dP）", media.Quality), err
		}
		return "已取得可识别的播放地址", err
	}) {
		return failure
	}
	if !run("播放密钥", func() (string, error) {
		if len(media.CENCKey) == 16 || len(media.HLSKey) == 16 {
			return "密钥长度有效", nil
		}
		if keyURL == "" {
			return "当前媒体无需额外密钥", nil
		}
		if strings.HasPrefix(keyURL, "data:") {
			_, encoded, found := strings.Cut(keyURL, ",")
			decoded, err := base64.StdEncoding.DecodeString(encoded)
			if !found || err != nil || len(decoded) != 16 {
				return "", errors.New("内嵌密钥格式无效")
			}
			return "内嵌密钥有效", nil
		}
		body, err := engine.downloader.sourceProbeBytes(ctx, keyURL, media.Referer, true)
		if err == nil && len(body) != 16 {
			err = errors.New("返回的播放密钥不是 16 字节")
		}
		return "密钥可达且长度有效", err
	}) {
		return failure
	}
	run("媒体连接", func() (string, error) {
		body, err := engine.downloader.sourceProbeBytes(ctx, segmentURL, media.Referer, false)
		if err == nil && len(body) == 0 {
			err = errors.New("媒体服务器返回空内容")
		}
		if source == sourceHongguo && err == nil {
			return fmt.Sprintf("已读取媒体开头 %d 字节；未做播放器实播或完整下载", len(body)), nil
		}
		return "已读取媒体开头；实际播放效果请在播放器确认", err
	})
	engine.changeSourceRecord(source, func(record *nativeSourceRecord) { record.Completed = len(report.Steps) })
	return failure
}

// sourceProbeResources 探测站源Resources。
func (d *Downloader) sourceProbeResources(ctx context.Context, media providerMedia) (string, string, error) {
	ctx = providerMediaContext(ctx, media.credentials)
	if media.Playlist == "" {
		return "", media.URL, nil
	}
	body, address := media.Playlist, media.URL
	for depth := 0; depth < 6; depth++ {
		lines, _, master := nativeHLSSelect(strings.Split(body, "\n"), 480)
		base, err := url.Parse(address)
		if err != nil {
			return "", "", err
		}
		resolve := func(raw string) string {
			ref, err := url.Parse(strings.TrimSpace(raw))
			if err != nil {
				return ""
			}
			return base.ResolveReference(ref).String()
		}
		key, segment := "", ""
		for _, line := range lines {
			line = strings.TrimSpace(line)
			if strings.HasPrefix(line, "#EXT-X-KEY:") {
				attributes := nativeHLSAttributes(line)
				if attributes["METHOD"] == "AES-128" {
					key = resolve(attributes["URI"])
				} else if attributes["METHOD"] != "NONE" {
					return "", "", errors.New("当前密钥格式不支持健康检测")
				}
			}
			if line != "" && !strings.HasPrefix(line, "#") {
				segment = resolve(line)
				break
			}
		}
		if !isProviderHTTPMediaURL(segment) {
			return "", "", errors.New("播放列表缺少有效媒体地址")
		}
		if !master {
			return key, segment, nil
		}
		body, address, err = d.fetchMediaPlaylist(ctx, segment, media.Referer)
		if err != nil {
			return "", "", err
		}
	}
	return "", "", errors.New("播放列表嵌套过多")
}

// sourceProbeBytes 探测站源字节。
func (d *Downloader) sourceProbeBytes(ctx context.Context, address, referer string, key bool) ([]byte, error) {
	if !isProviderHTTPMediaURL(address) {
		return nil, errors.New("媒体地址无效")
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, address, nil)
	if err != nil {
		return nil, err
	}
	request.Header.Set("Referer", referer)
	limit := int64(1024)
	if key {
		limit = 17
	} else {
		request.Header.Set("Range", "bytes=0-1023")
	}
	response, err := d.doMediaRequest(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK && response.StatusCode != http.StatusPartialContent {
		body, _ := io.ReadAll(io.LimitReader(response.Body, 64<<10))
		return nil, d.catalogResponseError(request, response, body)
	}
	contentType := strings.ToLower(response.Header.Get("Content-Type"))
	if strings.Contains(contentType, "text/html") || strings.Contains(contentType, "application/json") {
		body, _ := io.ReadAll(io.LimitReader(response.Body, 64<<10))
		if catalogResponseBlockReason(response, body) != "" {
			return nil, d.catalogResponseError(request, response, body)
		}
		return nil, errors.New("媒体接口返回了网页或错误信息")
	}
	return io.ReadAll(io.LimitReader(response.Body, limit))
}

// sourceCheckChapters 检查站源分集列表。
func (d *Downloader) sourceCheckChapters(ctx context.Context, source, id string) (string, []Chapter, error) {
	if isDuanjuProviderSource(source) {
		drama, chapters, err := d.fetchDuanjuDetail(ctx, source, id)
		return drama.DisplayTitle(), chapters, err
	}
	return d.GetHuangguoChapters(ctx, source, id)
}
