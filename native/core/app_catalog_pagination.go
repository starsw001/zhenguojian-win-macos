package core

import (
	"context"
	"errors"
	"net/url"
	"regexp"
	"strconv"
	"strings"

	"golang.org/x/net/html"
)

type nativeCatalogPagination struct {
	Current    int    `json:"current"`
	TotalPages int    `json:"totalPages"`
	Verified   bool   `json:"verified"`
	Rule       string `json:"rule"`
}

// sourceSetCatalogPagination 设置站源目录分页。
func sourceSetCatalogPagination(ctx context.Context, current, total int, rule string) {
	if current < 1 || total < current || total > 1000000 {
		return
	}
	if cursor, _ := ctx.Value(sourceCatalogCursorKey{}).(*sourceCatalogCursor); cursor != nil {
		cursor.Pagination = &nativeCatalogPagination{Current: current, TotalPages: total, Verified: true, Rule: rule}
	}
}

// sourceCatalogPageAdvanced 判断本次目录响应是否使分页游标前进。
func sourceCatalogPageAdvanced(result nativeCatalogResult) bool {
	p := result.Pagination
	return len(result.Items) > 0 && p != nil && p.Verified && p.Current == result.Page && p.TotalPages > p.Current && p.TotalPages <= 1000000 && result.HasMore
}

var wuguoCatalogPagePath = regexp.MustCompile(`^/(?:index\.php/)?vod/type/id/([1-9][0-9]*)(?:/page/([1-9][0-9]*))?\.html$`)

// sourceMaccmsPageNumber 返回站源MACCMS页码。
func sourceMaccmsPageNumber(source string, base *url.URL, href string) int {
	link, err := url.Parse(strings.TrimSpace(href))
	if err != nil || href == "" || strings.HasPrefix(href, "#") {
		return 0
	}
	link = base.ResolveReference(link)
	if link.Scheme != base.Scheme || link.Host != base.Host || link.User != nil || link.Fragment != "" {
		return 0
	}
	if source == sourceHuaguo {
		if link.Path != base.Path {
			return 0
		}
		left, right := link.Query(), base.Query()
		number, _ := strconv.Atoi(left.Get("page"))
		left.Del("page")
		right.Del("page")
		if left.Encode() == right.Encode() {
			return number
		}
	} else if source == sourceWuguo {
		left, right := wuguoCatalogPagePath.FindStringSubmatch(link.Path), wuguoCatalogPagePath.FindStringSubmatch(base.Path)
		if len(left) == 3 && len(right) == 3 && left[1] == right[1] && link.RawQuery == base.RawQuery {
			if left[2] == "" {
				return 1
			}
			number, _ := strconv.Atoi(left[2])
			return number
		}
	}
	return 0
}

// sourceMaccmsPagination 返回站源MACCMS分页。
func sourceMaccmsPagination(ctx context.Context, document *html.Node, source, address string, requested int) (*nativeCatalogPagination, error) {
	if source != sourceHuaguo && source != sourceWuguo {
		return nil, nil
	}
	base, err := url.Parse(address)
	if err != nil {
		return nil, err
	}
	for _, pager := range providerHTMLNodes(document, func(node *html.Node) bool {
		if source == sourceWuguo {
			return providerHTMLClass(node, "paginationCom_pageContent")
		}
		if node.Data != "ul" {
			return false
		}
		for _, name := range strings.Fields(providerHTMLAttr(node, "class")) {
			if name == "page" || strings.HasSuffix(name, "-page") {
				return true
			}
		}
		return false
	}) {
		current, last := 0, 0
		for _, node := range providerHTMLNodes(pager, func(node *html.Node) bool { return node.Type == html.ElementNode }) {
			if !hanxiaoquanPagerVisible(node, pager) {
				continue
			}
			if providerHTMLClass(node, "paginationCom_activePage") || providerHTMLClass(node, "active") || providerHTMLAttr(node, "aria-current") == "page" {
				if number, parseErr := strconv.Atoi(strings.TrimSpace(providerHTMLText(node))); parseErr == nil && number > 0 {
					if current != 0 && current != number {
						return nil, errors.New("站点当前页标识冲突，未提交目录")
					}
					current, last = number, max(last, number)
				}
			}
			if node.Data == "a" {
				last = max(last, sourceMaccmsPageNumber(source, base, providerHTMLAttr(node, "href")))
			}
			if source == sourceWuguo && node.Data == "input" {
				if total, parseErr := strconv.Atoi(providerHTMLAttr(node, "data-total")); parseErr == nil {
					last = max(last, total)
				}
			}
		}
		if current == 0 {
			continue
		}
		if current != requested || last < current || last > 1000000 {
			return nil, errors.New("站点返回页码与请求不一致，未将重复页当作末页")
		}
		sourceSetCatalogPagination(ctx, current, last, source+"_visible_pager")
		return &nativeCatalogPagination{Current: current, TotalPages: last, Verified: true, Rule: source + "_visible_pager"}, nil
	}
	return nil, nil
}
