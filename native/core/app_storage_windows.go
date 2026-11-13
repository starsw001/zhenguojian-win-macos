package core

import "golang.org/x/sys/windows"

// nativeFreeSpace 查询目标路径所在存储卷的可用空间。
func nativeFreeSpace(path string) int64 {
	value, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return -1
	}
	var available, total, free uint64
	if windows.GetDiskFreeSpaceEx(value, &available, &total, &free) != nil {
		return -1
	}
	return int64(available)
}
