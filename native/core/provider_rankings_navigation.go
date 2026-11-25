package core

import (
	"context"
	"encoding/json"
	"errors"
	"golang.org/x/net/html"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

var hongguoRankRoute = regexp.MustCompile(`^[a-z][a-z-]{1,64}$`)

type hongguoRankingGroup struct{ Name, Path, ID string }
type storedHongguoRankingBoard struct {
	Board rankingBoard `json:"board"`
	Path  string       `json:"path"`
}
type storedHongguoRankingCatalog struct {
	FetchedAt time.Time                   `json:"fetchedAt"`
	Boards    []storedHongguoRankingBoard `json:"boards"`
}

func hongguoRankingSpec(path string) (rankingBoard, bool) {
	if !hongguoRankRoute.MatchString(path) {
		return rankingBoard{}, false
	}
	for _, group := range []struct{ suffix, id, key string }{{"comic-drama", "comic", "comic"}, {"real-drama", "real", "real"}, {"ai-drama", "ai", "ai"}, {"drama", "all", "hongguo"}} {
		if !strings.HasSuffix(path, "-"+group.suffix) {
			continue
		}
		kind := strings.TrimSuffix(path, "-"+group.suffix)
		keys := map[string]string{"hot": "", "prestige": "prestige", "subscribe": "subscribe", "new": "new", "hot-search": "hot_search", "must-watch": "must_watch", "followed": "followed"}
		key, ok := keys[kind]
		if !ok {
			return rankingBoard{}, false
		}
		id := "hongguo-web-" + path
		if kind == "hot" {
			if group.id == "all" {
				id = "hongguo-hot"
			} else {
				id = "hongguo-" + group.id
			}
		}
		upstream := group.key
		if key != "" {
			upstream += "-" + key
		}
		return rankingBoard{ID: id, Source: sourceHongguo, CategoryID: group.id, path: path, upstreamKey: upstream}, true
	}
	return rankingBoard{}, false
}

func hongguoRankingLink(raw string) (string, bool) {
	u, err := url.Parse(raw)
	if err != nil || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.RawPath != "" {
		return "", false
	}
	if u.Host != "" && (u.Scheme != "https" || u.Port() != "" || u.Hostname() != "hongguoduanju.com" && u.Hostname() != "www.hongguoduanju.com") {
		return "", false
	}
	if u.Host == "" && u.Scheme != "" {
		return "", false
	}
	path := strings.TrimPrefix(u.Path, "/rank/")
	if path == u.Path {
		return "", false
	}
	_, ok := hongguoRankingSpec(path)
	return path, ok
}

func hongguoRankingNav(document *html.Node, label string) []*html.Node {
	for _, nav := range providerHTMLNodes(document, func(n *html.Node) bool { return n.Data == "nav" && providerHTMLAttr(n, "aria-label") == label }) {
		links := providerHTMLNodes(nav, func(n *html.Node) bool { return n.Data == "a" })
		if len(links) > 0 {
			return links
		}
	}
	return nil
}

func parseHongguoRankingGroups(body string) ([]hongguoRankingGroup, error) {
	if len(body) > 4<<20 {
		return nil, errHongguoRankingFormat
	}
	doc, err := html.Parse(strings.NewReader(body))
	if err != nil {
		return nil, err
	}
	groups := []hongguoRankingGroup{}
	seen := map[string]bool{}
	for _, a := range hongguoRankingNav(doc, "榜单分类") {
		path, ok := hongguoRankingLink(providerHTMLAttr(a, "href"))
		if !ok {
			return nil, errHongguoRankingFormat
		}
		board, _ := hongguoRankingSpec(path)
		if !strings.HasPrefix(path, "hot-") || strings.HasPrefix(path, "hot-search-") {
			return nil, errHongguoRankingFormat
		}
		name := strings.Join(strings.Fields(providerHTMLText(a)), "")
		if len(name)%2 == 0 && name[:len(name)/2] == name[len(name)/2:] {
			name = name[:len(name)/2]
		}
		if name == "" || len([]rune(name)) > 12 || seen[board.CategoryID] {
			return nil, errHongguoRankingFormat
		}
		seen[board.CategoryID] = true
		groups = append(groups, hongguoRankingGroup{Name: name, Path: path, ID: board.CategoryID})
	}
	if len(groups) == 0 || len(groups) > 8 {
		return nil, errors.New("红果未返回有效榜单分类，请稍后刷新")
	}
	return groups, nil
}

func parseHongguoRankingTypes(body string, group hongguoRankingGroup) ([]rankingBoard, error) {
	if len(body) > 4<<20 {
		return nil, errHongguoRankingFormat
	}
	doc, err := html.Parse(strings.NewReader(body))
	if err != nil {
		return nil, err
	}
	for _, n := range providerHTMLNodes(doc, func(n *html.Node) bool { return n.Data == "link" && providerHTMLAttr(n, "rel") == "canonical" }) {
		path, ok := hongguoRankingLink(providerHTMLAttr(n, "href"))
		if !ok || path != group.Path {
			return nil, errHongguoRankingFormat
		}
	}
	boards := []rankingBoard{}
	seen := map[string]bool{}
	for _, a := range hongguoRankingNav(doc, "榜单类型") {
		path, ok := hongguoRankingLink(providerHTMLAttr(a, "href"))
		if !ok {
			return nil, errHongguoRankingFormat
		}
		board, _ := hongguoRankingSpec(path)
		name := strings.TrimSpace(strings.TrimPrefix(providerHTMLText(a), group.Name+"/"))
		if board.CategoryID != group.ID || name == "" || len([]rune(name)) > 20 || seen[path] {
			return nil, errHongguoRankingFormat
		}
		seen[path] = true
		board.Name = name
		board.CategoryName = group.Name
		board.Description = "红果 · " + group.Name + " · " + name + "；按源站顺序更新。"
		boards = append(boards, board)
	}
	if len(boards) == 0 || len(boards) > 16 || !seen[group.Path] {
		return nil, errors.New("红果未返回有效子榜单，请稍后刷新")
	}
	return boards, nil
}

func (d *Downloader) readHongguoRankingCatalogLocked() {
	cache := &d.rankings
	cache.boardsLoaded = true
	if d.cfg.dataDir == "" {
		return
	}
	body, err := os.ReadFile(filepath.Join(d.cfg.dataDir, "ranking-navigation.json"))
	if err != nil || len(body) > 128<<10 {
		return
	}
	var saved storedHongguoRankingCatalog
	if json.Unmarshal(body, &saved) != nil || time.Since(saved.FetchedAt) < 0 || time.Since(saved.FetchedAt) > 24*time.Hour || len(saved.Boards) == 0 || len(saved.Boards) > 128 {
		return
	}
	boards := []rankingBoard{}
	seen := map[string]bool{}
	for _, entry := range saved.Boards {
		spec, ok := hongguoRankingSpec(entry.Path)
		b := entry.Board
		if !ok || b.ID != spec.ID || b.Source != sourceHongguo || b.CategoryID != spec.CategoryID || b.Name == "" || len([]rune(b.Name)) > 20 || b.CategoryName == "" || len([]rune(b.CategoryName)) > 12 || seen[b.ID] {
			return
		}
		spec.Name, spec.CategoryName, spec.Description = b.Name, b.CategoryName, b.Description
		boards = append(boards, spec)
		seen[b.ID] = true
	}
	cache.boards, cache.boardsFetchedAt = boards, saved.FetchedAt
}

func (d *Downloader) loadHongguoRankingBoards(ctx context.Context, force bool) ([]rankingBoard, error) {
	cache := &d.rankings
	cache.mu.Lock()
	if !cache.boardsLoaded {
		d.readHongguoRankingCatalogLocked()
	}
	if !force && len(cache.boards) > 0 && time.Since(cache.boardsFetchedAt) >= 0 && time.Since(cache.boardsFetchedAt) < rankingCacheTTL {
		boards := append([]rankingBoard{}, cache.boards...)
		cache.mu.Unlock()
		return boards, nil
	}
	if pending := cache.boardsPending; pending != nil {
		cache.mu.Unlock()
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-pending:
			return d.loadHongguoRankingBoards(ctx, false)
		}
	}
	cache.boardsPending = make(chan struct{})
	cache.mu.Unlock()
	boards, err := d.fetchHongguoRankingBoards(ctx)
	cache.mu.Lock()
	defer cache.mu.Unlock()
	defer close(cache.boardsPending)
	defer func() { cache.boardsPending = nil }()
	if err == nil {
		cache.boards, cache.boardsFetchedAt = boards, time.Now()
		saved := storedHongguoRankingCatalog{FetchedAt: cache.boardsFetchedAt}
		for _, b := range boards {
			saved.Boards = append(saved.Boards, storedHongguoRankingBoard{Board: b, Path: b.path})
		}
		if d.cfg.dataDir != "" {
			if body, e := json.Marshal(saved); e == nil {
				_ = writeNativeCacheFile(filepath.Join(d.cfg.dataDir, "ranking-navigation.json"), body)
			}
		}
		return append([]rankingBoard{}, boards...), nil
	}
	if len(cache.boards) > 0 && time.Since(cache.boardsFetchedAt) >= 0 && time.Since(cache.boardsFetchedAt) < 24*time.Hour {
		cached := append([]rankingBoard{}, cache.boards...)
		for i := range cached {
			cached[i].Description += " 分类刷新失败，暂用最近取得的分类。"
		}
		return cached, nil
	}
	return nil, err
}

func (d *Downloader) fetchHongguoRankingBoards(ctx context.Context) ([]rankingBoard, error) {
	ctx, cancel := context.WithTimeout(ctx, 35*time.Second)
	defer cancel()
	ctx = context.WithValue(ctx, providerTextNoCacheKey{}, true)
	base := d.providerBaseURL(sourceHongguo)
	body, err := d.fetchProviderText(ctx, base+"/rank/hot-drama", base+"/")
	if err != nil {
		return nil, err
	}
	groups, err := parseHongguoRankingGroups(body)
	if err != nil {
		return nil, err
	}
	boards := []rankingBoard{}
	seen := map[string]bool{}
	for _, group := range groups {
		page := body
		if group.Path != "hot-drama" {
			page, err = d.fetchProviderText(ctx, base+"/rank/"+group.Path, base+"/rank/hot-drama")
			if err != nil {
				return nil, err
			}
		}
		entries, e := parseHongguoRankingTypes(page, group)
		if e != nil {
			return nil, e
		}
		for _, b := range entries {
			if seen[b.ID] {
				return nil, errHongguoRankingFormat
			}
			seen[b.ID] = true
			boards = append(boards, b)
		}
	}
	return boards, nil
}

func (d *Downloader) availableRankingBoards(ctx context.Context) []rankingBoard {
	boards := []rankingBoard{}
	if nativeSourceAvailable(sourceHongguo) {
		live, err := d.loadHongguoRankingBoards(ctx, false)
		if err == nil {
			boards = append(boards, live...)
		} else {
			for _, b := range rankingBoards {
				if b.Source == sourceHongguo {
					b.Description += " 分类获取失败，暂仅保留热播入口；请稍后重试。"
					boards = append(boards, b)
				}
			}
		}
	}
	for _, b := range rankingBoards {
		if b.Source != sourceHongguo && nativeSourceAvailable(b.Source) {
			boards = append(boards, b)
		}
	}
	return boards
}
