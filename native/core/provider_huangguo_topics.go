package core

import (
	"context"
	"errors"
	"net/url"
	"strings"
	"unicode"

	"golang.org/x/net/html"
)

func huangguoAITopicSlug(category string) (string, bool) {
	if !strings.HasPrefix(category, "topic:") {
		return "", false
	}
	encoded := strings.TrimPrefix(category, "topic:")
	if encoded == "" || len(encoded) > 120 {
		return "", false
	}
	slug, err := url.PathUnescape(encoded)
	if err != nil || slug == "" || slug == "." || slug == ".." || strings.ContainsAny(slug, "/\\?#\x00\r\n") || strings.ContainsFunc(slug, unicode.IsControl) {
		return "", false
	}
	return slug, true
}

func validHuangguoAITopicCategory(category string) bool {
	_, valid := huangguoAITopicSlug(category)
	return valid
}

func parseHuangguoAITopics(document *html.Node, pageURL string) []nativeCategory {
	grid := providerHTMLFirstClass(document, "hg-topic-grid")
	base, err := url.Parse(pageURL)
	if err != nil || grid == nil {
		return nil
	}
	seen := map[string]bool{}
	var topics []nativeCategory
	for _, anchor := range providerHTMLNodes(grid, func(node *html.Node) bool {
		return node.Type == html.ElementNode && node.Data == "a" && providerHTMLClass(node, "hg-topic-card")
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
		if len(parts) != 2 || parts[0] != "topics" {
			continue
		}
		id := "topic:" + url.PathEscape(parts[1])
		if !validHuangguoAITopicCategory(id) || seen[id] {
			continue
		}
		title := providerHTMLFirstClass(anchor, "hg-topic-card__title")
		name := providerHTMLText(title)
		if name == "" || len([]rune(name)) > 48 || strings.ContainsFunc(name, unicode.IsControl) {
			continue
		}
		seen[id] = true
		topics = append(topics, nativeCategory{ID: id, Name: name, Kind: "topic"})
		if len(topics) >= nativeCategoryMaxEntries {
			break
		}
	}
	return topics
}

func (d *Downloader) fetchHuangguoAITopics(ctx context.Context) ([]nativeCategory, error) {
	ctx = context.WithValue(ctx, providerTextNoCacheKey{}, true)
	ctx, base := d.huangguoAICatalogContext(ctx)
	address := strings.TrimRight(base, "/") + "/topics/"
	document, actual, err := d.fetchProviderPage(ctx, address, base+"/", "")
	if err != nil {
		return nil, err
	}
	topics := parseHuangguoAITopics(document, actual)
	if len(topics) == 0 {
		return nil, errors.New("站源未返回专题聚合，保留现有目录，请稍后重试")
	}
	return topics, nil
}
