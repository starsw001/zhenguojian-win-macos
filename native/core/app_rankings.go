package core

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"time"
)

// loadRankingCache 加载排行榜缓存。
func (d *Downloader) loadRankingCache() {
	cache := &d.rankings
	cache.pages, cache.pending = map[string]rankingPage{}, map[string]*rankingCall{}
	file, err := os.Open(filepath.Join(d.cfg.dataDir, "rankings.json"))
	if err != nil {
		return
	}
	defer file.Close()
	limit := d.cacheBytes("rankingMiB")
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() > int64(limit) {
		return
	}
	body, err := io.ReadAll(io.LimitReader(file, int64(limit)+1))
	if err != nil || len(body) > limit {
		return
	}
	var pages map[string]rankingPage
	if json.Unmarshal(body, &pages) != nil {
		return
	}
	for key, page := range pages {
		board, valid := findRankingBoard(page.BoardID)
		age := time.Since(page.FetchedAt)
		if !valid || !nativeSourceAvailable(board.Source) || page.Page < 1 || page.Page > 500 ||
			key != page.BoardID+":"+strconv.Itoa(page.Page) || age < 0 || age >= 24*time.Hour || len(page.Items) > 100 {
			continue
		}
		valid = true
		for _, item := range page.Items {
			if sourceFromDramaID(item.Drama.ID) != board.Source || item.Rank < 1 {
				valid = false
				break
			}
		}
		if valid && len(cache.pages) < d.cacheLimit("rankingPages") {
			cache.pages[key] = page
		}
	}
}

// writeRankingCacheLocked 写入排行榜缓存，供内部持锁路径调用。
func (d *Downloader) writeRankingCacheLocked() {
	if d.cfg.dataDir == "" {
		return
	}
	for len(d.rankings.pages) > d.cacheLimit("rankingPages") {
		d.evictOldestRankingLocked()
	}
	body, err := json.Marshal(d.rankings.pages)
	for err == nil && len(body) > d.cacheBytes("rankingMiB") && len(d.rankings.pages) > 0 {
		d.evictOldestRankingLocked()
		body, err = json.Marshal(d.rankings.pages)
	}
	if err == nil {
		_ = writeNativeCacheFile(filepath.Join(d.cfg.dataDir, "rankings.json"), body)
	}
}

// evictOldestRankingLocked 处理站源下载器的evictOldest排行榜操作，供内部持锁路径调用。
func (d *Downloader) evictOldestRankingLocked() {
	oldestKey := ""
	oldest := time.Time{}
	for key, page := range d.rankings.pages {
		if oldestKey == "" || page.FetchedAt.Before(oldest) {
			oldestKey, oldest = key, page.FetchedAt
		}
	}
	delete(d.rankings.pages, oldestKey)
}

// nativeRanking 处理榜单读取请求并维护相应缓存。
func (engine *nativeEngine) nativeRanking(ctx context.Context, input nativeInput) (any, error) {
	board, found := findRankingBoard(input.Board)
	if !found || !nativeSourceAvailable(board.Source) {
		return nil, errNativeBuildSource
	}
	if input.Page < 1 || input.Page > 500 {
		return nil, errors.New("榜单页码无效")
	}
	page, err := engine.downloader.loadRankingPage(ctx, board, input.Page, input.Force)
	if err != nil {
		return nil, err
	}
	type item struct {
		Rank   int         `json:"rank"`
		Drama  nativeDrama `json:"drama"`
		Metric string      `json:"metric,omitempty"`
	}
	items := make([]item, 0, len(page.Items))
	engine.mu.Lock()
	defer engine.mu.Unlock()
	library := map[string]nativeDrama{}
	for _, drama := range engine.catalogs[board.Source] {
		library[drama.ID] = drama
	}
	for _, row := range page.Items {
		drama := nativeNormalize(row.Drama)
		if old, found := library[drama.ID]; found {
			drama = mergeNativeDrama(old, drama)
		}
		items = append(items, item{Rank: row.Rank, Drama: drama, Metric: row.Metric})
	}
	return map[string]any{"boardId": page.BoardID, "page": page.Page, "items": items,
		"hasMore": page.HasMore, "totalPages": page.TotalPages, "updatedText": page.UpdatedText,
		"fetchedAt": page.FetchedAt, "stale": page.Stale, "warning": page.Warning}, nil
}
