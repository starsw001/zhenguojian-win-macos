package core

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"
)

const hongguoBaseURL = "https://hongguoduanju.com"

// fetchHongguoDramas 获取红果作品列表。
func (d *Downloader) fetchHongguoDramas(ctx context.Context) ([]Drama, error) {
	initialized := hongguoCatalogInitialized(d.hongguoCatalogSnapshot())
	dramas, appErr := d.fetchHongguoAppCatalog(ctx)
	if len(dramas) > 0 || appErr == nil && initialized {
		return dramas, appErr
	}
	if ctx.Err() != nil {
		return dramas, ctx.Err()
	}
	webDramas, webErr := d.fetchHongguoWebDramas(ctx)
	if len(webDramas) > 0 && webErr == nil {
		return webDramas, nil
	}
	return webDramas, errors.Join(appErr, webErr)
}

// fetchHongguoWebDramas 获取红果网页作品列表。
func (d *Downloader) fetchHongguoWebDramas(ctx context.Context) ([]Drama, error) {
	routes := []struct {
		path string
		name string
	}{
		{path: "real-drama", name: "真人剧"},
		{path: "comic-drama", name: "漫剧"},
		{path: "ai-drama", name: "AI剧"},
		{path: "comic", name: "漫画"},
	}
	type routeResult struct {
		dramas []Drama
		err    error
	}
	results := make(chan routeResult, len(routes))
	for _, route := range routes {
		route := route
		go func() {
			dramas, err := d.fetchHongguoRoute(ctx, route.path, route.name)
			results <- routeResult{dramas: dramas, err: err}
		}()
	}
	seen := map[string]bool{}
	var out []Drama
	var failures []error
	for range routes {
		result := <-results
		if result.err != nil {
			failures = append(failures, result.err)
		}
		for _, drama := range result.dramas {
			if drama.ID != "" && !seen[drama.ID] {
				seen[drama.ID] = true
				out = append(out, drama)
			}
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].DisplayTitle() < out[j].DisplayTitle() })
	return out, errors.Join(failures...)
}

// fetchHongguoRoute 获取红果线路。
func (d *Downloader) fetchHongguoRoute(ctx context.Context, route, category string) ([]Drama, error) {
	pages := d.cfg.MaxPagesPerSort
	if pages < 1 {
		pages = 1
	}
	var dramas []Drama
	seen := map[string]bool{}
	for page := 1; page <= pages; page++ {
		items, totalPages, err := d.fetchHongguoCategoryPage(ctx, route+"?page="+strconv.Itoa(page), category)
		if err != nil {
			return dramas, fmt.Errorf("%s: %w", category, err)
		}
		if totalPages > pages {
			pages = totalPages
			if pages > 500 {
				pages = 500
			}
		}
		reportLibraryProgress(ctx, sourceHongguo, items, nil, false)
		added := 0
		for _, drama := range items {
			if !seen[drama.ID] {
				seen[drama.ID] = true
				dramas = append(dramas, drama)
				added++
			}
		}
		if added == 0 || len(items) < 24 {
			break
		}
	}
	return dramas, nil
}

// fetchHongguoCategoryPage 获取红果分类页面。
func (d *Downloader) fetchHongguoCategoryPage(ctx context.Context, route, category string) ([]Drama, int, error) {
	body, err := d.fetchProviderText(ctx, hongguoBaseURL+"/category/"+route, hongguoBaseURL+"/")
	if err != nil {
		return nil, 0, err
	}
	data := parseRouterData(body)
	page := routerLoaderMap(data, "category_page", "category_$")
	if len(page) == 0 || page["isSuccess"] == false {
		return nil, 0, errors.New("红果分类数据不可用，可能是页面结构或访问权限变化")
	}
	items := anyList(page["recommendList"])
	out := make([]Drama, 0, len(items))
	for _, item := range items {
		if dr := hongguoDramaFromAny(item, category); dr.ID != "" {
			out = append(out, dr)
		}
	}
	pages, _ := strconv.Atoi(mapString(nestedMap(page, "pagination"), "totalPages"))
	return out, pages, nil
}

// fetchHongguoChapters 获取红果分集列表。
func (d *Downloader) fetchHongguoChapters(ctx context.Context, sourceID string) (string, []Chapter, error) {
	sourceID = strings.TrimPrefix(strings.TrimSpace(sourceID), "hg-series-v1:")
	if sourceID == "" {
		return "", nil, fmt.Errorf("empty hongguo sourceID")
	}
	entry, appErr := d.hongguoAppDetail(ctx, sourceID)
	if appErr == nil {
		return entry.Drama.DisplayTitle(), append([]Chapter(nil), entry.Chapters...), nil
	}
	if ctx.Err() != nil {
		return "", nil, ctx.Err()
	}
	title, chapters, webErr := d.fetchHongguoWebChapters(ctx, sourceID)
	if webErr == nil {
		return title, chapters, nil
	}
	return "", nil, fmt.Errorf("App 详情失败: %v；网页详情失败: %w", publicError(appErr), webErr)
}

// fetchHongguoWebChapters 获取红果网页分集列表。
func (d *Downloader) fetchHongguoWebChapters(ctx context.Context, sourceID string) (string, []Chapter, error) {
	body, err := d.fetchProviderText(ctx, hongguoBaseURL+"/detail?series_id="+url.QueryEscape(sourceID), hongguoBaseURL+"/")
	if err != nil {
		return "", nil, err
	}
	data := parseRouterData(body)
	page := routerLoaderMap(data, "detail_page", "detail_")
	detail, _ := page["seriesDetail"].(map[string]any)
	if len(detail) == 0 {
		return "", nil, fmt.Errorf("hongguo detail is empty")
	}
	title := firstNonEmpty(mapString(detail, "series_name", "series_title", "name"), sourceID)
	vids := anyList(detail["vid_list"])
	chapters := make([]Chapter, 0, len(vids))
	for i, v := range vids {
		vid := strings.TrimSpace(fmt.Sprint(v))
		if vid == "" || vid == "<nil>" {
			continue
		}
		idx := i + 1
		chapters = append(chapters, Chapter{ID: providerChapterID(sourceHongguo, sourceID, vid), Source: sourceHongguo, Title: fmt.Sprintf("第%d集", idx), VideoURL: "hongguo-cenc://" + vid, CurrentEpisode: rawEpisode(idx)})
	}
	if len(chapters) == 0 {
		return title, nil, errors.New("红果详情没有返回剧集 ID")
	}
	return title, chapters, nil
}

// parseRouterData 解析路由数据。
func parseRouterData(raw string) map[string]any {
	idx := regexp.MustCompile(`(?s)(?:window\.)?_ROUTER_DATA\s*=\s*`).FindStringIndex(raw)
	if idx == nil {
		return nil
	}
	var data map[string]any
	dec := json.NewDecoder(strings.NewReader(raw[idx[1]:]))
	dec.UseNumber()
	if err := dec.Decode(&data); err != nil {
		return nil
	}
	return data
}

// resolveHongguoMedia 解析红果媒体。
func (d *Downloader) resolveHongguoMedia(ctx context.Context, task Task) (providerMedia, error) {
	source, seriesID, ok := splitProviderDramaID(task.DramaID)
	if !ok || source != sourceHongguo {
		return providerMedia{}, fmt.Errorf("红果章节缺少剧 ID，请刷新剧库后重新加入队列")
	}
	videoID := strings.TrimPrefix(task.Chapter.VideoURL, "hongguo-cenc://")
	if !hongguoNumericID.MatchString(videoID) || !hongguoNumericID.MatchString(seriesID) {
		return providerMedia{}, fmt.Errorf("红果章节 ID 无效，请重新获取章节")
	}
	if err := ctx.Err(); err != nil {
		return providerMedia{}, err
	}
	fresh, _ := ctx.Value(hongguoFreshMediaKey{}).(bool)
	key := seriesID + "/" + videoID
	cached, generation, hit := d.hongguoMediaCache.get(key, fresh, time.Now())
	if hit {
		return cached, nil
	}
	media, err := d.resolveHongguoMediaRoutes(ctx, task.Index, seriesID, videoID)
	if err != nil {
		if hongguoStopPlaybackFallback(err) && !errors.Is(err, errHongguoEmptyPrimaryPlayback) {
			d.hongguoMediaCache.clear()
		}
		return providerMedia{}, err
	}
	if err := ctx.Err(); err != nil {
		return providerMedia{}, err
	}
	d.hongguoMediaCache.put(key, generation, media, time.Now())
	return media, nil
}

// resolveHongguoMediaRoutes 解析红果媒体候选线路。
func (d *Downloader) resolveHongguoMediaRoutes(ctx context.Context, episode int, seriesID, videoID string) (providerMedia, error) {
	media, primaryErr := d.resolveHongguoAppMedia(ctx, videoID)
	if primaryErr == nil {
		return media, nil
	}
	if err := ctx.Err(); err != nil {
		return providerMedia{}, err
	}
	if hongguoStopPlaybackFallback(primaryErr) && !errors.Is(primaryErr, errHongguoEmptyPrimaryPlayback) {
		return providerMedia{}, fmt.Errorf("红果第%d集播放接口暂不可用：%w", episode, primaryErr)
	}
	media, webErr := d.resolveHongguoWebMedia(ctx, seriesID, videoID)
	if webErr == nil {
		return media, nil
	}
	return providerMedia{}, fmt.Errorf("红果第%d集在线播放和下载暂不可用：%w；官网同集公开线路不可用：%w", episode, primaryErr, webErr)
}

// resolveHongguoWebMedia 解析红果网页媒体。
func (d *Downloader) resolveHongguoWebMedia(ctx context.Context, seriesID, videoID string) (providerMedia, error) {
	pageURL := hongguoBaseURL + "/player/" + url.PathEscape(seriesID) + "/" + url.PathEscape(videoID)
	ctx = context.WithValue(ctx, nativeNoRedirectKey{}, true)
	body, err := d.fetchHongguoPlaybackText(ctx, pageURL)
	if err != nil {
		return providerMedia{}, err
	}
	page, err := hongguoRequestedPlaybackPage(parseRouterData(body), seriesID, videoID)
	if err != nil {
		return providerMedia{}, err
	}
	if protected, marked := page["protectedPlayback"]; marked && protected != false {
		preview, _ := page["publicVideoSeo"].(map[string]any)
		if preview["available"] == true && mapString(preview, "seriesId") == seriesID && mapString(preview, "itemId") == videoID {
			return providerMedia{}, errors.New("红果官网正片受保护（protectedPlayback），公开预览不是正片，不会用作正片播放或下载；请在官网或官方 App 查看本集")
		}
		return providerMedia{}, errors.New("红果网页启用了受保护播放（protectedPlayback）；请在官网或官方 App 确认访问权限，软件不会尝试绕过；已下载文件仍可离线播放")
	}
	info, _ := page["video_player_info"].(map[string]any)
	if id := mapString(info, "vid", "video_id"); id != "" && id != videoID {
		return providerMedia{}, errors.New("红果官网片源分集与请求不符，未使用其他分集或试看集")
	}
	if id := mapString(info, "series_id"); id != "" && id != seriesID {
		return providerMedia{}, errors.New("红果官网片源作品与请求不符，未使用其他作品")
	}
	var addresses []string
	for _, address := range hongguoMediaAddresses(info) {
		if checked, err := hongguoMultiCheckedURL(address, []string{"qznovelvod.com", "fqnovelvod.com", "douyinvod.com"}); err == nil {
			addresses = append(addresses, checked)
		}
	}
	if len(addresses) == 0 {
		return providerMedia{}, errors.New("红果官网未提供所选分集的公开媒体地址，请在官网或官方 App 确认访问权限")
	}
	duration, _ := strconv.ParseFloat(mapString(info, "duration"), 64)
	media := providerMedia{URL: addresses[0], Referer: pageURL, Duration: time.Duration(duration * float64(time.Second))}
	for _, address := range addresses {
		variant := media
		variant.URL, variant.Variants = address, nil
		media.Variants = append(media.Variants, variant)
	}
	return media, nil
}

// hongguoDramaFromAny 将红果响应节点转换为统一作品模型。
func hongguoDramaFromAny(v any, category string) Drama {
	m, ok := v.(map[string]any)
	if !ok {
		return Drama{}
	}
	vd, _ := m["video_data"].(map[string]any)
	if len(vd) == 0 {
		vd = m
	}
	sourceID := firstNonEmpty(mapString(vd, "series_id_str", "series_id"), mapString(m, "series_id_str", "series_id"), mapString(vd, "keyword"), mapString(m, "keyword"))
	if !hongguoNumericID.MatchString(sourceID) {
		return Drama{}
	}
	title := firstNonEmpty(mapString(vd, "series_title", "series_name", "title"), mapString(m, "series_name", "name"), sourceID)
	cover := firstNonEmpty(mapString(vd, "series_cover", "cover"), mapString(m, "series_cover"))
	intro := firstNonEmpty(mapString(vd, "series_intro", "video_desc"), mapString(m, "series_intro"))
	count := firstNonEmpty(mapString(vd, "episode_cnt"), mapString(m, "episode_cnt"))
	remark := firstNonEmpty(mapString(vd, "episode_right_text"), mapString(m, "episode_right_text"))
	releaseStatus := releaseStatusFromRemark(remark)
	if mapString(vd, "series_status") == "1" {
		releaseStatus = "finished"
	} else if mapString(vd, "series_status") == "0" {
		releaseStatus = "ongoing"
	}
	if remark == "" && count != "" {
		remark = "共" + count + "集"
	}
	tags := mapStringSlice(vd, "tags")
	for _, value := range anyList(vd["category_list"]) {
		item, _ := value.(map[string]any)
		if name := mapString(item, "name"); name != "" && !slices.Contains(tags, name) {
			tags = append(tags, name)
		}
	}
	var categories []struct {
		Name string `json:"name"`
	}
	if json.Unmarshal([]byte(mapString(vd, "category_schema")), &categories) == nil {
		for _, item := range categories {
			if item.Name != "" && !slices.Contains(tags, item.Name) {
				tags = append(tags, item.Name)
			}
		}
	}
	genre := mapString(vd, "category_name", "categoryName", "category")
	if genre == "" && len(tags) > 0 {
		genre = tags[0]
	}
	return Drama{ID: providerDramaID(sourceHongguo, sourceID), Source: sourceHongguo, SourceID: sourceID, Title: title, Name: title, Desc: intro, Intro: intro, Cover: cover, CoverURL: cover, CategoryName: firstNonEmpty(genre, category), ChannelName: "红果", Remark: remark, TotalEpisode: count, EpisodeCount: count, Tags: tags, ReleaseStatus: releaseStatus, Score: mapString(vd, "score"), Views: mapString(vd, "series_play_cnt", "play_cnt"), Heat: hongguoHeat(vd), OnlineDate: hongguoOnlineDate(vd, time.Now())}
}

// nestedMap 沿给定字段路径取得嵌套映射。
func nestedMap(v any, keys ...string) map[string]any {
	cur, _ := v.(map[string]any)
	for _, key := range keys {
		if cur == nil {
			return nil
		}
		cur, _ = cur[key].(map[string]any)
	}
	return cur
}

// routerLoaderMap 从页面路由数据中取得加载器映射。
func routerLoaderMap(data map[string]any, names ...string) map[string]any {
	loader, _ := data["loaderData"].(map[string]any)
	for _, name := range names {
		if page, _ := loader[name].(map[string]any); len(page) > 0 {
			return page
		}
	}
	for key, value := range loader {
		for _, name := range names {
			if strings.TrimSuffix(name, "$") != "" && strings.HasPrefix(key, strings.TrimSuffix(name, "$")) {
				if page, _ := value.(map[string]any); len(page) > 0 {
					return page
				}
			}
		}
	}
	return nil
}

// anyList 将通用响应值提取为列表。
func anyList(v any) []any {
	switch x := v.(type) {
	case []any:
		return x
	case map[string]any:
		for _, key := range []string{"list", "items", "data"} {
			if out := anyList(x[key]); len(out) > 0 {
				return out
			}
		}
	}
	return nil
}
