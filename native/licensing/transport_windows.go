package licensing

import (
	"errors"
	"net/http"
	"net/url"
	"runtime"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

var sysWinHTTP = windows.NewLazySystemDLL("winhttp.dll")
var sysIEConfig = sysWinHTTP.NewProc("WinHttpGetIEProxyConfigForCurrentUser")
var sysWinOpen = sysWinHTTP.NewProc("WinHttpOpen")
var sysAutoProxy = sysWinHTTP.NewProc("WinHttpGetProxyForUrl")
var sysWinClose = sysWinHTTP.NewProc("WinHttpCloseHandle")
var sysWinTimeout = sysWinHTTP.NewProc("WinHttpSetTimeouts")
var sysGlobalFree = windows.NewLazySystemDLL("kernel32.dll").NewProc("GlobalFree")
var proxyLookupSlots = make(chan struct{}, 2)

type userProxyConfig struct {
	Detect             uint32
	PAC, Proxy, Bypass *uint16
}
type autoProxyOptions struct {
	Flags, DetectFlags   uint32
	ConfigURL, Reserved  *uint16
	ReservedN, AutoLogon uint32
}
type autoProxyInfo struct {
	Access        uint32
	Proxy, Bypass *uint16
}

// copyGlobalString 复制 Windows 全局内存中的字符串并释放相应资源。
func copyGlobalString(p *uint16) string {
	if p == nil {
		return ""
	}
	s := windows.UTF16PtrToString(p)
	sysGlobalFree.Call(uintptr(unsafe.Pointer(p)))
	return s
}

// platformLicenseProxy 读取当前平台可用的许可证代理配置。
func platformLicenseProxy(req *http.Request) (*url.URL, error) {
	var cfg userProxyConfig
	ok, _, e := sysIEConfig.Call(uintptr(unsafe.Pointer(&cfg)))
	if ok == 0 {
		if errors.Is(e, syscall.ERROR_FILE_NOT_FOUND) {
			return nil, nil
		}
		return nil, errLicenseProxy
	}
	pac, manual, bypass := copyGlobalString(cfg.PAC), copyGlobalString(cfg.Proxy), copyGlobalString(cfg.Bypass)
	if pac == "" && cfg.Detect == 0 {
		return windowsProxyForURL(req.URL, manual, bypass)
	}
	type result struct {
		u *url.URL
		e error
	}
	resultCh := make(chan result, 1)
	select {
	case proxyLookupSlots <- struct{}{}:
	case <-req.Context().Done():
		return nil, req.Context().Err()
	}
	go func() {
		defer func() { <-proxyLookupSlots }()
		u, err := autoLicenseProxy(req.URL, pac, manual, bypass)
		resultCh <- result{u, err}
	}()
	select {
	case r := <-resultCh:
		return r.u, r.e
	case <-req.Context().Done():
		return nil, req.Context().Err()
	}
}

// autoLicenseProxy 为目标授权地址解析 Windows 自动代理。
func autoLicenseProxy(target *url.URL, pac, manual, bypass string) (*url.URL, error) {
	agent, _ := windows.UTF16PtrFromString("GuoApp License")
	h, _, _ := sysWinOpen.Call(uintptr(unsafe.Pointer(agent)), 1, 0, 0, 0)
	if h == 0 {
		return nil, errLicenseProxy
	}
	defer sysWinClose.Call(h)
	sysWinTimeout.Call(h, 3000, 3000, 3000, 3000)
	var opt autoProxyOptions
	if pac != "" {
		u, e := url.Parse(pac)
		if e != nil || u.Host == "" || u.User != nil || (u.Scheme != "http" && u.Scheme != "https") {
			return nil, errLicenseProxy
		}
		opt.Flags = 2
		opt.ConfigURL, e = windows.UTF16PtrFromString(pac)
		if e != nil {
			return nil, errLicenseProxy
		}
	} else {
		opt.Flags = 1
		opt.DetectFlags = 3
	}
	address, e := windows.UTF16PtrFromString(target.String())
	if e != nil {
		return nil, errLicenseProxy
	}
	var got autoProxyInfo
	ok, _, cause := sysAutoProxy.Call(h, uintptr(unsafe.Pointer(address)), uintptr(unsafe.Pointer(&opt)), uintptr(unsafe.Pointer(&got)))
	runtime.KeepAlive(opt.ConfigURL)
	if ok == 0 {
		if pac == "" && errors.Is(cause, syscall.Errno(12180)) || manual != "" {
			return windowsProxyForURL(target, manual, bypass)
		}
		return nil, errLicenseProxy
	}
	out, exceptions := copyGlobalString(got.Proxy), copyGlobalString(got.Bypass)
	if got.Access == 1 {
		return nil, nil
	}
	if got.Access != 3 || out == "" {
		return nil, errLicenseProxy
	}
	return windowsProxyForURL(target, out, exceptions)
}
