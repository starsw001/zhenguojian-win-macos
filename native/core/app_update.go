package core

import (
	"context"
	"duanjuapp/native/licensing"
	"duanjuapp/native/secureupdate"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"
)

type updateQuery struct {
	Version string `json:"version"`
	Edition string `json:"edition"`
	Ticket  string `json:"ticket"`
}
type updateResult struct {
	NoRelease bool   `json:"noRelease,omitempty"`
	Available bool   `json:"available"`
	Current   string `json:"current"`
	Version   string `json:"version"`
	Notes     string `json:"notes"`
	PageURL   string `json:"pageUrl"`
	AssetURL  string `json:"assetUrl"`
	Ticket    string `json:"ticket"`
	Error     string `json:"error,omitempty"`
}

var updates struct {
	sync.Mutex
	offer                *secureupdate.Offer
	state, message, slot string
	received, total      int64
}

// updateEdition 更新软件类型。
func updateEdition() string {
	if buildAllSources == "true" {
		return "zhenguoshang"
	}
	return "hongguoshang"
}

func nativeProgramRoot() string {
	executable, err := os.Executable()
	if err != nil {
		return ""
	}
	fallback := filepath.Dir(executable)
	root := filepath.Clean(os.Getenv("GUO_UPDATE_ROOT"))
	parent, parentErr := strconv.Atoi(os.Getenv("GUO_UPDATE_PARENT_PID"))
	if !filepath.IsAbs(root) || !secureupdate.ValidToken(os.Getenv("GUO_UPDATE_SESSION")) || parentErr != nil || parent <= 0 {
		return fallback
	}
	relative, err := filepath.Rel(root, executable)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return fallback
	}
	return root
}

// updateClient 取得update请求使用的客户端。
func (engine *nativeEngine) updateClient() *http.Client {
	engine.updateClientOnce.Do(func() {
		t := licensing.SystemTransport()
		t.ResponseHeaderTimeout = 15 * time.Second
		if engine.downloader != nil && engine.downloader.proxyRouter != nil {
			sys := t.Proxy
			router := engine.downloader.proxyRouter
			t.Proxy = func(r *http.Request) (*url.URL, error) {
				router.mu.RLock()
				mode, configured := router.mode, router.manual
				router.mu.RUnlock()
				switch mode {
				case "direct":
					return nil, nil
				case "manual":
					return configured, nil
				}
				return sys(r)
			}
		}
		engine.publicUpdateClient = secureupdate.NewPublicClient(t)
	})
	return engine.publicUpdateClient
}

// updateCheck 更新检查。
func (engine *nativeEngine) updateCheck(ctx context.Context, raw string) (updateResult, error) {
	var q updateQuery
	_ = json.Unmarshal([]byte(raw), &q)
	out := updateResult{Current: buildAppVersion}
	out.PageURL = secureupdate.ReleasesURL
	if runtime.GOOS != "windows" {
		return engine.passiveRelease(ctx, q)
	}
	if q.Version != buildAppVersion || q.Edition != updateEdition() {
		out.Error = "软件和当前更新通道不匹配"
		return out, nil
	}
	i, e := secureupdate.Runtime(buildLicenseDistributionKey, updateEdition())
	if e != nil {
		out.Error = e.Error()
		return out, nil
	}
	c, cancel := context.WithTimeout(ctx, 25*time.Second)
	defer cancel()
	o, e := secureupdate.Discover(c, engine.updateClient(), i.Edition, i.ID, i.Key, buildAppVersion)
	if errors.Is(e, secureupdate.ErrNoPublishedRelease) {
		out.NoRelease = true
		return out, nil
	}
	if e != nil {
		out.Error = e.Error()
		return out, nil
	}
	if o == nil {
		return out, nil
	}
	updates.Lock()
	if updates.state != "downloading" && updates.state != "extracting" && updates.state != "applying" {
		if updates.offer == nil || updates.offer.Release.SHA256 != o.Release.SHA256 {
			updates.state = "idle"
			updates.slot = ""
		}
		updates.offer = o
	}
	updates.Unlock()
	out.Available = true
	out.Version = o.Release.Version
	out.Notes = o.Notes
	out.PageURL = o.Page
	out.AssetURL = o.URL
	out.Ticket = o.Release.SHA256
	return out, nil
}

// activity 检查播放、下载等忙碌任务，并按 reserve 决定是否预留更新时段。
func (engine *nativeEngine) activity(reserve bool) error {
	engine.mu.Lock()
	defer engine.mu.Unlock()
	if len(engine.work) > 0 {
		return errors.New("本地媒体任务正在运行，请在空闲后更新")
	}
	engine.playbackMu.Lock()
	defer engine.playbackMu.Unlock()
	if engine.playbackCancel != nil {
		return errors.New("正在准备播放，暂缓更新")
	}
	m := engine.downloads
	if m == nil {
		return nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.moving || m.mediaBusy || len(m.active) > 0 {
		return errors.New("下载或媒体任务正在进行，请在空闲后更新")
	}
	for _, j := range m.jobs {
		if j.State == "queued" || j.State == "downloading" {
			return errors.New("下载队列正在工作，请在空闲后更新")
		}
	}
	if reserve {
		m.updateHandoff = true
		engine.updateHandoff = true
	}
	return nil
}

// updateDownload 更新下载。
func (engine *nativeEngine) updateDownload(raw string) (map[string]any, error) {
	var q updateQuery
	if runtime.GOOS != "windows" || json.Unmarshal([]byte(raw), &q) != nil || q.Edition != updateEdition() || q.Version != buildAppVersion || !secureupdate.ValidDigest(q.Ticket) {
		return nil, errors.New("更新请求与当前软件不符")
	}
	if e := engine.activity(false); e != nil {
		return nil, e
	}
	updates.Lock()
	if updates.offer == nil || updates.offer.Release.SHA256 != q.Ticket || updates.state == "downloading" || updates.state == "extracting" || updates.state == "applying" {
		updates.Unlock()
		return nil, errors.New("更新信息已变更或已有更新任务，请稍后重试")
	}
	updates.state, updates.message, updates.slot = "downloading", "", ""
	updates.received, updates.total = 0, updates.offer.Release.Size
	updates.Unlock()
	go engine.runUpdateDownload(q)
	return map[string]any{"state": "downloading"}, nil
}

// runUpdateDownload 执行Update下载。
func (engine *nativeEngine) runUpdateDownload(q updateQuery) {
	fail := func(e error) { updates.Lock(); updates.state, updates.message = "error", e.Error(); updates.Unlock() }
	updates.Lock()
	o := updates.offer
	updates.Unlock()
	if o == nil || q.Ticket == "" || o.Release.SHA256 != q.Ticket {
		fail(errors.New("没有已验签的更新清单"))
		return
	}
	i, e := secureupdate.Runtime(buildLicenseDistributionKey, updateEdition())
	if e != nil {
		fail(e)
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Minute)
	defer cancel()
	slot, e := i.Download(ctx, engine.updateClient(), *o, buildAppVersion, func(n, total int64) {
		updates.Lock()
		defer updates.Unlock()
		if n < 0 {
			updates.state = "extracting"
		} else {
			updates.received, updates.total = n, total
		}
	})
	if e != nil {
		fail(e)
		return
	}
	updates.Lock()
	updates.state, updates.slot = "ready", slot
	updates.Unlock()
}

// updateStatusValue 更新状态值。
func (engine *nativeEngine) updateStatusValue() map[string]any {
	updates.Lock()
	defer updates.Unlock()
	return map[string]any{"state": updates.state, "message": updates.message, "received": updates.received, "total": updates.total}
}

// updateApply 更新Apply。
func (engine *nativeEngine) updateApply() (map[string]any, error) {
	updates.Lock()
	defer updates.Unlock()
	if updates.state != "ready" || !secureupdate.ValidDigest(updates.slot) {
		return nil, errors.New("更新尚未通过验证")
	}
	i, e := secureupdate.Runtime(buildLicenseDistributionKey, updateEdition())
	if e != nil {
		return nil, e
	}
	if e = engine.activity(true); e != nil {
		return nil, e
	}
	if e = i.Request(os.Getenv("GUO_UPDATE_SESSION"), updates.slot); e != nil {
		if m := engine.downloads; m != nil {
			m.mu.Lock()
			m.updateHandoff = false
			m.mu.Unlock()
		}
		engine.playbackMu.Lock()
		engine.updateHandoff = false
		engine.playbackMu.Unlock()
		return nil, e
	}
	updates.state = "applying"
	return map[string]any{"restart": true}, nil
}

// updateReady 更新就绪。
func updateReady() (any, error) {
	if os.Getenv("GUO_UPDATE_TRIAL") != "1" {
		return map[string]any{}, nil
	}
	i, e := secureupdate.Runtime(buildLicenseDistributionKey, updateEdition())
	if e != nil {
		return nil, e
	}
	exe, e := os.Executable()
	if e != nil || filepath.Base(exe) != i.Edition+".exe" {
		return nil, errors.New("启动确认进程不匹配")
	}
	slot, e := i.CurrentStagedSlot(exe)
	if e != nil {
		return nil, e
	}
	if e = i.Ack(os.Getenv("GUO_UPDATE_SESSION")); e != nil {
		return nil, e
	}
	if e = i.WaitActive(slot, 15*time.Second); e != nil {
		return nil, e
	}
	if e = i.StartInstaller(slot); e != nil {
		_ = i.Commit(secureupdate.Selection{})
		return nil, e
	}
	return map[string]any{"installing": true}, nil
}
