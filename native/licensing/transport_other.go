//go:build !windows

package licensing

import (
	"net/http"
	"net/url"
)

// platformLicenseProxy 读取当前平台可用的许可证代理配置。
func platformLicenseProxy(*http.Request) (*url.URL, error) { return nil, nil }
