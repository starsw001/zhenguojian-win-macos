package secureupdate

import (
	"bytes"
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"golang.org/x/net/html"
)

var errPublicRedirect = errors.New("拒绝不可信的更新重定向")

const ReleaseFeedURL = ReleasesURL + ".atom"
const atomNamespace = "http://www.w3.org/2005/Atom"

type atomReleaseLink struct {
	Rel  string `xml:"rel,attr"`
	Href string `xml:"href,attr"`
}
type atomEntry struct {
	Links []atomReleaseLink `xml:"http://www.w3.org/2005/Atom link"`
}
type publicReleaseFeed struct {
	XMLName xml.Name    `xml:"http://www.w3.org/2005/Atom feed"`
	ID      string      `xml:"http://www.w3.org/2005/Atom id"`
	Entries []atomEntry `xml:"http://www.w3.org/2005/Atom entry"`
}

// linkedRelease 从链接解析发布版本信息。
func linkedRelease(address string, relative bool) (PublishedRelease, bool) {
	u, e := url.Parse(address)
	if e != nil || u.Opaque != "" || u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" {
		return PublishedRelease{}, false
	}
	if relative && u.Scheme == "" && u.Host == "" && strings.HasPrefix(address, "/") && !strings.HasPrefix(address, "//") {
		u.Scheme = "https"
		u.Host = "github.com"
	}
	prefix := "/" + Repository + "/releases/tag/"
	if u.Scheme != "https" || !strings.EqualFold(u.Host, "github.com") || !strings.HasPrefix(u.EscapedPath(), prefix) {
		return PublishedRelease{}, false
	}
	raw := strings.TrimPrefix(u.EscapedPath(), prefix)
	tag, e := url.PathUnescape(raw)
	if e != nil || strings.ContainsAny(tag, "/\\") {
		return PublishedRelease{}, false
	}
	r := PublishedRelease{Tag: tag}
	return r, r.Version() != ""
}

// chooseRelease 在已发现的发布条目中选择符合条件的候选。
func chooseRelease(best *PublishedRelease, href string, relative bool) bool {
	r, ok := linkedRelease(href, relative)
	if ok && comparePublished(r.Version(), best.Version()) > 0 {
		*best = r
	}
	return ok
}

// parseReleaseFeed 解析发布订阅源中的版本条目。
func parseReleaseFeed(raw []byte) (PublishedRelease, error) {
	var f publicReleaseFeed
	if e := xml.Unmarshal(raw, &f); e != nil || strings.TrimSpace(f.ID) != "tag:github.com,2008:"+ReleasesURL {
		return PublishedRelease{}, errors.New("GitHub 订阅内容与指定更新仓库不匹配")
	}
	if len(f.Entries) == 0 {
		return PublishedRelease{}, ErrNoPublishedRelease
	}
	var best PublishedRelease
	for _, entry := range f.Entries {
		for _, a := range entry.Links {
			if a.Rel == "alternate" || a.Rel == "" {
				chooseRelease(&best, a.Href, false)
			}
		}
	}
	if best.Tag == "" {
		return best, errors.New("订阅没有可核实的本仓库版本标签")
	}
	return best, nil
}

// htmlAttr 读取 HTML 节点上的指定属性。
func htmlAttr(n *html.Node, key string) string {
	for _, a := range n.Attr {
		if a.Key == key {
			return a.Val
		}
	}
	return ""
}

// hasClass 判断 HTML 节点是否具有指定类名。
func hasClass(n *html.Node, name string) bool {
	for _, c := range strings.Fields(htmlAttr(n, "class")) {
		if c == name {
			return true
		}
	}
	return false
}

// releaseHeading 从发布页标题区域提取版本信息。
func releaseHeading(n *html.Node) bool {
	if !hasClass(n, "Link--primary") {
		return false
	}
	title := false
	for p := n.Parent; p != nil; p = p.Parent {
		if hasClass(p, "markdown-body") {
			return false
		}
		if p.Data == "h1" || p.Data == "h2" || p.Data == "h3" || hasClass(p, "f1") {
			title = true
		}
		if hasClass(p, "Box-body") {
			return title
		}
	}
	return false
}

// parseReleasePage 解析发布页中的版本链接与条目。
func parseReleasePage(raw []byte) (PublishedRelease, error) {
	root, e := html.Parse(bytes.NewReader(raw))
	if e != nil {
		return PublishedRelease{}, errors.New("无法识别 GitHub 发布页")
	}
	var best PublishedRelease
	var scan func(*html.Node)
	scan = func(n *html.Node) {
		if n.Type == html.ElementNode && n.Data == "a" && releaseHeading(n) {
			chooseRelease(&best, htmlAttr(n, "href"), true)
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			scan(c)
		}
	}
	scan(root)
	if best.Tag == "" {
		return best, errors.New("未能从 GitHub 发布页确认最新版本")
	}
	return best, nil
}

// LatestPublishedRelease 从配置的发布渠道读取当前最新版本信息。
func LatestPublishedRelease(ctx context.Context, c *http.Client) (PublishedRelease, error) {
	feedCtx, stop := context.WithTimeout(ctx, 10*time.Second)
	b, e := readWeb(feedCtx, c, ReleaseFeedURL, 2<<20)
	stop()
	if e == nil {
		return parseReleaseFeed(b)
	}
	var h updateHTTPStatus
	var limit *updateLimit
	if ctx.Err() != nil || errors.Is(e, errPublicRedirect) || errors.As(e, &limit) || errors.As(e, &h) && (h != 404 && int(h) < 500) {
		return PublishedRelease{}, fmt.Errorf("读取 GitHub 版本订阅：%w", publicError(e))
	}
	b, e = readWeb(ctx, c, ReleasesURL, 4<<20)
	if e != nil {
		return PublishedRelease{}, fmt.Errorf("读取 GitHub 发布页：%w", publicError(e))
	}
	return parseReleasePage(b)
}
