//go:build !windows

package core

// platformSystemProxy 返回当前平台的空系统代理配置。
func platformSystemProxy() nativeSystemProxy { return nativeSystemProxy{} }
