package core

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"

	"golang.org/x/net/html"
)

type sourceCatalogCursorKey struct{}
type sourceCatalogCursor struct {
	Next       int
	Pagination *nativeCatalogPagination
}

type sourceRuleRefreshKey struct{}
type sourceConnectivityOnlyKey struct{}

// sourceCatalogRule 返回指定站源的目录与分页处理规则。
func sourceCatalogRule(source string) string {
	switch source {
	case sourceFanguo:
		return "站点搜索推荐入口；不代表全站目录"
	case sourceMaoguo:
		return "站点最新标签与next_id游标；推荐接口不代表全站穷举"
	case sourceHeguo, sourceXingguo:
		return "站点分类推荐入口；当前规则不提供全站穷举"
	case sourceHongguo:
		return "官网分类及题材按真实页码续载；兼容已有App分类游标"
	default:
		return "站点分类与分页，按接口规则取至末页"
	}
}

// searchSourceCatalog 搜索站源目录。
func (engine *nativeEngine) searchSourceCatalog(ctx context.Context, source, query string, page int, onlineOnly bool) (nativeCatalogResult, error) {
	result := nativeCatalogResult{Items: []nativeDrama{}, Page: page}
	if len(query) > 256 || page < 1 || page > 1000000 {
		return result, errors.New("搜索关键词过长")
	}
	if !onlineOnly {
		cached := engine.searchCachedCatalog(source, query)
		if len(cached) > 0 {
			start := min((page-1)*60, len(cached))
			end := min(start+60, len(cached))
			result.Items, result.HasMore = cached[start:end], end < len(cached)
			result.LocalSearch, result.SearchState = true, "local_hit"
			return result, nil
		}
	}
	ctx = context.WithValue(ctx, providerTextNoCacheKey{}, true)
	ctx = context.WithValue(ctx, sourceConnectivityOnlyKey{}, true)
	var items []Drama
	var err error
	switch source {
	case sourceHongguo:
		var entry hongguoSearchEntry
		entry, err = engine.downloader.searchHongguoDramas(ctx, query)
		items, result.Warning = entry.Dramas, entry.Warning
		if entry.Limited && result.Warning == "" {
			result.Warning = "站点仅返回当前可获取的匹配结果，可用完整剧名继续查找"
		}
	case sourceHuangju:
		items, result.HasMore, err = engine.downloader.fetchHuangjuCatalogPage(ctx, page, "", query)
	case sourceSorani:
		items, result.HasMore, err = engine.downloader.fetchSoraniCatalogPage(ctx, page, "", query)
	case sourceGuipian:
		items, result.HasMore, err = engine.downloader.fetchGuipianCatalogPage(ctx, page, "", query)
		if err == nil && len(items) == 0 {
			err = errors.New("线上RSS最新条目未匹配；该入口不是全站搜索，不能确认全站无结果")
		} else if err == nil {
			result.Warning = "已匹配线上RSS最新条目；此入口不代表全站搜索"
		}
	case sourceHanxiaoquan:
		items, result.HasMore, err = engine.downloader.fetchHanxiaoquanCatalogPage(ctx, page, "", query)
	case sourceYeguo:
		items, result.HasMore, err = engine.downloader.fetchYeguoCatalogPage(ctx, page, "", query)
	case sourceDSD:
		items, result.HasMore, err = engine.downloader.fetchDSDCatalogPage(ctx, page, "", query)
	case sourceHuangguoVideo:
		items, err = engine.downloader.searchDeclaredVideoForm(ctx, query)
	case sourceHuangdou, sourceCloudFront:
		err = errors.New("本地没有匹配；当前线路没有已验证的在线搜索接口，不能确认搜索无结果。请提供有效搜索API或更新线路规则")
	default:
		if isDuanjuProviderSource(source) {
			items, err = engine.downloader.searchDuanju(ctx, source, query)
		} else {
			err = errors.New("当前线路没有可用的在线搜索规则，不能确认搜索无结果")
		}
	}
	if err != nil {
		return result, fmt.Errorf("本地未命中，线上搜索未完成：%w", err)
	}
	for _, drama := range items {
		if drama.ID != "" {
			result.Items = append(result.Items, nativeNormalize(drama))
		}
	}
	result.SearchState = "online_hit"
	if len(result.Items) == 0 {
		if result.Warning != "" {
			return result, errors.New("线上搜索未完整返回，不能确认搜索无结果：" + result.Warning)
		}
		result.SearchState = "empty_confirmed"
	}
	return result, nil
}

// searchDeclaredVideoForm 搜索Declared视频表单。
func (d *Downloader) searchDeclaredVideoForm(ctx context.Context, query string) ([]Drama, error) {
	base := d.providerBaseURL(sourceHuangguoVideo)
	body, err := d.fetchProviderText(ctx, base+"/videos", base+"/")
	if err != nil {
		return nil, err
	}
	document, err := html.Parse(strings.NewReader(body))
	if err != nil {
		return nil, err
	}
	root, _ := url.Parse(base + "/videos")
	for _, form := range providerHTMLNodes(document, func(n *html.Node) bool { return n.Type == html.ElementNode && n.Data == "form" }) {
		method := strings.ToLower(providerHTMLAttr(form, "method"))
		if method != "" && method != "get" {
			continue
		}
		action, parseErr := url.Parse(providerHTMLAttr(form, "action"))
		if parseErr != nil {
			continue
		}
		address := root.ResolveReference(action)
		if address.Scheme != root.Scheme || address.Host != root.Host {
			continue
		}
		values := address.Query()
		keyword := ""
		for _, input := range providerHTMLNodes(form, func(n *html.Node) bool { return n.Type == html.ElementNode && n.Data == "input" }) {
			name := providerHTMLAttr(input, "name")
			switch name {
			case "q", "wd", "keyword", "searchword", "search":
				keyword = name
			default:
				if strings.EqualFold(providerHTMLAttr(input, "type"), "hidden") && name != "" {
					values.Set(name, providerHTMLAttr(input, "value"))
				}
			}
		}
		if keyword == "" {
			continue
		}
		values.Set(keyword, query)
		address.RawQuery = values.Encode()
		response, fetchErr := d.fetchProviderText(ctx, address.String(), root.String())
		if fetchErr != nil {
			return nil, fetchErr
		}
		items := parseHuangguoVideoCards(response, address.String())
		if len(items) == 0 && !sourceExplicitEmpty(response) {
			return nil, errors.New("搜索页没有可识别的结果或明确空结果标记，不能判断无结果")
		}
		return items, nil
	}
	return nil, errors.New("站点没有声明可用的GET搜索表单，未猜测接口或把本地空索引当作全站无结果")
}

// sourceExplicitEmpty 判断站源响应是否明确表示当前目录为空。
func sourceExplicitEmpty(body string) bool {
	lower := strings.ToLower(body)
	for _, marker := range []string{"暂无数据", "暂无相关", "没有找到相关", "未找到相关", "搜索结果为空", "没有相关", "no results", "nothing found", "no videos found"} {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return false
}

// refreshSourceByRule 刷新站源By规则。
func (engine *nativeEngine) refreshSourceByRule(ctx context.Context, source string) error {
	if source == sourceHuangguoAI || !nativeSourceAvailable(source) {
		return errors.New("当前线路不属于此采集任务")
	}
	ctx = context.WithValue(ctx, sourceRuleRefreshKey{}, true)
	ctx = context.WithValue(ctx, sourceConnectivityOnlyKey{}, true)
	ctx = context.WithValue(ctx, providerTextNoCacheKey{}, true)
	before := len(engine.nativeCached(source).Items)
	pages, fetched := 0, map[string]bool{}
	rule := sourceCatalogRule(source)
	engine.changeSourceRecord(source, func(record *nativeSourceRecord) {
		record.CatalogRule, record.CatalogPages, record.CatalogFetched = rule, 0, 0
		record.CatalogFullAt = time.Time{}
		record.Stage = "读取当前入口与分类"
	})
	defer func() {
		engine.changeSourceRecord(source, func(record *nativeSourceRecord) {
			record.Added = max(0, len(engine.catalogs[source])-before)
			record.CatalogPages, record.CatalogFetched = pages, len(fetched)
			record.CatalogOrigin = engine.downloader.providerBaseURL(source)
			if isDuanjuProviderSource(source) {
				record.CatalogOrigin = engine.downloader.duanjuBaseURL(source)
			}
		})
	}()
	categories, categoryErr := engine.nativeCategories(ctx, source, true)
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if len(categories) <= 1 {
		categories = []nativeCategory{{Name: "全部"}}
	} else {
		filtered := []nativeCategory{}
		for _, category := range categories {
			if category.ID != "" && category.Kind != "tag" {
				filtered = append(filtered, category)
			}
		}
		if len(filtered) > 0 {
			categories = filtered
		}
	}
	var failures []error
	if categoryErr != nil {
		failures = append(failures, fmt.Errorf("分类更新未完成：%w", categoryErr))
	}
	for index, category := range categories {
		seen := map[string]bool{}
		for page := 1; ; page++ {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			if page > 1000000 {
				return errors.New("分页超出安全范围，采集未完成，旧目录保留")
			}
			engine.changeSourceRecord(source, func(record *nativeSourceRecord) {
				record.Stage = fmt.Sprintf("按规则采集 · %s · 第%d页", category.Name, page)
				record.Completed, record.Total = index, len(categories)
			})
			result, err := engine.nativeCatalog(ctx, nativeInput{Source: source, Category: category.ID, Page: page, Force: true})
			pages++
			newIDs := 0
			for _, drama := range result.Items {
				fetched[drama.ID] = true
				if !seen[drama.ID] {
					newIDs++
					seen[drama.ID] = true
				}
			}
			engine.changeSourceRecord(source, func(record *nativeSourceRecord) {
				record.CatalogPages, record.CatalogFetched = pages, len(fetched)
			})
			if err == nil && result.Warning != "" {
				err = errors.New(result.Warning)
			}
			if err == nil && result.HasMore && newIDs == 0 && !sourceCatalogPageAdvanced(result) {
				err = errors.New("站点分页重复或未前进；保留已采集目录，不误报全量完成")
			}
			if err != nil {
				failures = append(failures, fmt.Errorf("%s第%d页：%w", category.Name, page, err))
				var backoff *requestBackoff
				if errors.As(err, &backoff) {
					return errors.Join(failures...)
				}
				break
			}
			if !result.HasMore {
				break
			}
			if result.NextPage > page {
				page = result.NextPage - 1
			}
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(200 * time.Millisecond):
			}
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
	}
	if len(failures) > 0 {
		return errors.Join(failures...)
	}
	if len(fetched) == 0 {
		return errors.New("当前入口没有返回可识别目录；旧索引保留，未标记采集完成")
	}
	engine.mu.Lock()
	state := engine.catalogStates[source]
	state.UpdatedAt, state.HasMore, state.Warning = time.Now(), false, ""
	engine.catalogStates[source] = state
	saveErr := engine.writeCatalogDiskLocked()
	engine.mu.Unlock()
	if saveErr != nil {
		return saveErr
	}
	engine.changeSourceRecord(source, func(record *nativeSourceRecord) {
		record.CatalogFullAt = time.Now()
		record.Completed, record.Total = len(categories), len(categories)
	})
	return nil
}

// sourceHTMLHasMore 判断站源HTML是否还有后续条目。
func sourceHTMLHasMore(body, address string, page int) bool {
	document, err := html.Parse(strings.NewReader(body))
	return err == nil && sourceDocumentHasMore(document, address, page)
}

// sourceDocumentHasMore 判断站源文档是否还有后续条目。
func sourceDocumentHasMore(document *html.Node, raw string, page int) bool {
	base, err := url.Parse(raw)
	if err != nil {
		return false
	}
	for _, a := range providerHTMLNodes(document, func(n *html.Node) bool { return n.Type == html.ElementNode && n.Data == "a" }) {
		href := strings.TrimSpace(providerHTMLAttr(a, "href"))
		if href == "" || href == "#" || strings.HasPrefix(strings.ToLower(href), "javascript:") {
			continue
		}
		link, err := url.Parse(href)
		if err != nil {
			continue
		}
		link = base.ResolveReference(link)
		if link.Host != base.Host || link.String() == base.String() {
			continue
		}
		label := strings.ToLower(strings.TrimSpace(providerHTMLText(a)))
		disabled := strings.Contains(strings.ToLower(providerHTMLAttr(a, "class")), "disabled") || providerHTMLAttr(a, "aria-disabled") == "true"
		if !disabled && (strings.Contains(label, "下一页") || label == "下页" || label == "next" || providerHTMLAttr(a, "rel") == "next") {
			return true
		}
		if number, err := strconv.Atoi(label); err == nil && number > page && !disabled {
			for parent, depth := a.Parent, 0; parent != nil && depth < 4; parent, depth = parent.Parent, depth+1 {
				class := strings.ToLower(providerHTMLAttr(parent, "class"))
				if strings.Contains(class, "page") || strings.Contains(class, "pagination") {
					return true
				}
			}
		}
	}
	return false
}

// sourceSearchEnvelope 搜索站源信封。
func sourceSearchEnvelope(response map[string]any, source string) error {
	if success, ok := response["success"].(bool); ok && !success {
		return errors.New("线上搜索接口返回失败状态，不能确认无结果")
	}
	if code, ok := huangguoCatalogInteger(response["code"]); ok && code >= 400 {
		return fmt.Errorf("线上搜索接口返回错误码%d，不能确认无结果", code)
	}
	paths := map[string][][]string{
		sourceYaguo:   {{"data", "list"}, {"data", "theater", "search_data"}},
		sourceMaoguo:  {{"data", "list"}},
		sourceGuanguo: {{"data"}},
		sourceFanguo:  {{"result", "elements"}},
		sourceXingguo: {{"data", "datalist"}},
		sourceHeguo:   {{"data", "bookList"}},
		sourceNiuguo:  {{"list"}, {"data"}, {"data", "list"}},
	}
	for _, path := range paths[source] {
		var value any = response
		for _, key := range path {
			object, _ := value.(map[string]any)
			value = object[key]
		}
		if _, ok := value.([]any); ok {
			return nil
		}
	}
	return errors.New("线上搜索接口缺少有效结果列表，不能将错误包或null当作无结果")
}
