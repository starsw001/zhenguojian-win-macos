//go:build !windows

package core

import "errors"

const sourceSessionPersistent = false

func sourceSessionProtect([]byte, bool) ([]byte, error) {
	return nil, errors.New("当前平台的青空登录状态仅保留在本次运行内存中")
}
