package licensing

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"net"
	"net/http"
	"net/url"
	"path"
	"strings"
	"time"

	"golang.org/x/net/http/httpproxy"
)

var errLicenseRedirect = errors.New("授权请求不允许重定向")
var errLicenseProxy = errors.New("Windows 系统代理配置不可用")

// licenseProxy 组合显式环境代理与系统代理的选择逻辑。
func licenseProxy(system func(*http.Request) (*url.URL, error)) func(*http.Request) (*url.URL, error) {
	settings := httpproxy.FromEnvironment()
	if settings.HTTPSProxy == "" && !settings.CGI {
		settings.HTTPSProxy = settings.HTTPProxy
	}
	env := settings.ProxyFunc()
	exclusions := (&httpproxy.Config{HTTPProxy: "http://unused.invalid", HTTPSProxy: "http://unused.invalid", NoProxy: settings.NoProxy}).ProxyFunc()
	return func(r *http.Request) (*url.URL, error) {
		if p, e := exclusions(r.URL); p == nil || e != nil {
			return nil, e
		}
		if r.URL.Scheme == "https" && settings.HTTPSProxy != "" || r.URL.Scheme == "http" && settings.HTTPProxy != "" || settings.CGI {
			return env(r.URL)
		}
		return system(r)
	}
}

// windowsProxyForURL 查询指定 URL 的 Windows 系统代理设置。
func windowsProxyForURL(destination *url.URL, proxyList, bypass string) (*url.URL, error) {
	host := strings.ToLower(destination.Hostname())
	for _, rule := range strings.FieldsFunc(strings.ToLower(bypass), func(r rune) bool { return r == ';' || r == ',' || r == ' ' }) {
		if rule == "<local>" && !strings.Contains(host, ".") && net.ParseIP(host) == nil {
			return nil, nil
		}
		if ok, _ := path.Match(rule, host); ok {
			return nil, nil
		}
		if ok, _ := path.Match(rule, strings.ToLower(destination.Host)); ok {
			return nil, nil
		}
	}
	var chosen, generic, socks string
	for _, rule := range strings.FieldsFunc(proxyList, func(r rune) bool { return r == ';' || r == ' ' || r == '\t' }) {
		scheme, value, split := strings.Cut(rule, "=")
		switch {
		case split && strings.EqualFold(scheme, destination.Scheme) && chosen == "":
			chosen = value
		case split && strings.EqualFold(scheme, "socks") && socks == "":
			socks = value
		case !split && generic == "":
			generic = rule
		}
	}
	if chosen == "" {
		chosen = generic
	}
	prefix := "http://"
	if chosen == "" {
		chosen = socks
		prefix = "socks5://"
	}
	if chosen == "" || strings.EqualFold(chosen, "DIRECT") {
		return nil, nil
	}
	if !strings.Contains(chosen, "://") {
		chosen = prefix + chosen
	}
	u, e := url.Parse(chosen)
	if e != nil || u.Hostname() == "" || u.User != nil || u.Path != "" || u.RawQuery != "" || u.Fragment != "" {
		return nil, errLicenseProxy
	}
	switch u.Scheme {
	case "https", "http", "socks5", "socks5h":
		return u, nil
	}
	return nil, errLicenseProxy
}

// SystemTransport 创建遵循系统与环境代理设置的授权传输层。
func SystemTransport() *http.Transport {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = licenseProxy(platformLicenseProxy)
	return transport
}

// defaultLicenseClient 构造授权服务使用的默认 HTTP 客户端。
func defaultLicenseClient() *http.Client {
	return &http.Client{Transport: SystemTransport(), Timeout: 12 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return errLicenseRedirect }}
}

// licenseConnectionError 将底层授权连接错误转换为客户端提示。
func licenseConnectionError(err error) error {
	var cert *tls.CertificateVerificationError
	var unknown x509.UnknownAuthorityError
	var invalid x509.CertificateInvalidError
	var hostname x509.HostnameError
	var dns *net.DNSError
	var op *net.OpError
	var te net.Error
	switch {
	case errors.Is(err, errLicenseRedirect):
		return errors.New("许可证服务发生意外跳转，已阻止提交 [AUTH_REDIRECT]")
	case errors.Is(err, errLicenseProxy) || errors.As(err, &op) && op.Op == "proxyconnect":
		return errors.New("连不上已配置的代理，请检查 Windows 或环境变量中的代理设置 [AUTH_PROXY]")
	case errors.As(err, &cert) || errors.As(err, &unknown) || errors.As(err, &invalid) || errors.As(err, &hostname):
		return errors.New("许可证服务的 HTTPS 证书验证失败，请检查系统时间、受信任证书或网络代理 [AUTH_TLS]")
	case errors.As(err, &dns):
		return errors.New("无法解析许可证服务或代理的域名，请检查 DNS 或代理设置 [AUTH_DNS]")
	case errors.Is(err, context.DeadlineExceeded) || errors.As(err, &te) && te.Timeout():
		return errors.New("连接许可证服务超时，请检查网络或代理后重试 [AUTH_TIMEOUT]")
	default:
		return errors.New("无法连接许可证服务，请检查网络、防火墙或代理后重试 [AUTH_CONNECT]")
	}
}
