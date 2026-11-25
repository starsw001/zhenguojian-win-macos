package core

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"path"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"time"

	"golang.org/x/net/publicsuffix"
)

const hongguoPlaybackVersionCode = 73932
const hongguoPlaybackVersionName = "7.3.9.32"
const hongguoPlaybackRegistrationUserAgent = "com.phoenix.read/72232 (Linux; U; Android 16; zh_CN; 25053RT47C; Build/BP2A.250605.031.A3)"

type hongguoPlaybackCookie struct {
	Origin string      `json:"origin"`
	Cookie http.Cookie `json:"cookie"`
}

type hongguoPlaybackCookieObserverKey struct{}
type hongguoPlaybackCookieObserver func(*http.Request, *http.Response) error

// hongguoPlaybackCookieHost 判断 Cookie 主机是否属于允许的播放身份范围。
func hongguoPlaybackCookieHost(host string) bool {
	return host == "log5-applog-lf.fqnovel.com" || host == "api5-normal-sinfonlineb.fqnovel.com"
}

// valid 判断红果播放身份 Cookie的字段与约束是否有效。
func (saved hongguoPlaybackCookie) valid() bool {
	uri, err := url.Parse(saved.Origin)
	if err != nil || uri.Scheme != "https" || uri.User != nil || uri.Port() != "" || uri.Path != "/" || uri.RawQuery != "" || uri.Fragment != "" || !hongguoPlaybackCookieHost(uri.Hostname()) {
		return false
	}
	cookie := saved.Cookie
	switch cookie.Name {
	case "install_id", "ttreq", "store-region", "store-region-src":
	default:
		return false
	}
	domain := strings.TrimPrefix(cookie.Domain, ".")
	return cookie.Valid() == nil && cookie.MaxAge == 0 && len(cookie.Value) <= 2048 && cookie.Secure &&
		(domain == "" || domain == "fqnovel.com" || domain == uri.Hostname()) &&
		strings.HasPrefix(cookie.Path, "/") && len(cookie.Path) <= 256
}

// cloneHongguoPlaybackDevice 复制红果播放设备身份。
func cloneHongguoPlaybackDevice(device hongguoPlaybackDevice) hongguoPlaybackDevice {
	device.Cookies = append([]hongguoPlaybackCookie(nil), device.Cookies...)
	return device
}

// hongguoMergePlaybackCookies 合并符合播放身份保存规则的响应 Cookie。
func hongguoMergePlaybackCookies(saved []hongguoPlaybackCookie, request *http.Request, response *http.Response, now time.Time) []hongguoPlaybackCookie {
	if request.URL.Scheme != "https" || !hongguoPlaybackCookieHost(request.URL.Hostname()) {
		return append([]hongguoPlaybackCookie(nil), saved...)
	}
	out := make([]hongguoPlaybackCookie, 0, 8)
	for _, item := range saved {
		if item.valid() && (item.Cookie.Expires.IsZero() || item.Cookie.Expires.After(now)) {
			out = append(out, item)
		}
	}
	for _, received := range response.Cookies() {
		cookie := http.Cookie{Name: received.Name, Value: received.Value, Domain: strings.ToLower(received.Domain), Path: received.Path, Secure: true, HttpOnly: received.HttpOnly, Expires: received.Expires}
		if cookie.Path == "" {
			cookie.Path = path.Dir(request.URL.Path)
			if !strings.HasPrefix(cookie.Path, "/") {
				cookie.Path = "/"
			}
		}
		if received.MaxAge > 0 {
			cookie.Expires = now.Add(time.Duration(min(received.MaxAge, 365*24*60*60)) * time.Second)
		}
		item := hongguoPlaybackCookie{Origin: "https://" + request.URL.Hostname() + "/", Cookie: cookie}
		if !item.valid() {
			continue
		}
		for i := len(out) - 1; i >= 0; i-- {
			other := out[i]
			sameDomain := strings.TrimPrefix(other.Cookie.Domain, ".") == strings.TrimPrefix(cookie.Domain, ".")
			if cookie.Domain == "" {
				sameDomain = sameDomain && other.Origin == item.Origin
			}
			if other.Cookie.Name == cookie.Name && other.Cookie.Path == cookie.Path && sameDomain {
				out = append(out[:i], out[i+1:]...)
			}
		}
		if received.MaxAge >= 0 && (cookie.Expires.IsZero() || cookie.Expires.After(now)) && len(out) < 8 {
			out = append(out, item)
		}
	}
	return out
}

// hongguoApplyPlaybackCookies 将当前身份适用的 Cookie 加入播放请求。
func hongguoApplyPlaybackCookies(request *http.Request, device hongguoPlaybackDevice, now time.Time) {
	if request.URL.Scheme != "https" || !hongguoPlaybackCookieHost(request.URL.Hostname()) {
		return
	}
	jar, _ := cookiejar.New(&cookiejar.Options{PublicSuffixList: publicsuffix.List})
	for _, saved := range device.Cookies {
		if !saved.valid() || !saved.Cookie.Expires.IsZero() && !saved.Cookie.Expires.After(now) {
			continue
		}
		origin, _ := url.Parse(saved.Origin)
		cookie := saved.Cookie
		jar.SetCookies(origin, []*http.Cookie{&cookie})
	}
	request.Header.Del("Cookie")
	for _, cookie := range jar.Cookies(request.URL) {
		request.AddCookie(cookie)
	}
}

// hongguoRememberPlaybackCookies 记录播放响应中需要持久保存的身份 Cookie。
func (d *Downloader) hongguoRememberPlaybackCookies(device hongguoPlaybackDevice, request *http.Request, response *http.Response) error {
	if len(response.Cookies()) == 0 {
		return nil
	}
	client := d.hongguoClient()
	client.playbackDeviceMu.Lock()
	defer client.playbackDeviceMu.Unlock()
	if client.playbackDevice == nil || client.playbackDevice.DeviceID != device.DeviceID || client.playbackDevice.InstallID != device.InstallID {
		return nil
	}
	updated := cloneHongguoPlaybackDevice(*client.playbackDevice)
	updated.Cookies = hongguoMergePlaybackCookies(updated.Cookies, request, response, time.Now())
	if reflect.DeepEqual(updated.Cookies, client.playbackDevice.Cookies) {
		return nil
	}
	body, err := json.Marshal(updated)
	if err == nil {
		err = nativeDownloadWrite(filepath.Join(d.cfg.dataDir, "hongguo-playback-device.json"), body)
	}
	if err != nil {
		return &hongguoPlaybackResponseError{"无法保存红果播放会话，未修改设备身份或其他记录"}
	}
	client.playbackDevice = &updated
	return nil
}

// hongguoPlaybackResponseContext 记录播放响应对应的设备与请求上下文。
func hongguoPlaybackResponseContext(ctx context.Context, observer hongguoPlaybackCookieObserver) context.Context {
	return context.WithValue(ctx, hongguoPlaybackCookieObserverKey{}, observer)
}

// prepareHongguoPlaybackRequest 为红果播放请求设置参数、请求头及当前身份凭据。
func prepareHongguoPlaybackRequest(request *http.Request, body []byte, device hongguoPlaybackDevice, now time.Time) {
	query := hongguoAppQuery(device.DeviceID, device.InstallID)
	query.Set("_rticket", strconv.FormatInt(now.UnixMilli(), 10))
	request.URL.RawQuery = query.Encode()
	request.Header.Set("User-Agent", hongguoAppUserAgent)
	request.Header.Set("Accept", "application/json")
	request.Header.Set("Accept-Encoding", "identity")
	request.Header.Set("Content-Type", "application/json; charset=utf-8")
	request.Header.Set("X-XS-From-Web", "0")
	request.Header.Set("Sdk-Version", "2")
	hongguoApplyPlaybackCookies(request, device, now)
	signHongguoPlayerRequest(request, body, now, device.InstallID)
}
