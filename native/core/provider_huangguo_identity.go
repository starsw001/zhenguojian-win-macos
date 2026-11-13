package core

import (
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strconv"
	"strings"
)

var huangguoWorkPath = regexp.MustCompile(`^/(detail|video)/([^/]+)(?:/|$)`)
var huangguoEpisodePath = regexp.MustCompile(`^/video/([^/]+)/ep-([0-9]+)/?$`)
var huangguoWorkToken = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.-]{0,191}$`)

// huangguoEpisodeLabelIndex 从分集标签中解析明确集号。
func huangguoEpisodeLabelIndex(label string) int {
	label = strings.TrimSpace(label)
	if index := episodeIndex(label, 0); index > 0 {
		return index
	}
	if index, err := strconv.Atoi(label); err == nil && index > 0 {
		return index
	}
	if label == "第一集" || label == "第一话" || label == "第一期" {
		return 1
	}
	return 0
}

// huangguoWorkRoute 取得作品所对应的来源路由信息。
func huangguoWorkRoute(raw string) (string, string, bool) {
	u, err := url.Parse(raw)
	if err != nil {
		return "", "", false
	}
	match := huangguoWorkPath.FindStringSubmatch(u.Path)
	if len(match) != 3 || !huangguoWorkToken.MatchString(match[2]) {
		return "", "", false
	}
	return match[1], match[2], true
}

// huangguoWorkAddress 取得作品所对应的详情地址。
func huangguoWorkAddress(base, reference, identifier string) (string, string, error) {
	parent, err := url.Parse(base)
	if err != nil || parent.Host == "" || parent.User != nil || parent.Scheme != "https" && parent.Scheme != "http" {
		return "", "", errors.New("目录缺少有效站点地址，请重新更新目录")
	}
	if _, id, valid := huangguoWorkRoute(identifier); valid {
		if u, parseErr := url.Parse(identifier); parseErr == nil && u.IsAbs() && reference == "" {
			reference = identifier
		}
		identifier = id
	}
	if !huangguoWorkToken.MatchString(identifier) {
		return "", "", errors.New("作品标识无效，请重新更新目录")
	}
	if reference == "" {
		reference = "/detail/" + url.PathEscape(identifier) + "/"
	}
	link, err := url.Parse(reference)
	if err != nil {
		return "", "", errors.New("作品详情地址无效")
	}
	resolved := parent.ResolveReference(link)
	kind, owner, valid := huangguoWorkRoute(resolved.String())
	if !valid || owner != identifier || resolved.User != nil || !strings.EqualFold(resolved.Host, parent.Host) || resolved.Scheme != parent.Scheme {
		return "", "", errors.New("目录详情地址指向其他域名或作品，已阻止混用")
	}
	resolved.Path, resolved.RawPath, resolved.RawQuery, resolved.Fragment = "/"+kind+"/"+owner+"/", "", "", ""
	resolved.Host = strings.ToLower(resolved.Host)
	return resolved.String(), owner, nil
}

// bindHuangguoWork 关联黄果作品。
func bindHuangguoWork(drama Drama, pageURL, reference string) (Drama, error) {
	address, _, err := huangguoWorkAddress(pageURL, reference, drama.SourceID)
	if err != nil {
		return Drama{}, err
	}
	drama.Source, drama.SourceID = sourceHuangguoAI, address
	drama.ID = providerDramaID(sourceHuangguoAI, address)
	return drama, nil
}

// huangguoWorkOrigin 取得作品绑定的来源站点。
func huangguoWorkOrigin(address string) string {
	u, err := url.Parse(address)
	if err != nil || !u.IsAbs() || u.User != nil || u.Host == "" || u.Scheme != "https" && u.Scheme != "http" {
		return ""
	}
	return u.Scheme + "://" + strings.ToLower(u.Host)
}

// validateHuangguoWorkChapter 校验黄果作品分集。
func validateHuangguoWorkChapter(identifier string, chapter Chapter) error {
	origin := huangguoWorkOrigin(identifier)
	_, owner, valid := huangguoWorkRoute(identifier)
	_, actualOwner, actualValid := huangguoWorkRoute(chapter.PageURL)
	if origin == "" || !valid {
		return errors.New("旧目录条目缺少域名身份，请更新目录后重新打开；已有下载与观看记录保留")
	}
	if !actualValid || actualOwner != owner || huangguoWorkOrigin(chapter.PageURL) != origin {
		return errors.New("分集地址不属于当前域名和作品，已阻止混入其他剧集")
	}
	if chapter.Source != sourceHuangguoAI || !strings.HasPrefix(chapter.ID, providerChapterID(sourceHuangguoAI, identifier, "")) {
		return errors.New("分集标识与当前作品不一致")
	}
	return nil
}

// validateHuangguoWorkDetail 校验黄果作品详情。
func validateHuangguoWorkDetail(downloader *Downloader, selected nativeDrama, raw Drama, chapters []Chapter) error {
	_, identifier, valid := splitProviderDramaID(selected.ID)
	if !valid || huangguoWorkOrigin(identifier) == "" || raw.ID != selected.ID || raw.SourceID != identifier || raw.Source != sourceHuangguoAI {
		return errors.New("详情的域名或作品标识与目录不一致，请重新更新该源")
	}
	selectedTitle := ordinaryTitle(selected.Title)
	detailTitle := ordinaryTitle(raw.DisplayTitle())
	if !strings.Contains(detailTitle, selectedTitle) {
		downloader.recordDiagnostic(diagnosticEvent{Event: "detail.huangguo_title_mismatch", Level: "warning", Source: sourceHuangguoAI,
			DramaID: selected.ID, DramaTitle: selectedTitle, Message: fmt.Sprintf("黄果 AI 标题判断未放过：ordinaryTitle(selected.Title)=%q；ordinaryTitle(raw.DisplayTitle())=%q", selectedTitle, detailTitle)})
		return errors.New("详情名称与目录作品不一致，未采用同名以外或其他作品的分集")
	}
	for _, chapter := range chapters {
		if err := validateHuangguoWorkChapter(identifier, chapter); err != nil {
			return fmt.Errorf("分集归属校验失败：%w", err)
		}
	}
	return nil
}
