package core

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

type nativeResourceSettings struct {
	LogDirectory              string                      `json:"logDirectory"`
	LogPath                   string                      `json:"logPath,omitempty"`
	DefaultLogDirectory       string                      `json:"defaultLogDirectory,omitempty"`
	CacheLimits               nativeCacheLimits           `json:"cacheLimits,omitempty"`
	CacheUsage                map[string]nativeCacheUsage `json:"cacheUsage,omitempty"`
	ProxyMode                 string                      `json:"proxyMode"`
	ProxyURL                  string                      `json:"proxyUrl"`
	CatalogConcurrency        int                         `json:"catalogConcurrency"`
	CatalogIntervalMS         int                         `json:"catalogIntervalMs"`
	DownloadConcurrency       int                         `json:"downloadConcurrency"`
	DownloadSourceConcurrency int                         `json:"downloadSourceConcurrency"`
	DownloadBySource          bool                        `json:"downloadBySource"`
	Warning                   string                      `json:"warning,omitempty"`
	SystemProxyStatus         string                      `json:"systemProxyStatus,omitempty"`
}

// defaultResourceSettings 构造默认资源设置。
func defaultResourceSettings() nativeResourceSettings {
	return nativeResourceSettings{ProxyMode: "auto", CatalogConcurrency: 3, CatalogIntervalMS: 250, DownloadConcurrency: 2, DownloadSourceConcurrency: 1, CacheLimits: defaultNativeCacheLimits()}
}

// UnmarshalJSON 将 JSON 数据解码为资源设置，并处理对应字段转换。
func (settings *nativeResourceSettings) UnmarshalJSON(data []byte) error {
	type storedSettings nativeResourceSettings
	stored := storedSettings{DownloadSourceConcurrency: 1}
	if err := json.Unmarshal(data, &stored); err != nil {
		return err
	}
	*settings = nativeResourceSettings(stored)
	return nil
}

// validate 校验资源设置的字段与约束是否有效。
func (settings nativeResourceSettings) validate() error {
	if settings.LogDirectory != "" && (!filepath.IsAbs(settings.LogDirectory) || len(settings.LogDirectory) > 4096 || strings.ContainsRune(settings.LogDirectory, 0)) {
		return errors.New("请选择有效的绝对日志目录，留空则使用软件默认目录")
	}
	if err := settings.CacheLimits.validate(); err != nil {
		return err
	}
	if settings.CatalogConcurrency < 1 || settings.CatalogConcurrency > 6 ||
		settings.CatalogIntervalMS < 0 || settings.CatalogIntervalMS > 5000 ||
		settings.DownloadConcurrency < 1 || settings.DownloadConcurrency > 10 ||
		settings.DownloadSourceConcurrency < 1 || settings.DownloadSourceConcurrency > 10 {
		return errors.New("目录并发应为 1 至 6，下载总并发及单站点并发应为 1 至 10，请求间隔应为 0 至 5000 毫秒")
	}
	if settings.ProxyMode != "auto" && settings.ProxyMode != "direct" && settings.ProxyMode != "manual" {
		return errors.New("代理模式无效")
	}
	if settings.ProxyMode == "manual" || settings.ProxyURL != "" {
		if len(settings.ProxyURL) > 2048 {
			return errors.New("代理地址过长")
		}
		parsed, err := url.Parse(settings.ProxyURL)
		if err != nil || parsed.Hostname() == "" || parsed.Fragment != "" || parsed.RawQuery != "" ||
			(parsed.Path != "" && parsed.Path != "/") ||
			(parsed.Scheme != "http" && parsed.Scheme != "https" && parsed.Scheme != "socks5" && parsed.Scheme != "socks5h") {
			return errors.New("请输入有效的 HTTP、HTTPS 或 SOCKS5 代理地址")
		}
		if port := parsed.Port(); port != "" {
			number, err := strconv.Atoi(port)
			if err != nil || number < 1 || number > 65535 {
				return errors.New("代理端口无效")
			}
		}
	}
	return nil
}

type proxyRouter struct {
	mu             sync.RWMutex
	mode           string
	manual         *url.URL
	system         nativeSystemProxy
	systemRead     time.Time
	systemOverride bool
}

// proxy 根据资源设置为当前请求选择代理。
func (router *proxyRouter) proxy(request *http.Request) (*url.URL, error) {
	if request.Context().Value(directProxyAttemptKey{}) == true {
		return nil, nil
	}
	if request.URL.Hostname() == "127.0.0.1" || request.URL.Hostname() == "localhost" || request.URL.Hostname() == "::1" {
		return nil, nil
	}
	router.mu.RLock()
	mode, manual := router.mode, router.manual
	router.mu.RUnlock()
	switch mode {
	case "direct":
		return nil, nil
	case "manual":
		return manual, nil
	}
	return router.systemSettings().resolve(request)
}

// loadResourceSettings 加载资源设置。
func (engine *nativeEngine) loadResourceSettings() {
	settings := defaultResourceSettings()
	file := filepath.Join(engine.directory, "resource-settings.json")
	info, err := os.Stat(file)
	if err == nil {
		var stored nativeResourceSettings
		if info.Mode().IsRegular() && info.Size() <= 16384 {
			data, readErr := os.ReadFile(file)
			decodeErr := json.Unmarshal(data, &stored)
			if readErr == nil && (decodeErr != nil || stored.CacheLimits.validate() != nil) {
				var fields map[string]json.RawMessage
				if json.Unmarshal(data, &fields) == nil && fields["cacheLimits"] != nil {
					delete(fields, "cacheLimits")
					legacy, _ := json.Marshal(fields)
					decodeErr = json.Unmarshal(legacy, &stored)
					stored.CacheLimits = defaultNativeCacheLimits()
					stored.Warning = "缓存设置无效，缓存上限使用默认值；原代理和下载设置保留，请检查后重新保存"
				}
			}
			if decodeErr == nil {
				stored.LogDirectory, decodeErr = nativePortableSelectedPath(engine.directory, stored.LogDirectory, "logs")
			}
			if readErr == nil && decodeErr == nil && stored.validate() == nil {
				settings = stored
			} else {
				settings.Warning = "网络与下载设置无法读取，已使用默认值，请重新保存设置"
			}
		} else {
			settings.Warning = "网络与下载设置无效，已使用默认值，请重新保存设置"
		}
	} else if !os.IsNotExist(err) {
		settings.Warning = "网络与下载设置无法读取，已使用默认值，请重新保存设置"
	}
	if settings.CacheLimits == nil {
		settings.CacheLimits = defaultNativeCacheLimits()
	}
	settings.CacheUsage = nil
	engine.settings = settings
	engine.applyResourceSettings(settings)
	if err := nativeEnsureLogDirectory(engine.effectiveLogDirectory(settings.LogDirectory)); err != nil {
		engine.settings.Warning = strings.TrimSpace(settings.Warning + " 日志目录不可写，请在网络与资源中选择可写目录；不会改写为其他默认位置")
	}
}

// effectiveLogDirectory 按用户设置与便携目录规则选择日志目录。
func (engine *nativeEngine) effectiveLogDirectory(selected string) string {
	if selected != "" {
		return filepath.Clean(selected)
	}
	if engine.defaultLogDirectory != "" {
		return engine.defaultLogDirectory
	}
	return filepath.Join(engine.directory, "logs")
}

// nativeEnsureLogDirectory 准备原生日志目录。
func nativeEnsureLogDirectory(directory string) error {
	if !filepath.IsAbs(directory) {
		return errors.New("日志目录必须是绝对路径")
	}
	if err := os.MkdirAll(directory, 0700); err != nil {
		return errors.New("无法创建日志目录，请选择可写位置，不需要管理员权限")
	}
	file, err := os.CreateTemp(directory, ".log-writecheck-*")
	if err != nil {
		return errors.New("日志目录不可写，请选择其他位置，不需要管理员权限")
	}
	name := file.Name()
	closeErr := file.Close()
	removeErr := os.Remove(name)
	return errors.Join(closeErr, removeErr)
}

// resourceSettings 返回资源设置。
func (engine *nativeEngine) resourceSettings() nativeResourceSettings {
	engine.settingsMu.Lock()
	settings := engine.settings
	engine.settingsMu.Unlock()
	if settings.ProxyMode == "" {
		settings = defaultResourceSettings()
	}
	settings.CacheLimits = settings.CacheLimits.clone()
	settings.CacheUsage = engine.cacheUsage()
	settings.DefaultLogDirectory = engine.effectiveLogDirectory("")
	settings.LogPath = filepath.Join(engine.effectiveLogDirectory(settings.LogDirectory), "app.log")
	system := engine.downloader.proxyRouter.systemSettings()
	settings.SystemProxyStatus = "自动读取系统静态代理及环境变量"
	if system.PAC && system.HTTP == "" && system.HTTPS == "" {
		settings.SystemProxyStatus = "检测到系统自动代理脚本；请配置手动代理或选择直连"
	} else if system.HTTP != "" || system.HTTPS != "" {
		settings.SystemProxyStatus = "已读取系统静态代理"
	}
	return settings
}

// applyResourceSettings 应用资源设置。
func (engine *nativeEngine) applyResourceSettings(settings nativeResourceSettings) {
	if engine.downloader.diagnostics != nil {
		engine.downloader.diagnostics.setDirectory(engine.effectiveLogDirectory(settings.LogDirectory))
	}
	limits := settings.CacheLimits.clone()
	engine.downloader.cachePolicy.Store(&limits)
	router := engine.downloader.proxyRouter
	manual, _ := url.Parse(settings.ProxyURL)
	router.mu.Lock()
	router.mode, router.manual = settings.ProxyMode, manual
	router.mu.Unlock()
	engine.downloader.hongguoMediaCache.clear()
	limiter := engine.downloader.limiter
	limiter.mu.Lock()
	limiter.concurrency = settings.CatalogConcurrency
	limiter.interval = time.Duration(settings.CatalogIntervalMS) * time.Millisecond
	limiter.notifyLocked()
	limiter.mu.Unlock()
	if engine.downloads != nil {
		engine.downloads.mu.Lock()
		engine.downloads.concurrency = settings.DownloadConcurrency
		engine.downloads.sourceConcurrency = settings.DownloadSourceConcurrency
		engine.downloads.bySource = settings.DownloadBySource
		engine.downloads.scheduleLocked()
		engine.downloads.mu.Unlock()
	}
	if engine.covers != nil {
		client := engine.downloader.hongguoClient()
		client.mu.Lock()
		for len(client.suggestions) > engine.downloader.cacheLimit("suggestionEntries") {
			oldestKey := ""
			oldest := time.Time{}
			for key, entry := range client.suggestions {
				if oldestKey == "" || entry.ExpiresAt.Before(oldest) {
					oldestKey, oldest = key, entry.ExpiresAt
				}
			}
			delete(client.suggestions, oldestKey)
		}
		client.mu.Unlock()
		engine.covers.mu.Lock()
		engine.covers.limit = int64(engine.downloader.cacheBytes("coverMiB"))
		engine.covers.entryLimit = engine.downloader.cacheLimit("coverEntries")
		engine.covers.prune("")
		engine.covers.mu.Unlock()
		engine.downloader.rankings.mu.Lock()
		engine.downloader.writeRankingCacheLocked()
		engine.downloader.rankings.mu.Unlock()
	}
}

// saveResourceSettings 保存资源设置。
func (engine *nativeEngine) saveResourceSettings(settings nativeResourceSettings) (nativeResourceSettings, error) {
	settings.ProxyURL = strings.TrimSpace(settings.ProxyURL)
	settings.LogDirectory = strings.TrimSpace(settings.LogDirectory)
	var pathErr error
	settings.LogDirectory, pathErr = nativePortableSelectedPath(engine.directory, settings.LogDirectory, "logs")
	if pathErr != nil {
		return nativeResourceSettings{}, pathErr
	}
	settings.LogPath, settings.DefaultLogDirectory = "", ""
	settings.Warning = ""
	settings.SystemProxyStatus = ""
	settings.CacheUsage = nil
	if err := settings.validate(); err != nil {
		return nativeResourceSettings{}, err
	}
	if err := nativeEnsureLogDirectory(engine.effectiveLogDirectory(settings.LogDirectory)); err != nil {
		return nativeResourceSettings{}, err
	}
	engine.settingsMu.Lock()
	defer engine.settingsMu.Unlock()
	if settings.CacheLimits == nil {
		settings.CacheLimits = engine.settings.CacheLimits.clone()
		if len(settings.CacheLimits) == 0 {
			settings.CacheLimits = defaultNativeCacheLimits()
		}
	}
	settings.CacheLimits = settings.CacheLimits.clone()
	engine.mu.Lock()
	if err := engine.validateCacheReduction(settings.CacheLimits); err != nil {
		engine.mu.Unlock()
		return nativeResourceSettings{}, err
	}
	stored := settings
	if root := nativePortableRoot(engine.directory); root != "" && stored.LogDirectory != "" {
		stored.LogDirectory, _ = filepath.Rel(root, stored.LogDirectory)
		stored.LogDirectory = filepath.ToSlash(stored.LogDirectory)
	}
	data, err := json.Marshal(stored)
	if err == nil {
		var existing, updated map[string]json.RawMessage
		old, readErr := os.ReadFile(filepath.Join(engine.directory, "resource-settings.json"))
		if readErr == nil && len(old) <= 16384 && json.Unmarshal(old, &existing) == nil && json.Unmarshal(data, &updated) == nil {
			for key, value := range updated {
				existing[key] = value
			}
			delete(existing, "cacheUsage")
			delete(existing, "warning")
			delete(existing, "systemProxyStatus")
			delete(existing, "logPath")
			delete(existing, "defaultLogDirectory")
			data, err = json.Marshal(existing)
		}
	}
	if err == nil && len(data) > 16384 {
		err = errors.New("网络与资源配置过大")
	}
	if err == nil {
		err = nativeDownloadWrite(filepath.Join(engine.directory, "resource-settings.json"), data)
	}
	if err != nil {
		engine.mu.Unlock()
		return nativeResourceSettings{}, errors.New("未能保存网络与下载设置，原设置保持不变")
	}
	engine.settings = settings
	limits := settings.CacheLimits.clone()
	engine.downloader.cachePolicy.Store(&limits)
	engine.mu.Unlock()
	engine.applyResourceSettings(settings)
	engine.downloader.client.CloseIdleConnections()
	settings.DefaultLogDirectory = engine.effectiveLogDirectory("")
	settings.LogPath = filepath.Join(engine.effectiveLogDirectory(settings.LogDirectory), "app.log")
	return settings, nil
}
