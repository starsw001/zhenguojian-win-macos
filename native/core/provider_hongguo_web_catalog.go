package core

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strconv"
	"strings"
)

var hongguoWebRoutes = []struct{ slug, name string }{
	{"real-drama", "真人剧"},
	{"comic-drama", "漫剧"},
	{"ai-drama", "AI剧"},
	{"comic", "漫画"},
}

var hongguoWebTopic = regexp.MustCompile(`^[a-z][a-z0-9-]{0,63}$`)

type hongguoWebCatalogPage struct {
	Items      []Drama
	Categories []nativeCategory
	Page       int
	TotalPages int
}

// hongguoWebCategory 取得红果网页分类对应的显示与查询信息。
func hongguoWebCategory(category string) (route, topic, name string, valid bool) {
	parts := strings.Split(category, ":")
	if len(parts) < 2 || len(parts) > 3 || parts[0] != "web" {
		return
	}
	for _, entry := range hongguoWebRoutes {
		if parts[1] == entry.slug {
			route, name = entry.slug, entry.name
			break
		}
	}
	if route == "" {
		return
	}
	if len(parts) == 3 {
		if !hongguoWebTopic.MatchString(parts[2]) {
			return
		}
		topic = parts[2]
	}
	valid = true
	return
}

// parseHongguoWebCatalogPage 解析红果网页目录页。
func parseHongguoWebCatalogPage(body, category string, requested int) (hongguoWebCatalogPage, error) {
	result := hongguoWebCatalogPage{Items: []Drama{}, Categories: []nativeCategory{}}
	route, topic, name, valid := hongguoWebCategory(category)
	if !valid || requested < 1 || requested > 1000000 || len(body) > 4<<20 {
		return result, errors.New("红果网页分类或页码无效")
	}
	loader := routerLoaderMap(parseRouterData(body), "category_page", "category_$")
	if loader["isSuccess"] != true || mapString(nestedMap(loader, "categoryRoute"), "contentType") != route {
		return result, errors.New("红果返回了其他分类或未完成的页面，未提交目录")
	}
	canonical, err := url.Parse(mapString(loader, "canonicalUrl"))
	expected := "/category/" + route
	if topic != "" {
		expected += "/" + topic
	}
	if err != nil || canonical.Path != expected || canonical.User != nil || canonical.Hostname() != "hongguoduanju.com" && canonical.Hostname() != "www.hongguoduanju.com" {
		return result, errors.New("红果返回的分类地址与所选题材不一致")
	}
	pagination := nestedMap(loader, "pagination")
	current, _ := strconv.Atoi(mapString(pagination, "pageNum"))
	total, _ := strconv.Atoi(mapString(pagination, "totalPages"))
	itemCount, countErr := strconv.Atoi(mapString(pagination, "total"))
	pageSize, sizeErr := strconv.Atoi(mapString(pagination, "pageSize"))
	if countErr != nil || sizeErr != nil || itemCount < 0 || itemCount > 200000000 || pageSize < 1 || pageSize > 200 {
		return result, errors.New("红果目录总数或分页大小无效")
	}
	if itemCount == 0 && current == 1 && total == 0 {
		total = 1
	}
	if current != requested || total < current || total > 1000000 {
		return result, errors.New("红果返回页码与请求不一致，未将重复页当作末页")
	}
	if query := nestedMap(loader, "query"); mapString(query, "page") != strconv.Itoa(requested) {
		return result, errors.New("红果页面查询游标不一致")
	}
	seen := map[string]bool{}
	for _, raw := range anyList(loader["recommendList"]) {
		drama := hongguoDramaFromAny(raw, name)
		if drama.ID == "" || seen[drama.ID] {
			return result, errors.New("红果目录存在无法识别或重复的作品，未提交该页")
		}
		seen[drama.ID] = true
		result.Items = append(result.Items, drama)
	}
	if total != max(1, (itemCount+pageSize-1)/pageSize) {
		return result, errors.New("红果目录总页数与作品总数不一致")
	}
	expectedCount := min(pageSize, max(0, itemCount-(current-1)*pageSize))
	if len(result.Items) != expectedCount {
		return result, errors.New("红果目录页返回不完整，保留当前页等待重试")
	}
	if len(result.Items) == 0 && itemCount != 0 {
		return result, errors.New("红果目录没有返回预期作品")
	}
	result.Page, result.TotalPages = current, total
	topics := map[string]bool{}
	for _, row := range anyList(loader["selectorList"]) {
		group, _ := row.(map[string]any)
		for _, raw := range anyList(group["items"]) {
			item, _ := raw.(map[string]any)
			slug, label := mapString(item, "selector_item_id"), strings.TrimSpace(mapString(item, "show_name"))
			if !hongguoWebTopic.MatchString(slug) || label == "" || len(label) > 96 || topics[slug] {
				continue
			}
			topics[slug] = true
			result.Categories = append(result.Categories, nativeCategory{ID: "web:" + route + ":" + slug, Name: label})
		}
	}
	return result, nil
}

// fetchHongguoWebCatalogPage 获取红果网页目录页。
func (d *Downloader) fetchHongguoWebCatalogPage(ctx context.Context, category string, page int) (hongguoWebCatalogPage, error) {
	route, topic, _, valid := hongguoWebCategory(category)
	if !valid {
		return hongguoWebCatalogPage{}, errors.New("红果网页分类无效")
	}
	base := d.providerBaseURL(sourceHongguo)
	address := base + "/category/" + route
	if topic != "" {
		address += "/" + topic
	}
	if page > 1 {
		address += "?page=" + strconv.Itoa(page)
	}
	body, err := d.fetchProviderText(ctx, address, base+"/")
	if err != nil {
		return hongguoWebCatalogPage{}, err
	}
	return parseHongguoWebCatalogPage(body, category, page)
}

// fetchHongguoWebCategories 获取红果网页分类列表。
func (d *Downloader) fetchHongguoWebCategories(ctx context.Context) ([]nativeCategory, error) {
	all := []nativeCategory{{Name: "全部"}}
	var failures []error
	for _, route := range hongguoWebRoutes {
		category := "web:" + route.slug
		all = append(all, nativeCategory{ID: category, Name: route.name})
		page, err := d.fetchHongguoWebCatalogPage(ctx, category, 1)
		if err != nil {
			failures = append(failures, fmt.Errorf("%s: %w", route.name, err))
			continue
		}
		all = append(all, page.Categories...)
	}
	return all, errors.Join(failures...)
}
