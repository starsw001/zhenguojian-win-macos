package core

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/cookiejar"
	"os"
	"path/filepath"
	"strings"
)

var errSoraniLoginRequired = errors.New("青空需要登录，请到站源管理 → 青空 → 登录青空账号；没有账号请先到源站注册。")

type soraniAccountSession struct {
	Token        string `json:"accessToken"`
	RefreshToken string `json:"refreshToken"`
	Cookie       string `json:"cookie,omitempty"`
	UserAgent    string `json:"userAgent,omitempty"`
	Name         string `json:"name,omitempty"`
	jar          http.CookieJar
	epoch        uint64
}

func soraniNeedsLogin(message string) bool {
	for _, phrase := range []string{"请先登录", "需要登录", "尚未登录", "未登录", "登录已过期", "登录过期", "登录失效", "登录已失效", "重新登录"} {
		if strings.Contains(message, phrase) {
			return true
		}
	}
	return false
}

func (client *soraniAPIClient) accountOriginAllowed() bool {
	return client.base == soraniAPIBaseURL && !client.downloader.cfg.InsecureTLS
}

func (session *soraniAccountSession) prepare() error {
	for _, token := range []string{session.Token, session.RefreshToken} {
		if len(token) < 8 || len(token) > 32<<10 || strings.ContainsAny(token, " \t\r\n\x00") {
			return errors.New("青空登录状态无效，请在原站完成登录后重试")
		}
	}
	if len(session.Cookie) > 16<<10 || strings.ContainsAny(session.Cookie, "\r\n\x00") {
		return errors.New("青空登录 Cookie 无效")
	}
	if len(session.UserAgent) > 1024 || strings.ContainsAny(session.UserAgent, "\r\n\x00") {
		return errors.New("青空登录客户端信息无效")
	}
	jar, _ := cookiejar.New(nil)
	request, _ := http.NewRequest(http.MethodGet, soraniAPIBaseURL, nil)
	request.Header.Set("Cookie", session.Cookie)
	cookies := request.Cookies()
	if len(cookies) > 64 {
		return errors.New("青空登录 Cookie 数量无效")
	}
	for _, cookie := range cookies {
		cookie.Path, cookie.Secure, cookie.HttpOnly = "/", true, true
		if cookie.Valid() != nil {
			return errors.New("青空登录 Cookie 无效")
		}
	}
	jar.SetCookies(request.URL, cookies)
	session.jar = jar
	return nil
}

func (client *soraniAPIClient) accountPath() string {
	return filepath.Join(client.downloader.cfg.dataDir, "source-sorani-session.bin")
}

func (client *soraniAPIClient) loadAccount() {
	client.authOnce.Do(func() {
		if !sourceSessionPersistent || !client.accountOriginAllowed() {
			return
		}
		sealed, err := os.ReadFile(client.accountPath())
		if err != nil || len(sealed) > 128<<10 {
			return
		}
		plain, err := sourceSessionProtect(sealed, false)
		if err != nil {
			return
		}
		var session soraniAccountSession
		if json.Unmarshal(plain, &session) != nil || session.prepare() != nil {
			return
		}
		client.authMu.Lock()
		defer client.authMu.Unlock()
		client.authEpoch++
		session.epoch = client.authEpoch
		client.auth = &session
	})
}

func (client *soraniAPIClient) accountSnapshot() *soraniAccountSession {
	client.loadAccount()
	client.authMu.Lock()
	defer client.authMu.Unlock()
	if client.auth == nil {
		return nil
	}
	copy := *client.auth
	return &copy
}

func (client *soraniAPIClient) saveAccountLocked(session *soraniAccountSession) error {
	if !sourceSessionPersistent {
		return nil
	}
	if session == nil {
		if err := os.Remove(client.accountPath()); err != nil && !errors.Is(err, os.ErrNotExist) {
			return errors.New("无法清除青空本机登录状态，请检查数据目录权限")
		}
		return nil
	}
	plain, err := json.Marshal(session)
	if err != nil {
		return errors.New("无法保存青空登录状态")
	}
	sealed, err := sourceSessionProtect(plain, true)
	if err != nil {
		return err
	}
	return writeNativeCacheFile(client.accountPath(), sealed)
}

func (client *soraniAPIClient) accountStatus() map[string]any {
	session := client.accountSnapshot()
	status := map[string]any{"authenticated": session != nil, "persistent": sourceSessionPersistent, "name": "", "message": "青空需要登录；没有账号请先到源站自行注册。"}
	if session != nil {
		status["name"], status["message"] = session.Name, "已连接青空账号；播放权限以源站实时返回为准。"
	}
	return status
}

func (client *soraniAPIClient) connectAccount(ctx context.Context, candidate *soraniAccountSession) (map[string]any, error) {
	if !client.accountOriginAllowed() || candidate == nil {
		return nil, errors.New("只能从青空原站登录页连接账号")
	}
	client.loadAccount()
	client.authMu.Lock()
	epoch := client.authEpoch
	client.authMu.Unlock()
	session := *candidate
	if err := session.prepare(); err != nil {
		return nil, err
	}
	value, err := client.request(ctx, http.MethodGet, "/auth/profile", nil, 128<<10, &session)
	if err != nil {
		return nil, err
	}
	profile, ok := soraniPayload(value).(map[string]any)
	if !ok || nativeText(profile["id"]) == "" && mapString(profile, "username", "nickname", "displayName") == "" {
		return nil, errors.New("未取得青空账号资料，请先在网页中完成登录")
	}
	session.Name = truncate(mapString(profile, "displayName", "nickname", "username"), 80)
	client.authMu.Lock()
	if epoch != client.authEpoch {
		client.authMu.Unlock()
		return nil, errors.New("青空登录状态已变化，请重新操作")
	}
	if err := client.saveAccountLocked(&session); err != nil {
		client.authMu.Unlock()
		return nil, err
	}
	client.authEpoch++
	session.epoch = client.authEpoch
	client.auth = &session
	client.authMu.Unlock()
	return client.accountStatus(), nil
}

func (client *soraniAPIClient) disconnectAccount() (map[string]any, error) {
	client.loadAccount()
	client.authMu.Lock()
	client.authEpoch++
	client.auth = nil
	err := client.saveAccountLocked(nil)
	client.authMu.Unlock()
	if err != nil {
		return nil, err
	}
	return client.accountStatus(), nil
}

func (client *soraniAPIClient) refreshAccount(ctx context.Context, previous *soraniAccountSession) (*soraniAccountSession, error) {
	client.refreshMu.Lock()
	defer client.refreshMu.Unlock()
	current := client.accountSnapshot()
	if current == nil {
		return nil, errSoraniLoginRequired
	}
	if current.epoch != previous.epoch {
		return current, nil
	}
	value, err := client.request(ctx, http.MethodPost, "/auth/refresh", nil, 128<<10, &soraniAccountSession{Token: current.RefreshToken})
	if err != nil {
		if errors.Is(err, errSoraniLoginRequired) {
			client.authMu.Lock()
			if client.authEpoch == current.epoch {
				client.authEpoch++
				client.auth = nil
				_ = client.saveAccountLocked(nil)
			}
			client.authMu.Unlock()
			return nil, soraniExpiredError{}
		}
		return nil, err
	}
	row, _ := soraniPayload(value).(map[string]any)
	next := *current
	next.Token, next.RefreshToken = mapString(row, "token", "accessToken"), mapString(row, "refreshToken")
	if err := next.prepare(); err != nil {
		return nil, errors.New("青空刷新登录状态失败，请重新登录")
	}
	client.authMu.Lock()
	defer client.authMu.Unlock()
	if client.authEpoch != current.epoch {
		return nil, errors.New("青空登录状态已变化，请重新操作")
	}
	if err := client.saveAccountLocked(&next); err != nil {
		return nil, err
	}
	client.authEpoch++
	next.epoch = client.authEpoch
	client.auth = &next
	return &next, nil
}

type soraniExpiredError struct{}

func (soraniExpiredError) Error() string {
	return "青空登录已过期，请重新登录青空账号后播放。"
}
func (soraniExpiredError) Is(target error) bool { return target == errSoraniLoginRequired }

func (engine *nativeEngine) sourceAccount(ctx context.Context, input nativeInput) (any, error) {
	switch canonicalProviderSource(input.Source) {
	case sourcePiguo:
		return engine.piguoSourceAccount(ctx, input)
	case sourceSorani:
	default:
		return nil, errors.New("当前站源不支持此登录入口")
	}
	client := engine.downloader.soraniClient()
	switch input.Command {
	case "status":
		return client.accountStatus(), nil
	case "connect":
		status, err := client.connectAccount(ctx, input.SourceAuth)
		if err == nil {
			engine.changeSourceRecord(sourceSorani, func(record *nativeSourceRecord) {
				if soraniNeedsLogin(record.Error) || strings.Contains(record.Error, "青空次元该分集暂无可用播放地址") {
					record.Error = ""
					if !record.Running {
						record.Stage = "账号已连接，待重新检测"
					}
				}
			})
		}
		return status, err
	case "logout":
		return client.disconnectAccount()
	default:
		return nil, errors.New("青空账号操作无效")
	}
}
