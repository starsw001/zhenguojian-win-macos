package core

import (
	"net/url"

	"regexp"

	"strings"
)

var (
	badNameChars = regexp.MustCompile(`[\\/\?%\*:|"<>\x00-\x1f]+`)
	spaceChars   = regexp.MustCompile(`\s+`)
	rePublicURL  = regexp.MustCompile(`(?i)(?:https?|socks5h?)://[^\s"'<>]+`)
)

// publicError 包装面向调用方的错误，避免直接暴露敏感请求信息。
func publicError(err error) error {
	if err == nil {
		return nil
	}
	return redactedError{cause: err}
}

type redactedError struct{ cause error }

// Error 返回当前错误的可显示文本。
func (err redactedError) Error() string { return redactErrorString(err.cause.Error()) }

// Unwrap 返回底层错误，供 errors.Is 和 errors.As 继续匹配。
func (err redactedError) Unwrap() error { return err.cause }

// redactErrorString 隐藏错误文本中 URL 的凭据、查询参数和片段。
func redactErrorString(s string) string {
	return rePublicURL.ReplaceAllStringFunc(s, func(raw string) string {
		u, err := url.Parse(raw)
		if err != nil {
			return "[无效 URL 已隐藏]"
		}
		if u.Host == "" {
			return raw
		}
		if u.User != nil {
			u.User = url.User("redacted")
		}
		if u.RawQuery != "" {
			u.RawQuery = "[redacted]"
		}
		if u.Fragment != "" {
			u.Fragment = "redacted"
		}
		return u.String()
	})
}

// truncate 去除首尾空白，并按字符数上限截断文本。
func truncate(s string, n int) string {
	s = strings.TrimSpace(s)
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "..."
}
