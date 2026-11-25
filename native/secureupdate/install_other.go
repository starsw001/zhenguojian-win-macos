//go:build !windows

package secureupdate

import "errors"

func (i Install) StartInstaller(slot string) error {
	return errors.New("当前平台不支持原位更新")
}
