package core

import (
	"context"
	"errors"
	"fmt"
	"html"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
)

type nativeCategory struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Kind string `json:"kind,omitempty"`
}

const nativeCategoryMaxEntries = 10000

var nativeAICategories = []nativeCategory{
	{ID: "ai-duanju", Name: "AI 短剧"},
	{ID: "ai-manju", Name: "AI 漫剧"},
	{ID: "ai-huanlian", Name: "AI 换脸"},
	{ID: "ai-mogai", Name: "AI 魔改"},
}

// nativeCatalogKey 构造站源与分类对应的目录缓存键。
func nativeCatalogKey(source, category string) string {
	source = canonicalProviderSource(source)
	if category = strings.TrimSpace(category); category != "" {
		return source + "|" + category
	}
	return source
}

// validNativeCategory 检查分类值是否符合当前站源的分类规则。
func validNativeCategory(source, category string) bool {
	if category == "" {
		return true
	}
	if len(category) > 128 || strings.ContainsAny(category, "|/\\\x00\r\n") {
		return false
	}
	switch source {
	case sourceHongguo:
		if _, _, _, valid := hongguoWebCategory(category); valid {
			return true
		}
		for _, genre := range hongguoAppGenres {
			if category == genre.key {
				return true
			}
		}
	case sourceHuangguoAI:
		if validHuangguoAITagCategory(category) {
			return true
		}
		if huangguoAICategorySlug.MatchString(category) {
			return true
		}
		for _, entry := range nativeAICategories {
			if category == entry.ID {
				return true
			}
		}
	case sourceHuangguoVideo:
		id, err := strconv.Atoi(category)
		return err == nil && id > 0
	case sourceCloudFront:
		return strings.TrimSpace(category) == category
	case sourceHuangju:
		return category == huangjuNewestCategory || validHuangjuID(category) && !strings.HasPrefix(category, "@")
	case sourceYeguo:
		return validYeguoCategory(category)
	case sourceDSD:
		return webProviderNumericID.MatchString(category)
	case sourceSorani:
		return validSoraniCategory(category)
	case sourceGuipian:
		return validGuipianCategory(category)
	case sourceHanxiaoquan:
		return validHanxiaoquanCategory(category)
	}
	if isDuanjuProviderSource(source) {
		return validDuanjuCategory(source, category)
	}
	return false
}

// nativeCategories 返回原生分类列表。
func (engine *nativeEngine) nativeCategories(ctx context.Context, source string, force bool) ([]nativeCategory, error) {
	source = canonicalProviderSource(source)
	if force && ordinarySource(source) {
		ctx = context.WithValue(ctx, providerTextNoCacheKey{}, true)
	}
	all := []nativeCategory{{Name: "全部"}}
	switch source {
	case sourceHongguo:
		all, err := engine.downloader.fetchHongguoWebCategories(ctx)
		if err == nil {
			engine.mu.Lock()
			if engine.categoryOptions == nil {
				engine.categoryOptions = map[string][]nativeCategory{}
			}
			engine.categoryOptions[source] = append([]nativeCategory{}, all...)
			engine.mu.Unlock()
		}
		return all, err
	case sourceHuangdou, sourceGuanguo, sourceWangguo, sourceFaguo:
		return all, nil
	}
	engine.mu.Lock()
	cached := append([]nativeCategory{}, engine.categoryOptions[source]...)
	engine.mu.Unlock()
	if source == sourceHuangju {
		cached = cleanHuangjuCategories(cached)
	}
	if len(cached) > 1 && !force && source != sourceHuangguoAI {
		return cached, nil
	}
	d := engine.downloader
	var err error
	switch source {
	case sourceHuangguoAI:
		var tags []nativeCategory
		tags, err = d.fetchHuangguoAIPopularTags(ctx)
		all = append(all, d.huangguoAICatalogCategories()...)
		all = append(all, tags...)
	case sourceCloudFront:
		var tabs legacyTabList
		err = d.fetchAPI(ctx, "/api/app/playlet-tab/all", nil, &tabs)
		for _, tab := range tabs {
			if validNativeCategory(source, tab.ID) && tab.ID != "" && tab.Name != "" {
				all = append(all, nativeCategory{ID: tab.ID, Name: tab.Name})
			}
		}
	case sourceHuangguoVideo:
		address := d.providerBaseURL(source) + "/videos"
		var body string
		body, err = d.fetchProviderText(ctx, address, d.providerBaseURL(source)+"/")
		if err == nil {
			all = append(all, parseHuangguoVideoCategories(body)...)
		}
	case sourceHuangju:
		var categories []nativeCategory
		categories, err = d.fetchHuangjuCategories(ctx)
		if err == nil {
			all = append(all, categories...)
		}
	case sourceYeguo:
		var categories []nativeCategory
		categories, err = d.fetchYeguoCategories(ctx)
		if err == nil {
			all = append(all, categories...)
		}
	case sourceDSD:
		var categories []nativeCategory
		categories, err = d.fetchDSDCategories(ctx, force)
		if err == nil {
			all = append(all, categories...)
		}
	case sourceSorani:
		all = append(all, d.fetchSoraniCategories()...)
	case sourceGuipian:
		all = append(all, d.fetchGuipianCategories()...)
	case sourceHanxiaoquan:
		all = append(all, d.fetchHanxiaoquanCategories()...)
	default:
		if isDuanjuProviderSource(source) {
			var categories []nativeCategory
			categories, err = d.fetchDuanjuCategories(ctx, source)
			if err == nil {
				all = append(all, categories...)
			}
			break
		}
		return nil, errors.New("请选择有效站源")
	}
	if err != nil {
		return nil, err
	}
	if len(all) == 1 {
		return nil, errors.New("站源暂未返回内容分类，请稍后重试")
	}
	engine.mu.Lock()
	if engine.categoryOptions == nil {
		engine.categoryOptions = map[string][]nativeCategory{}
	}
	engine.categoryOptions[source] = all
	engine.writeCatalogDiskLocked()
	engine.mu.Unlock()
	return all, nil
}

var categoryAnchorPattern = regexp.MustCompile(`(?is)<a\b([^>]*)>(.*?)</a>`)
var categoryTextPattern = regexp.MustCompile(`(?s)<[^>]*>`)

// parseHuangguoVideoCategories 解析黄果视频分类列表。
func parseHuangguoVideoCategories(body string) []nativeCategory {
	var categories []nativeCategory
	seen := map[string]bool{}
	for _, match := range categoryAnchorPattern.FindAllStringSubmatch(body, -1) {
		href := html.UnescapeString(extractAttr("<a "+match[1]+">", "href"))
		address, err := url.Parse(href)
		if err != nil || address.Path != "" && strings.TrimRight(address.Path, "/") != "/videos" {
			continue
		}
		id := address.Query().Get("category")
		name := strings.TrimSpace(html.UnescapeString(categoryTextPattern.ReplaceAllString(match[2], "")))
		if !validNativeCategory(sourceHuangguoVideo, id) || id == "" || seen[id] || name == "" || len([]rune(name)) > 24 {
			continue
		}
		seen[id] = true
		categories = append(categories, nativeCategory{ID: id, Name: name})
	}
	return categories
}

// fetchHuangguoAICatalogPage 获取黄果AI目录页。
func (d *Downloader) fetchHuangguoAICatalogPage(ctx context.Context, page int, category string) ([]Drama, bool, error) {
	ctx = context.WithValue(ctx, providerTextNoCacheKey{}, true)
	ctx, _ = d.huangguoAICatalogContext(ctx)
	if validHuangguoAITagCategory(category) {
		return d.fetchHuangguoAITagPage(ctx, page, category)
	}
	categories := d.huangguoAICatalogCategories()
	if category != "" {
		found := false
		for _, entry := range categories {
			if entry.ID == category {
				categories = []nativeCategory{entry}
				found = true
				break
			}
		}
		if !found {
			return nil, false, errors.New("当前站点没有此分类，请刷新分类列表")
		}
	}
	type categoryResult struct {
		items []Drama
		more  bool
		err   error
	}
	results := make([]categoryResult, len(categories))
	slots := make(chan struct{}, 2)
	var group sync.WaitGroup
	for index, entry := range categories {
		group.Add(1)
		go func(index int, entry nativeCategory) {
			defer group.Done()
			select {
			case slots <- struct{}{}:
				defer func() { <-slots }()
			case <-ctx.Done():
				results[index].err = ctx.Err()
				return
			}
			var err error
			results[index].items, results[index].more, err = d.fetchHuangguoAISectionPage(ctx, page, entry)
			if err != nil {
				results[index].err = fmt.Errorf("%s: %w", entry.Name, err)
			}
		}(index, entry)
	}
	group.Wait()
	var items []Drama
	var failures []error
	more := false
	for _, result := range results {
		items = append(items, result.items...)
		more = more || result.more || result.err != nil
		if result.err != nil {
			failures = append(failures, result.err)
		}
	}
	return items, more, errors.Join(failures...)
}
