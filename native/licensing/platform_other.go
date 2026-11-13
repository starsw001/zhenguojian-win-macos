//go:build !windows

package licensing

import "errors"

// machineHash 返回当前非 Windows 平台尚不支持该授权模块的错误。
func machineHash() (string, error) { return "", errors.New("此授权模块目前仅用于 Windows") }

// platformProtect 返回当前平台尚未配置安全授权存储的错误。
func platformProtect([]byte) ([]byte, error) {
	return nil, errors.New("此平台未配置安全授权存储")
}

// platformUnprotect 返回当前平台尚未配置安全授权存储的错误。
func platformUnprotect([]byte) ([]byte, error) {
	return nil, errors.New("此平台未配置安全授权存储")
}
