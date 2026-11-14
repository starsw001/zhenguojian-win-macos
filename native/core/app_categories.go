package core

import (
	"context"
	"errors"
	"html"
	"net/url"
	"regexp"
	"strconv"
	"strings"
)

type nativeCategory struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Kind string `json:"kind,omitempty"`
}

const nativeCategoryMaxEntries = 10000

var nativeAICategories = []nativeCategory{
	{ID: "ai-duanju", Name: "短剧"},
	{ID: "ai-manju", Name: "漫剧"},
}

var nativeAIWebsiteCategories = []nativeCategory{
	{ID: "original", Name: "黄果原创"},
	{ID: "ai-duanju", Name: "短剧"},
	{ID: "ai-manju", Name: "漫剧"},
	{ID: "topics", Name: "专题"},
	{ID: "ranks-hot", Name: "排行榜"},
}

var nativeAIWebsiteSubcategories = []nativeCategory{
	{ID: "original-manju", Name: "原创漫剧"},
	{ID: "original-duanju", Name: "原创短剧"},
	{ID: "ai-duanju-hot", Name: "当前热播"},
	{ID: "ai-duanju-random", Name: "精选推荐"},
	{ID: "ai-manju-hot", Name: "当前热播"},
	{ID: "ai-manju-random", Name: "精选推荐"},
	{ID: "ranks-recommend", Name: "推荐榜"},
	{ID: "ranks-potential", Name: "飙升榜"},
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
		if validHuangguoAITopicCategory(category) {
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
		for _, entry := range nativeAIWebsiteCategories {
			if category == entry.ID {
				return true
			}
		}
		for _, entry := range nativeAIWebsiteSubcategories {
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
		var topics, tags []nativeCategory
		all = append(all, d.huangguoAICatalogCategories()...)
		topics, err = d.fetchHuangguoAITopics(ctx)
		if err == nil {
			all = append(all, topics...)
			tags, err = d.fetchHuangguoAIPopularTags(ctx)
		}
		if err == nil {
			all = append(all, tags...)
		}
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
	if _, _, valid := huangguoAIWebsiteCategory(category); valid {
		return d.fetchHuangguoAIWebsitePage(ctx, page, category)
	}
	return nil, false, errors.New("当前站点没有此分类，请刷新分类列表")
}
