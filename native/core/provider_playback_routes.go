package core

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const nativePlaybackRouteLimit = 128

// explicitRouteEpisode 从线路信息中读取明确的分集编号。
func explicitRouteEpisode(title string) int {
	number, err := strconv.Atoi(strings.TrimSpace(title))
	if err == nil && number > 0 && number <= 100000 {
		return number
	}
	return episodeIndex(title, 0)
}

// sameEpisodeRoute 判断备用线路是否明确指向同一作品的同一集。
func sameEpisodeRoute(primary, alternate Chapter) bool {
	if primary.VIP || alternate.VIP || primary.Source != alternate.Source {
		return false
	}
	number, err := strconv.Atoi(primary.EpisodeString(0))
	if err != nil || number <= 0 || alternate.EpisodeString(0) != primary.EpisodeString(0) ||
		explicitRouteEpisode(primary.Title) != number || explicitRouteEpisode(alternate.Title) != number {
		return false
	}
	a, errA := url.Parse(primary.PageURL)
	b, errB := url.Parse(alternate.PageURL)
	if errA != nil || errB != nil || !isProviderHTTPMediaURL(primary.PageURL) ||
		!isProviderHTTPMediaURL(alternate.PageURL) || a.User != nil || b.User != nil ||
		providerMediaOrigin(a) != providerMediaOrigin(b) {
		return false
	}
	ownerA, lineA, okA := maccmsEpisodeRoute(primary.PageURL)
	ownerB, lineB, okB := maccmsEpisodeRoute(alternate.PageURL)
	return okA && okB && ownerA == ownerB && lineA != lineB
}

// attachChapterRoutes 为分集关联满足同集约束的候选线路。
func attachChapterRoutes(primary, alternatives []Chapter) []Chapter {
	grouped := map[string][]Chapter{}
	for _, alternate := range alternatives {
		grouped[alternate.EpisodeString(0)] = append(grouped[alternate.EpisodeString(0)], alternate)
	}
	for i := range primary {
		seen := map[string]bool{primary[i].PageURL: true}
		for _, alternate := range grouped[primary[i].EpisodeString(0)] {
			if len(primary[i].Alternatives) >= nativePlaybackRouteLimit-1 {
				break
			}
			if seen[alternate.PageURL] || !sameEpisodeRoute(primary[i], alternate) {
				continue
			}
			seen[alternate.PageURL] = true
			alternate.Alternatives = nil
			primary[i].Alternatives = append(primary[i].Alternatives, alternate)
		}
	}
	return primary
}

// resolvePlaybackMedia 解析当前播放候选并返回媒体信息。
func (d *Downloader) resolvePlaybackMedia(ctx context.Context, task Task) (providerMedia, error) {
	if len(task.Chapter.Alternatives) == 0 {
		return d.resolveProviderMedia(ctx, task)
	}
	if len(task.Chapter.Alternatives) >= nativePlaybackRouteLimit {
		return providerMedia{}, errors.New("同集备用线路过多，请刷新详情")
	}
	var options []providerMedia
	for i, chapter := range append([]Chapter{task.Chapter}, task.Chapter.Alternatives...) {
		if i > 0 && (!sameEpisodeRoute(task.Chapter, chapter) || len(chapter.Alternatives) != 0) {
			return providerMedia{}, errors.New("备用线路与当前作品或分集不符")
		}
		if i > 0 {
			source, id, valid := splitProviderDramaID(task.DramaID)
			if !valid || !strings.HasPrefix(chapter.ID, providerChapterID(source, id, "")) {
				return providerMedia{}, errors.New("备用线路不属于当前作品")
			}
		}
		pending := task
		chapter.Alternatives = nil
		pending.Chapter = chapter
		options = append(options, providerMedia{URL: firstNonEmpty(chapter.PageURL, chapter.VideoURL), Referer: chapter.Referer, pending: &pending})
	}
	media := options[0]
	media.Variants = options[1:]
	return media, nil
}

// materializePlaybackMedia 将延迟解析的候选转换为具体媒体信息。
func (d *Downloader) materializePlaybackMedia(ctx context.Context, media providerMedia) (providerMedia, error) {
	if media.pending == nil {
		return media, nil
	}
	resolved, err := d.resolveProviderMedia(ctx, *media.pending)
	if err != nil {
		return providerMedia{}, err
	}
	resolved.pending = nil
	return resolved, nil
}

// nativeRouteMustStop 判断当前错误是否要求停止候选地址回退。
func nativeRouteMustStop(err error) bool {
	var backoff *requestBackoff
	return errors.Is(err, errNativeMediaAccessStop) || errors.As(err, &backoff) ||
		errors.Is(err, context.Canceled)
}

type nativeResolvedRoute struct {
	media providerMedia
	at    time.Time
}

// resolveLocked 解析候选线路检查缓存，供内部持锁路径调用。
func (checks *nativeRouteChecks) resolveLocked(ctx context.Context, d *Downloader, media providerMedia, index int) (providerMedia, error) {
	if checks.blocked {
		return providerMedia{}, fmt.Errorf("%w，候选检测已停止，请稍后重试", errNativeMediaAccessStop)
	}
	if media.pending == nil {
		return media, nil
	}
	if cached, ok := checks.resolved[index]; ok && time.Since(cached.at) < 90*time.Second &&
		(cached.media.credentials == nil || cached.media.credentials.expires.IsZero() || time.Now().Before(cached.media.credentials.expires)) {
		return cached.media, nil
	}
	resolved, err := d.materializePlaybackMedia(ctx, media)
	if err != nil {
		if nativeRouteMustStop(err) && !errors.Is(err, context.Canceled) {
			checks.blocked = true
		}
		return providerMedia{}, err
	}
	if checks.resolved == nil {
		checks.resolved = map[int]nativeResolvedRoute{}
	}
	checks.resolved[index] = nativeResolvedRoute{media: resolved, at: time.Now()}
	return resolved, nil
}
