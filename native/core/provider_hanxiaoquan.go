package core

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"golang.org/x/net/html"
)

// 韩小圈（hanxiaoquan）原生站源：MacCMS 苹果 CMS 标准模板站点，韩剧/韩国电影/韩国综艺/韩国动漫。
//
// 上游页面（实测 2026-09，走国内网络）：
//   GET /                      首页（热播推荐）
//   GET /hxq/{id}.html         分类第 1 页（id: 1 最新韩剧 2 韩国电影 3 韩国综艺 4 韩国动漫）
//   GET /hxq/{id}-{page}.html  分类翻页（第 1 页省略后缀）
//   GET /hanxiaoquan/{id}.html 详情（含多线路播放列表）
//   GET /play/{id}-{line}-{ep}.html  播放页（var now="<m3u8>"）
//   GET /search.php?searchword=  搜索
//
// 列表卡片为 <div class="module-item">，标题取 module-item-title，封面取 img[data-src]，
// 备注取 module-item-text（多为更新日期）。详情字段为 <span class="video-info-itemtitle">，
// 封面取 og:image 或 video-cover 内的 data-src。播放列表按 <div id="playlist{N}"> 分线路，
// 分集为 <a title="第NN集" href="/play/{id}-{line}-{ep}.html">。

const (
	hanxiaoquanSiteBaseURL = "https://www.jennyhow.com"
	hanxiaoquanUserAgent   = "Mozilla/5.0 (Linux; Android 13; Pixel 7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/126.0.0.0 Mobile Safari/537.36"
	hanxiaoquanPageSize    = 24
)

var hanxiaoquanCategories = []nativeCategory{
	{ID: "1", Name: "最新韩剧"},
	{ID: "2", Name: "韩国电影"},
	{ID: "3", Name: "韩国综艺"},
	{ID: "4", Name: "韩国动漫"},
}

var (
	reHanxiaoquanCardPic  = regexp.MustCompile(`(?is)<a href="/hanxiaoquan/(\d+)\.html"[^>]*title="([^"]*)"[^>]*class="module-item-pic"`)
	reHanxiaoquanCardImg  = regexp.MustCompile(`(?is)<img[^>]*data-src="([^"]+)"`)
	reHanxiaoquanCardText = regexp.MustCompile(`(?is)<a href="/hanxiaoquan/\d+\.html"[^>]*class="module-item-title"[^>]*>([^<]*)</a>`)
	reHanxiaoquanCardNote = regexp.MustCompile(`(?is)class="module-item-text"[^>]*>([^<]*)<`)
	reHanxiaoquanAdText   = regexp.MustCompile(`@[^@]{1,60}@`)
	reHanxiaoquanTitle    = regexp.MustCompile(`(?is)<h1 class="page-title"[^>]*>(.*?)</h1>`)
	reHanxiaoquanOgImage  = regexp.MustCompile(`(?is)property="og:image"\s+content="([^"]+)"`)
	reHanxiaoquanCoverSrc = regexp.MustCompile(`(?is)<div class="video-cover">.*?<img[^>]*data-src="([^"]+)"`)
	reHanxiaoquanInfoItem = regexp.MustCompile(`(?is)<span class="video-info-itemtitle">([^<]*)</span>\s*<div class="video-info-item[^"]*">(.*?)</div>`)
	reHanxiaoquanAnchor   = regexp.MustCompile(`(?is)<a[^>]*>([^<]+)</a>`)
	reHanxiaoquanLineTab  = regexp.MustCompile(`(?is)href="#playlist(\d+)"[^>]*>\s*<i[^>]*></i>\s*([^<]+)`)
	reHanxiaoquanEpisodes = regexp.MustCompile(`(?is)<a title="([^"]*)" href="(/play/\d+-\d+-\d+\.html)"`)
	reHanxiaoquanNow      = regexp.MustCompile(`(?is)var\s+now\s*=\s*["']([^"']+)["']`)
	reHanxiaoquanMediaAny = regexp.MustCompile(`(?i)https?://[^"'\s<>\\]+\.(?:m3u8|mp4)(?:\?[^"'\s<>\\]*)?`)
	reHanxiaoquanTag      = regexp.MustCompile(`<[^>]+>`)
	reHanxiaoquanLeadNum  = regexp.MustCompile(`(\d+)`)
)

// validHanxiaoquanCategory 判断是否有效：韩小圈分类。
func validHanxiaoquanCategory(category string) bool {
	if category == "" {
		return true
	}
	if len(category) > 8 || strings.ContainsAny(category, "|/\\\x00\r\n") {
		return false
	}
	for _, entry := range hanxiaoquanCategories {
		if entry.ID == category {
			return true
		}
	}
	return false
}

// hanxiaoquanNumericID 判断韩小圈数字标识是否满足当前规则。
func hanxiaoquanNumericID(value string) bool {
	value = strings.TrimSpace(value)
	if value == "" || len(value) > 12 {
		return false
	}
	number, err := strconv.Atoi(value)
	return err == nil && number > 0
}

// hanxiaoquanClean 去标签、解 HTML 实体、压缩空白。
func hanxiaoquanClean(text string) string {
	if text == "" {
		return ""
	}
	text = reHanxiaoquanTag.ReplaceAllString(text, "")
	text = guipianUnescape(text)
	text = strings.ReplaceAll(text, "\u3000", " ")
	return strings.Join(strings.Fields(text), " ")
}

// hanxiaoquanFixURL 修正韩小圈来源返回的地址格式。
func hanxiaoquanFixURL(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	if strings.HasPrefix(raw, "//") {
		raw = "https:" + raw
	} else if strings.HasPrefix(raw, "/") {
		raw = hanxiaoquanSiteBaseURL + raw
	}
	parsed, err := url.Parse(raw)
	if err != nil || !validNativeCoverURL(parsed) {
		return ""
	}
	return parsed.String()
}

// fetchHanxiaoquanCategories 获取韩小圈分类列表。
func (d *Downloader) fetchHanxiaoquanCategories() []nativeCategory {
	return append([]nativeCategory(nil), hanxiaoquanCategories...)
}

// hanxiaoquanGet 读取韩小圈站源的页面或接口响应。
func (d *Downloader) hanxiaoquanGet(ctx context.Context, path string) (string, error) {
	site := d.providerBaseURL(sourceHanxiaoquan)
	address := path
	if !strings.HasPrefix(address, "http") {
		address = site + path
	}
	pageContext := context.WithValue(ctx, providerTextUserAgentKey{}, hanxiaoquanUserAgent)
	return d.fetchProviderText(pageContext, address, site+"/")
}

// parseHanxiaoquanCards 解析 module-item 卡片列表。
func parseHanxiaoquanCards(body string) []Drama {
	items := make([]Drama, 0, hanxiaoquanPageSize)
	seen := map[string]bool{}
	for _, match := range reHanxiaoquanCardPic.FindAllStringSubmatchIndex(body, -1) {
		if len(match) < 6 {
			continue
		}
		id := body[match[2]:match[3]]
		title := hanxiaoquanClean(body[match[4]:match[5]])
		if !hanxiaoquanNumericID(id) || seen[id] {
			continue
		}
		chunk := body[match[1]:]
		if end := len(chunk); end > 600 {
			chunk = chunk[:600]
		}
		cover := ""
		if coverMatch := reHanxiaoquanCardImg.FindStringSubmatch(chunk); len(coverMatch) > 1 {
			cover = hanxiaoquanFixURL(coverMatch[1])
		}
		note := ""
		if noteMatch := reHanxiaoquanCardNote.FindStringSubmatch(chunk); len(noteMatch) > 1 {
			note = hanxiaoquanClean(noteMatch[1])
		}
		if title == "" {
			if titleMatch := reHanxiaoquanCardText.FindStringSubmatch(chunk); len(titleMatch) > 1 {
				title = hanxiaoquanClean(titleMatch[1])
			}
		}
		if title == "" {
			continue
		}
		seen[id] = true
		items = append(items, Drama{
			ID: providerDramaID(sourceHanxiaoquan, id), Source: sourceHanxiaoquan, SourceID: id,
			Title: truncate(title, 512), Name: truncate(title, 512),
			Cover: cover, CoverURL: cover, ChannelName: "韩小圈",
			CategoryName: "韩剧", Remark: truncate(note, 64),
		})
		if len(items) > 500 {
			break
		}
	}
	return items
}

// hanxiaoquanPageCount 从分页链接推断末页；拿不到时按当前页是否满页保守判断。
func hanxiaoquanPagerVisible(node, container *html.Node) bool {
	for current := node; current != nil; current = current.Parent {
		style := strings.ToLower(strings.Join(strings.Fields(providerHTMLAttr(current, "style")), ""))
		if strings.Contains(style, "display:none") || strings.Contains(style, "visibility:hidden") ||
			providerHTMLAttr(current, "aria-hidden") == "true" || providerHTMLAttr(current, "aria-disabled") == "true" ||
			providerHTMLClass(current, "disabled") || providerHTMLClass(current, "hidden") || providerHTMLClass(current, "hide") {
			return false
		}
		for _, attribute := range current.Attr {
			if attribute.Key == "hidden" {
				return false
			}
		}
		if current == container {
			break
		}
	}
	return true
}

// hanxiaoquanPageRange 从韩小圈分页信息中提取页码范围。
func hanxiaoquanPageRange(body, category string) (int, int) {
	document, err := html.Parse(strings.NewReader(body))
	if err != nil {
		return 0, 0
	}
	pattern := regexp.MustCompile(`^/hxq/` + regexp.QuoteMeta(category) + `(?:-([0-9]+))?\.html$`)
	current, last := 0, 0
	for _, pager := range providerHTMLNodes(document, func(node *html.Node) bool { return providerHTMLAttr(node, "id") == "page" }) {
		for _, node := range providerHTMLNodes(pager, func(node *html.Node) bool { return node.Type == html.ElementNode }) {
			if !hanxiaoquanPagerVisible(node, pager) {
				continue
			}
			if providerHTMLClass(node, "page-current") {
				if number, err := strconv.Atoi(strings.TrimSpace(providerHTMLText(node))); err == nil && number > 0 && number <= 100000 {
					current = number
					last = max(last, number)
				}
			}
			if node.Data != "a" {
				continue
			}
			address, err := url.Parse(providerHTMLAttr(node, "href"))
			if err != nil {
				continue
			}
			match := pattern.FindStringSubmatch(address.Path)
			if len(match) != 2 {
				continue
			}
			number := 1
			if match[1] != "" {
				number, _ = strconv.Atoi(match[1])
			}
			if number > 0 && number <= 100000 {
				last = max(last, number)
			}
		}
	}
	return current, last
}

// hanxiaoquanPageCount 从韩小圈分页信息中提取页数。
func hanxiaoquanPageCount(body, category string, page, count int) int {
	if current, last := hanxiaoquanPageRange(body, category); current > 0 && last >= current {
		return last
	}
	pattern := regexp.MustCompile(fmt.Sprintf(`(?is)/hxq/%s-(\d+)\.html`, regexp.QuoteMeta(category)))
	maxPage := page
	for _, match := range pattern.FindAllStringSubmatch(body, -1) {
		if value, err := strconv.Atoi(match[1]); err == nil && value > maxPage && value < 100000 {
			maxPage = value
		}
	}
	if maxPage > page {
		return maxPage
	}
	if count >= hanxiaoquanPageSize {
		return page + 1
	}
	return page
}

// fetchHanxiaoquanCatalogPage 获取韩小圈目录页。
func (d *Downloader) fetchHanxiaoquanCatalogPage(ctx context.Context, page int, category, query string) ([]Drama, bool, error) {
	if page < 1 || page > 100000 {
		return nil, false, errors.New("韩小圈目录页码无效")
	}
	if query != "" {
		return d.searchHanxiaoquan(ctx, page, query)
	}
	if !validHanxiaoquanCategory(category) {
		return nil, false, errors.New("韩小圈内容分类无效")
	}
	if category == "" {
		category = hanxiaoquanCategories[0].ID
	}
	path := "/hxq/" + category + ".html"
	if page > 1 {
		path = fmt.Sprintf("/hxq/%s-%d.html", category, page)
	}
	body, err := d.hanxiaoquanGet(ctx, path)
	if err != nil {
		return nil, false, err
	}
	if current, _ := hanxiaoquanPageRange(body, category); current > 0 && current != page {
		return nil, false, fmt.Errorf("韩小圈请求第%d页，但站点返回第%d页；已阻止重复页混入目录", page, current)
	}
	items := parseHanxiaoquanCards(body)
	if len(items) == 0 {
		return []Drama{}, false, nil
	}
	pageCount := hanxiaoquanPageCount(body, category, page, len(items))
	return items, page < pageCount, nil
}

// hanxiaoquanInfoFields 抽取详情页的 video-info-items 字段。
func hanxiaoquanInfoFields(body string) map[string]string {
	fields := map[string]string{}
	for _, match := range reHanxiaoquanInfoItem.FindAllStringSubmatch(body, -1) {
		label := strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(match[1]), "："))
		value := hanxiaoquanClean(match[2])
		if label != "" && value != "" && fields[label] == "" {
			fields[label] = value
		}
	}
	return fields
}

// hanxiaoquanPickPlaylist 解析全部线路，返回分集最多的那条线的线路名与分集。
func hanxiaoquanPickPlaylist(body string) (string, []Chapter, error) {
	lineNames := map[string]string{}
	for _, tab := range reHanxiaoquanLineTab.FindAllStringSubmatch(body, -1) {
		lineNames[tab[1]] = strings.TrimSpace(tab[2])
	}
	lineIDs := make([]string, 0, len(lineNames))
	for id := range lineNames {
		lineIDs = append(lineIDs, id)
	}
	sort.Strings(lineIDs)
	if len(lineIDs) == 0 {
		// 无 tab 时兜底：直接扫描页面内所有分集链接。
		lineIDs = []string{""}
	}
	bestName := ""
	var best []Chapter
	var allChapters []Chapter
	for _, lineID := range lineIDs {
		segment := body
		if lineID != "" {
			marker := `id="playlist` + lineID + `"`
			start := strings.Index(body, marker)
			if start < 0 {
				continue
			}
			// 从本容器标记之后开始找下一个 playlist 容器或列表结束标记，
			// 避免匹配到容器自身导致截断失败（进而把后续线路的分集一并算入）。
			segment = body[start+len(marker):]
			if end := strings.Index(segment, `id="playlist`); end >= 0 {
				segment = segment[:end]
			} else if end := strings.Index(segment, `class="module-footer"`); end >= 0 {
				segment = segment[:end]
			}
		}
		var chapters []Chapter
		seenPath := map[string]bool{}
		for index, ep := range reHanxiaoquanEpisodes.FindAllStringSubmatch(segment, -1) {
			epTitle := hanxiaoquanClean(ep[1])
			playURL := hanxiaoquanPlayURL(ep[2])
			if playURL == "" || seenPath[playURL] {
				continue
			}
			seenPath[playURL] = true
			order := index + 1
			if num := reHanxiaoquanLeadNum.FindString(epTitle); num != "" {
				if parsed, err := strconv.Atoi(num); err == nil && parsed > 0 && parsed <= 100000 {
					order = parsed
				}
			}
			if epTitle == "" {
				epTitle = fmt.Sprintf("第 %d 集", order)
			}
			chapters = append(chapters, Chapter{
				ID:             providerChapterID(sourceHanxiaoquan, "", playURL),
				Source:         sourceHanxiaoquan,
				Title:          truncate(epTitle, 128),
				CurrentEpisode: rawEpisode(order),
				VideoURL:       "hanxiaoquan-play://" + playURL,
				PageURL:        playURL,
				Referer:        hanxiaoquanSiteBaseURL + "/",
			})
		}
		allChapters = append(allChapters, chapters...)
		if len(chapters) > len(best) {
			best = chapters
			bestName = lineNames[lineID]
		}
	}
	if len(best) == 0 {
		return "", nil, errors.New("韩小圈暂无可播放分集")
	}
	sort.SliceStable(best, func(i, j int) bool {
		left, _ := strconv.Atoi(best[i].EpisodeString(i + 1))
		right, _ := strconv.Atoi(best[j].EpisodeString(j + 1))
		return left < right
	})
	return bestName, attachChapterRoutes(best, allChapters), nil
}

// hanxiaoquanPlayURL 归一化播放锚点并校验同源。
func hanxiaoquanPlayURL(href string) string {
	href = strings.TrimSpace(href)
	if href == "" {
		return ""
	}
	if !strings.HasPrefix(href, "http") {
		href = hanxiaoquanSiteBaseURL + "/" + strings.TrimPrefix(href, "/")
	}
	parsed, err := url.Parse(href)
	if err != nil || parsed.User != nil {
		return ""
	}
	host := strings.ToLower(parsed.Hostname())
	if host != "jennyhow.com" && host != "www.jennyhow.com" {
		return ""
	}
	if !strings.HasSuffix(strings.ToLower(parsed.Path), ".html") || !strings.Contains(parsed.Path, "/play/") {
		return ""
	}
	return parsed.String()
}

// fetchHanxiaoquanDetail 获取韩小圈详情。
func (d *Downloader) fetchHanxiaoquanDetail(ctx context.Context, sourceID string) (Drama, []Chapter, error) {
	if !hanxiaoquanNumericID(sourceID) {
		return Drama{}, nil, errors.New("韩小圈视频 ID 无效")
	}
	body, err := d.hanxiaoquanGet(ctx, "/hanxiaoquan/"+sourceID+".html")
	if err != nil {
		return Drama{}, nil, err
	}
	name := ""
	if match := reHanxiaoquanTitle.FindStringSubmatch(body); len(match) > 1 {
		name = hanxiaoquanClean(match[1])
	}
	if name == "" {
		return Drama{}, nil, errors.New("韩小圈详情缺少视频名称")
	}
	cover := ""
	if match := reHanxiaoquanOgImage.FindStringSubmatch(body); len(match) > 1 {
		cover = hanxiaoquanFixURL(match[1])
	}
	if cover == "" {
		if match := reHanxiaoquanCoverSrc.FindStringSubmatch(body); len(match) > 1 {
			cover = hanxiaoquanFixURL(match[1])
		}
	}
	fields := hanxiaoquanInfoFields(body)
	description := fields["剧情"]
	if description == "" {
		description = fields["简介"]
	}
	// 简介里常夹带 "@广告词@" 形式的干扰词，去掉首尾 @...@ 段。
	description = reHanxiaoquanAdText.ReplaceAllString(description, "")

	drama := Drama{
		ID: providerDramaID(sourceHanxiaoquan, sourceID), Source: sourceHanxiaoquan, SourceID: sourceID,
		Title: truncate(name, 512), Name: truncate(name, 512),
		Desc: truncate(description, 12000), Intro: truncate(description, 12000),
		Cover: cover, CoverURL: cover, ChannelName: "韩小圈", CategoryName: "韩剧",
		Remark:     truncate(firstNonEmpty(fields["更新"], fields["状态"]), 64),
		OnlineDate: truncate(fields["更新"], 32),
	}
	tags := make([]string, 0, 8)
	seenTag := map[string]bool{}
	addTag := func(value string) {
		value = strings.TrimSpace(value)
		if value != "" && !seenTag[value] && len([]rune(value)) <= 48 && len(tags) < 16 {
			seenTag[value] = true
			tags = append(tags, value)
		}
	}
	if year := fields["上映"]; year != "" {
		addTag(year + "年")
	}
	if director := fields["导演"]; director != "" {
		addTag("导演 " + truncate(director, 32))
	}
	if actors := fields["主演"]; actors != "" {
		addTag("主演 " + truncate(actors, 48))
	}
	drama.Tags = tags

	_, chapters, err := hanxiaoquanPickPlaylist(body)
	if err != nil {
		return Drama{}, nil, err
	}
	for index := range chapters {
		chapters[index].ID = providerChapterID(sourceHanxiaoquan, sourceID, chapters[index].PageURL)
		for route := range chapters[index].Alternatives {
			c := &chapters[index].Alternatives[route]
			c.ID = providerChapterID(sourceHanxiaoquan, sourceID, c.PageURL)
		}
	}
	drama.TotalEpisode, drama.EpisodeCount = len(chapters), len(chapters)
	return drama, chapters, nil
}

// resolveHanxiaoquanMedia 解析韩小圈媒体。
func (d *Downloader) resolveHanxiaoquanMedia(ctx context.Context, task Task) (providerMedia, error) {
	source, sourceID, valid := splitProviderDramaID(task.DramaID)
	prefix := providerChapterID(sourceHanxiaoquan, sourceID, "")
	if !valid || source != sourceHanxiaoquan || !hanxiaoquanNumericID(sourceID) || !strings.HasPrefix(task.Chapter.ID, prefix) {
		return providerMedia{}, errors.New("韩小圈播放分集信息无效，请刷新详情")
	}
	playURL := strings.TrimPrefix(task.Chapter.ID, prefix)
	if playURL == "" {
		playURL = task.Chapter.PageURL
	}
	if hanxiaoquanPlayURL(playURL) == "" {
		return providerMedia{}, errors.New("韩小圈播放页地址无效")
	}
	body, err := d.hanxiaoquanGet(ctx, playURL)
	if err != nil {
		return providerMedia{}, err
	}
	address := ""
	if match := reHanxiaoquanNow.FindStringSubmatch(body); len(match) > 1 {
		address = strings.TrimSpace(match[1])
	}
	if address == "" {
		address = reHanxiaoquanMediaAny.FindString(body)
	}
	if address == "" {
		return providerMedia{}, errors.New("韩小圈该集暂无可用播放地址")
	}
	if strings.HasPrefix(address, "//") {
		address = "https:" + address
	}
	if !isProviderHTTPMediaURL(address) {
		return providerMedia{}, errors.New("韩小圈播放地址无效")
	}
	parsed, err := url.Parse(address)
	if err != nil {
		return providerMedia{}, errors.New("韩小圈播放地址无效")
	}
	media := providerMedia{URL: address, Referer: hanxiaoquanSiteBaseURL + "/"}
	if strings.HasSuffix(strings.ToLower(parsed.Path), ".m3u8") {
		return d.prepareWebProviderMedia(ctx, media, "韩小圈")
	}
	return media, nil
}

// searchHanxiaoquan 调用站点搜索接口。
func (d *Downloader) searchHanxiaoquan(ctx context.Context, page int, query string) ([]Drama, bool, error) {
	query = strings.TrimSpace(query)
	if query == "" || len(query) > 256 {
		return nil, false, errors.New("韩小圈搜索关键词无效")
	}
	if page > 1 {
		// 站点搜索无翻页，直接返回空。
		return []Drama{}, false, nil
	}
	body, err := d.hanxiaoquanGet(ctx, "/search.php?"+url.Values{"searchword": {query}}.Encode())
	if err != nil {
		return nil, false, err
	}
	items := parseHanxiaoquanCards(body)
	if len(items) == 0 && !sourceExplicitEmpty(body) {
		return nil, false, errors.New("韩小圈搜索页没有可识别结果或明确空结果，不能确认无结果")
	}
	return items, false, nil
}
