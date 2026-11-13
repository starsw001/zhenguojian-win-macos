package core

import (
	"duanjuapp/native/licensing"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
)

var buildLicenseRequired = "false"
var buildAppVersion = "1.1.1+132"
var buildLicenseDistributionKey = ""
var buildLicenseServerKey = ""
var buildLicenseActivatePath = ""
var buildLicenseVerifyPath = ""
var buildLicenseEventPath = ""
var errNativeLicense = errors.New("请先完成专属登录和本机许可证激活，或联网复核授权")

var nativeLicense struct {
	once    sync.Once
	manager *licensing.Manager
}

// nativeLicenseEnabled 判断原生许可证Enabled是否满足当前规则。
func nativeLicenseEnabled() bool { return runtime.GOOS == "windows" && buildLicenseRequired == "true" }

// nativeLicenseManager 返回原生许可证管理器。
func nativeLicenseManager() *licensing.Manager {
	nativeLicense.once.Do(func() {
		edition := "hongguoshang"
		if buildAllSources == "true" {
			edition = "zhenguoshang"
		}
		executable, _ := os.Executable()
		root := filepath.Dir(executable)
		nativeLicense.manager = licensing.Open(licensing.Config{
			Root: root, Storage: filepath.Join(root, "app-data", "licenses", edition),
			Edition: edition, Version: buildAppVersion, ServerURL: licensing.ServiceOrigin,
			DistributionPublicKey: buildLicenseDistributionKey, ServerPublicKey: buildLicenseServerKey,
			ActivatePath: buildLicenseActivatePath, VerifyPath: buildLicenseVerifyPath, EventPath: buildLicenseEventPath,
			OnLock: nativeStopLicensedWork,
		})
		go func(manager *licensing.Manager) {
			for range time.NewTicker(time.Minute).C {
				manager.Poll(true)
			}
		}(nativeLicense.manager)
	})
	return nativeLicense.manager
}

// nativeStopLicensedWork 停止原生Licensed作品。
func nativeStopLicensedWork() {
	nativeState.Lock()
	engine := nativeState.engine
	nativeState.Unlock()
	if engine == nil {
		return
	}
	engine.playbackMu.Lock()
	if engine.playbackCancel != nil {
		engine.playbackCancel()
	}
	engine.playbackMu.Unlock()
	engine.readMu.Lock()
	for _, request := range engine.reads {
		if request.cancel != nil {
			request.cancel()
		}
	}
	engine.readMu.Unlock()
	engine.mu.Lock()
	for _, task := range engine.sourceTasks {
		if task.cancel != nil {
			task.cancel()
		}
	}
	if engine.startupAICancel != nil {
		engine.startupAICancel()
	}
	engine.mu.Unlock()
	if engine.stream != nil {
		engine.stream.mu.Lock()
		for id, session := range engine.stream.sessions {
			session.cancel()
			delete(engine.stream.sessions, id)
		}
		engine.stream.mu.Unlock()
	}
	if engine.downloads != nil {
		_ = engine.downloads.control("", "pauseAll")
	}
	engine.lanMu.Lock()
	if engine.lan != nil {
		engine.lan.close()
		engine.lan = nil
	}
	engine.lanMu.Unlock()
}

// nativeLicenseAction 返回原生许可证Action。
func nativeLicenseAction(input nativeInput) (any, error) {
	if !nativeLicenseEnabled() {
		return map[string]any{"phase": "configuration", "message": "此开发构建未启用分发授权，不可作为加密成品分发", "ready": false}, nil
	}
	if input.Action == "licenseNetworkCheck" {
		if err := licensing.PublicNetworkCheck(buildAppVersion, buildLicenseDistributionKey, buildLicenseServerKey); err != nil {
			return nil, err
		}
		return map[string]any{"reachable": true, "message": "本机可以连接正确的许可证服务，未提交账号、密码或许可证。"}, nil
	}
	manager := nativeLicenseManager()
	switch input.Action {
	case "licenseUpdateReady":
		return updateReady()
	case "licenseStatus":
		return manager.Poll(true), nil
	case "licenseLogin":
		return manager.Login(input.Username, input.Password), nil
	case "licenseActivate":
		return manager.Activate(input.LicenseKey), nil
	case "licenseVerify":
		return manager.Verify(), nil
	}
	return nil, errors.New("授权操作无效")
}

// nativeRequireLicense 返回原生Require许可证。
func nativeRequireLicense(input nativeInput) error {
	if !nativeLicenseEnabled() {
		return nil
	}
	if strings.HasPrefix(input.Action, "license") {
		return nil
	}
	switch input.Action {
	case "cancelPlayback", "cancelRead", "release":
		return nil
	case "controlDownloads":
		if input.Command == "pauseAll" || input.Command == "pause" {
			return nil
		}
	case "lan":
		if input.Command == "stop" || input.Command == "cancel" || input.Command == "disconnect" {
			return nil
		}
	}
	if !nativeLicenseManager().Allowed() {
		go nativeStopLicensedWork()
		return errNativeLicense
	}
	return nil
}
