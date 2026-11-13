package core

import (
	"context"
	"errors"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"unicode"

	"golang.org/x/net/html"
)

var huangguoAITagSlug = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,95}$`)

// validHuangguoAITagCategory 判断是否有效：黄果AI标签分类。
func validHuangguoAITagCategory(category string) bool {
	return strings.HasPrefix(category, "tag:") && huangguoAITagSlug.MatchString(strings.TrimPrefix(category, "tag:"))
}

// parseHuangguoAIPopularTags 解析黄果AI热门标签列表。
func parseHuangguoAIPopularTags(document *html.Node, pageURL string) []nativeCategory {
	section := providerHTMLFirstClass(document, "hg-home-popular-tags")
	base, err := url.Parse(pageURL)
	if err != nil || section == nil {
		return nil
	}
	seen := map[string]bool{}
	var tags []nativeCategory
	for _, anchor := range providerHTMLNodes(section, func(node *html.Node) bool {
		return node.Type == html.ElementNode && node.Data == "a" && providerHTMLClass(node, "hg-tagrow__item")
	}) {
		reference, err := url.Parse(providerHTMLAttr(anchor, "href"))
		if err != nil || reference.User != nil || reference.RawQuery != "" || reference.Fragment != "" {
			continue
		}
		address := base.ResolveReference(reference)
		if address.Scheme != base.Scheme || !strings.EqualFold(address.Host, base.Host) {
			continue
		}
		parts := strings.Split(strings.Trim(address.Path, "/"), "/")
		if len(parts) != 2 || parts[0] != "tag" || !huangguoAITagSlug.MatchString(parts[1]) {
			continue
		}
		name := providerHTMLText(anchor)
		id := "tag:" + parts[1]
		if seen[id] || name == "" || len([]rune(name)) > 48 || strings.ContainsFunc(name, unicode.IsControl) {
			continue
		}
		seen[id] = true
		tags = append(tags, nativeCategory{ID: id, Name: name, Kind: "tag"})
		if len(tags) >= nativeCategoryMaxEntries {
			break
		}
	}
	return tags
}

// fetchHuangguoAIPopularTags 获取黄果AI热门标签列表。
func (d *Downloader) fetchHuangguoAIPopularTags(ctx context.Context) ([]nativeCategory, error) {
	ctx = context.WithValue(ctx, providerTextNoCacheKey{}, true)
	ctx, base := d.huangguoAICatalogContext(ctx)
	address := base + "/"
	document, actual, err := d.fetchProviderPage(ctx, address, address, "")
	if err != nil {
		return nil, err
	}
	tags := parseHuangguoAIPopularTags(document, actual)
	if len(tags) == 0 {
		return nil, errors.New("站源未返回热门标签，保留现有目录，请稍后重试")
	}
	return tags, nil
}

// fetchHuangguoAITagPage 获取黄果AI标签页面。
func (d *Downloader) fetchHuangguoAITagPage(ctx context.Context, page int, category string) ([]Drama, bool, error) {
	slug := strings.TrimPrefix(category, "tag:")
	ctx, baseURL := d.huangguoAICatalogContext(ctx)
	address := baseURL + "/tag/" + url.PathEscape(slug) + "/?page=" + strconv.Itoa(max(1, page))
	document, actual, err := d.fetchProviderPage(ctx, address, baseURL+"/", "")
	if err != nil {
		return nil, false, err
	}
	grid := providerHTMLFirstClass(document, "hg-card-grid")
	if grid == nil {
		return nil, false, errors.New("标签页结构已变化，未取得剧集列表，请刷新重试")
	}
	var body strings.Builder
	if err := html.Render(&body, grid); err != nil {
		return nil, false, err
	}
	items := parseHuangguoAIDramaCards(body.String(), actual, "")
	base, err := url.Parse(actual)
	if err != nil {
		return nil, false, err
	}
	more := false
	for _, anchor := range providerHTMLNodes(document, func(node *html.Node) bool {
		return node.Type == html.ElementNode && node.Data == "a"
	}) {
		reference, err := url.Parse(providerHTMLAttr(anchor, "href"))
		if err != nil {
			continue
		}
		next := base.ResolveReference(reference)
		number, _ := strconv.Atoi(next.Query().Get("page"))
		if strings.EqualFold(next.Host, base.Host) && strings.TrimRight(next.Path, "/") == strings.TrimRight(base.Path, "/") && number > page {
			more = true
			break
		}
	}
	return items, more, nil
}
