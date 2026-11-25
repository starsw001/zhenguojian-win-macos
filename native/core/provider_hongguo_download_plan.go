package core

import (
	"context"
	"net/url"
	"strconv"
	"time"
)

func (c *hongguoMediaCache) epoch() uint64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.generation
}

func (choice nativePlaybackChoice) hongguoMatches(drama string, index int, chapter Chapter) bool {
	series, video, ok := hongguoPlaybackIDs(Task{DramaID: drama, Index: index, Chapter: chapter})
	return ok && choice.source == sourceHongguo && choice.dramaID == drama && choice.episodeIndex == index && choice.danmakuSeries == series && choice.danmakuVideo == video
}

func hongguoReusableDownloadAddress(m providerMedia, issued, now time.Time) bool {
	if issued.IsZero() || issued.After(now) || now.Sub(issued) >= 10*time.Minute || m.pending != nil || m.Playlist != "" || len(m.CENCKey) > 0 || len(m.HLSKey) > 0 {
		return false
	}
	raw, err := hongguoIssuedMediaURL(m.URL)
	if err != nil || raw != m.URL {
		return false
	}
	u, err := url.Parse(raw)
	if err != nil {
		return false
	}
	expires := time.Time{}
	if creds := m.credentials; creds != nil {
		if creds.cookie != "" || creds.cdnAuthKey != "" || creds.cdnAuthValue != "" || creds.cdnApp != "" || creds.cdnM3U8 || creds.cdnOther || creds.origin != providerMediaOrigin(u) {
			return false
		}
		expires = creds.expires
	}
	values, err := url.ParseQuery(u.RawQuery)
	if err != nil {
		return false
	}
	for _, field := range []string{"expire", "expires", "expire_at", "x-expires", "deadline"} {
		for _, v := range values[field] {
			n, err := strconv.ParseInt(v, 10, 64)
			if err != nil || n < 1_000_000_000 || n > 9_999_999_999 {
				return false
			}
			at := time.Unix(n, 0)
			if expires.IsZero() || at.Before(expires) {
				expires = at
			}
		}
	}
	if expires.IsZero() {
		return now.Sub(issued) < 15*time.Second
	}
	return now.Add(2 * time.Second).Before(expires)
}

func (engine *nativeEngine) hongguoDownloadFromPlayback(ctx context.Context, job nativeDownloadJob) (providerMedia, bool, error) {
	if err := ctx.Err(); err != nil {
		return providerMedia{}, false, err
	}
	engine.mu.Lock()
	choice, ok := engine.playbacks[job.hongguoPlaybackSession]
	stream := engine.stream
	engine.mu.Unlock()
	if !ok || !choice.hongguoMatches(job.Drama.ID, job.Index, job.Chapter) || !choice.guardAccess || choice.checks == nil || choice.probeContext == nil || choice.probeContext.Err() != nil || stream == nil {
		return providerMedia{}, false, nil
	}
	choice.checks.mu.Lock()
	blocked := choice.checks.blocked
	choice.checks.mu.Unlock()
	if blocked {
		return providerMedia{}, false, errNativeMediaAccessStop
	}
	stream.mu.Lock()
	active := stream.sessions[choice.streamSession]
	stream.mu.Unlock()
	if active == nil {
		return providerMedia{}, false, nil
	}
	if err := active.routeAccessError(); err != nil {
		return providerMedia{}, false, err
	}
	now := time.Now()
	var routes []providerMedia
	selected, quality := job.PreferredRouteKey == "", job.Quality <= 0
	for _, m := range choice.media {
		if !hongguoReusableDownloadAddress(m, choice.hongguoResolvedAt, now) {
			continue
		}
		selected = selected || nativeRouteKey(m) == job.PreferredRouteKey
		quality = quality || job.Quality == m.Quality
		routes = append(routes, m)
	}
	if len(routes) == 0 || !selected || !quality || choice.probeContext.Err() != nil || choice.hongguoGeneration != engine.downloader.hongguoMediaCache.epoch() {
		return providerMedia{}, false, nil
	}
	if err := ctx.Err(); err != nil {
		return providerMedia{}, false, err
	}
	return mergeHongguoMediaRoutes(routes), true, nil
}
