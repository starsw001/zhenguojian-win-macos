//go:build !windows

package licensing

import (
	"golang.org/x/sys/unix"
	"os"
)

// lockLicenseState 取得本地授权状态的跨进程锁，并返回释放函数。
func lockLicenseState(path string) (func(), error) {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	if err = unix.Flock(int(file.Fd()), unix.LOCK_EX); err != nil {
		file.Close()
		return nil, err
	}
	return func() { unix.Flock(int(file.Fd()), unix.LOCK_UN); file.Close() }, nil
}
