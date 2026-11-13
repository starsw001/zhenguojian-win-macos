package licensing

import (
	"golang.org/x/sys/windows"
	"os"
)

// lockLicenseState 取得本地授权状态的跨进程锁，并返回释放函数。
func lockLicenseState(path string) (func(), error) {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	if err = RestrictPrivateFile(path); err != nil {
		file.Close()
		return nil, err
	}
	var overlapped windows.Overlapped
	handle := windows.Handle(file.Fd())
	if err = windows.LockFileEx(handle, windows.LOCKFILE_EXCLUSIVE_LOCK, 0, 1, 0, &overlapped); err != nil {
		file.Close()
		return nil, err
	}
	return func() { windows.UnlockFileEx(handle, 0, 1, 0, &overlapped); file.Close() }, nil
}
