package core

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const piguoBrowserUserAgent = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/150.0.0.0 Safari/537.36"

type piguoAccessSession struct {
	Cookie    string `json:"cookie"`
	UserAgent string `json:"userAgent"`
	SavedAt   int64  `json:"savedAt"`
}

type piguoAccessSessionKey struct{}

func piguoAccessCookie(raw string) (string, error) {
	if raw == "" || len(raw) > 32<<10 || strings.ContainsAny(raw, "\r\n\x00") {
		return "", errors.New("皮果浏览器验证状态无效")
	}
	request, _ := http.NewRequest(http.MethodGet, piguoBaseURL+"/", nil)
	request.Header.Set("Cookie", raw)
	var values []string
	clearance := false
	for _, cookie := range request.Cookies() {
		if len(values) >= 16 || len(cookie.Name) > 128 || len(cookie.Value) > 8<<10 {
			return "", errors.New("皮果浏览器验证状态无效")
		}
		if cookie.Name != "cf_clearance" && cookie.Name != "__cf_bm" && cookie.Name != "_cfuvid" && !strings.HasPrefix(cookie.Name, "cf_chl_") {
			continue
		}
		if cookie.Valid() != nil || strings.ContainsAny(cookie.Value, ";\r\n\x00") {
			return "", errors.New("皮果浏览器验证状态无效")
		}
		clearance = clearance || cookie.Name == "cf_clearance" && cookie.Value != ""
		values = append(values, cookie.Name+"="+cookie.Value)
	}
	if !clearance {
		return "", errors.New("皮果原站验证尚未完成，请在内置页面通过浏览器验证后重试")
	}
	return strings.Join(values, "; "), nil
}

func (session *piguoAccessSession) prepare() error {
	if session == nil || session.UserAgent != piguoBrowserUserAgent {
		return errors.New("皮果浏览器客户端信息无效，请重新打开内置验证页")
	}
	cookie, err := piguoAccessCookie(session.Cookie)
	if err != nil {
		return err
	}
	session.Cookie = cookie
	if session.SavedAt <= 0 {
		session.SavedAt = time.Now().Unix()
	}
	return nil
}

func (d *Downloader) piguoAccessPath() string {
	return filepath.Join(d.cfg.dataDir, "source-piguo-session.bin")
}

func (d *Downloader) loadPiguoAccess() {
	d.piguoAccessOnce.Do(func() {
		if !sourceSessionPersistent {
			return
		}
		sealed, err := os.ReadFile(d.piguoAccessPath())
		if err != nil || len(sealed) > 128<<10 {
			return
		}
		plain, err := sourceSessionProtect(sealed, false)
		if err != nil {
			return
		}
		var session piguoAccessSession
		if json.Unmarshal(plain, &session) != nil || session.prepare() != nil {
			return
		}
		d.piguoAccessMu.Lock()
		d.piguoAccess = &session
		d.piguoAccessMu.Unlock()
	})
}

func (d *Downloader) piguoAccessSnapshot() *piguoAccessSession {
	d.loadPiguoAccess()
	d.piguoAccessMu.RLock()
	defer d.piguoAccessMu.RUnlock()
	if d.piguoAccess == nil {
		return nil
	}
	session := *d.piguoAccess
	return &session
}

func (d *Downloader) savePiguoAccess(session *piguoAccessSession) error {
	if !sourceSessionPersistent {
		return nil
	}
	if session == nil {
		if err := os.Remove(d.piguoAccessPath()); err != nil && !errors.Is(err, os.ErrNotExist) {
			return errors.New("无法清除皮果本机验证状态，请检查数据目录权限")
		}
		return nil
	}
	plain, err := json.Marshal(session)
	if err != nil {
		return errors.New("无法保存皮果浏览器验证状态")
	}
	sealed, err := sourceSessionProtect(plain, true)
	if err != nil {
		return err
	}
	return writeNativeCacheFile(d.piguoAccessPath(), sealed)
}

func (d *Downloader) applyPiguoAccessSession(ctx context.Context, rawURL string, request *http.Request) {
	if request == nil || providerSourceForURL(rawURL) != sourcePiguo || providerSourceForURL(request.URL.String()) != sourcePiguo {
		return
	}
	session, _ := ctx.Value(piguoAccessSessionKey{}).(*piguoAccessSession)
	if session == nil {
		session = d.piguoAccessSnapshot()
	}
	if session == nil || session.prepare() != nil {
		return
	}
	request.Header.Set("Cookie", session.Cookie)
	request.Header.Set("User-Agent", session.UserAgent)
}

func (d *Downloader) piguoAccessStatus() map[string]any {
	connected := d.piguoAccessSnapshot() != nil
	message := "皮果当前要求浏览器验证；请在内置原站页手动完成，软件不会复制系统浏览器会话。"
	if connected {
		message = "已连接皮果原站验证会话；目录、搜索、详情和海报将复用该会话。"
	}
	return map[string]any{"authenticated": connected, "persistent": sourceSessionPersistent, "message": message}
}

func (d *Downloader) connectPiguoAccess(ctx context.Context, input *soraniAccountSession) (map[string]any, error) {
	if input == nil {
		return nil, errors.New("未收到皮果浏览器验证状态")
	}
	session := &piguoAccessSession{Cookie: input.Cookie, UserAgent: input.UserAgent, SavedAt: time.Now().Unix()}
	if err := session.prepare(); err != nil {
		return nil, err
	}
	probe := context.WithValue(ctx, piguoAccessSessionKey{}, session)
	probe = context.WithValue(probe, providerTextNoCacheKey{}, true)
	items, _, err := d.fetchMaccmsCatalogPage(probe, sourcePiguo, 1, "67")
	if err != nil {
		return nil, err
	}
	if len(items) == 0 {
		return nil, errors.New("皮果验证页已打开，但当前目录没有返回可识别作品")
	}
	d.piguoAccessMu.Lock()
	if err = d.savePiguoAccess(session); err == nil {
		d.piguoAccess = session
	}
	d.piguoAccessMu.Unlock()
	if err != nil {
		return nil, err
	}
	return d.piguoAccessStatus(), nil
}

func (d *Downloader) disconnectPiguoAccess() (map[string]any, error) {
	d.loadPiguoAccess()
	d.piguoAccessMu.Lock()
	err := d.savePiguoAccess(nil)
	if err == nil {
		d.piguoAccess = nil
	}
	d.piguoAccessMu.Unlock()
	if err != nil {
		return nil, err
	}
	return d.piguoAccessStatus(), nil
}

func (engine *nativeEngine) piguoSourceAccount(ctx context.Context, input nativeInput) (any, error) {
	switch input.Command {
	case "status":
		return engine.downloader.piguoAccessStatus(), nil
	case "connect":
		status, err := engine.downloader.connectPiguoAccess(ctx, input.SourceAuth)
		if err == nil {
			engine.changeSourceRecord(sourcePiguo, func(record *nativeSourceRecord) {
				if strings.Contains(record.Error, "浏览器验证") || strings.Contains(strings.ToLower(record.Error), "cloudflare") || strings.Contains(record.Error, "状态码403") {
					record.Error = ""
					if !record.Running {
						record.Stage = "浏览器验证已连接，待重新检测"
					}
				}
			})
		}
		return status, err
	case "logout":
		return engine.downloader.disconnectPiguoAccess()
	default:
		return nil, errors.New("皮果浏览器验证操作无效")
	}
}
