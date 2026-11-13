//go:build !windows

package core

import "golang.org/x/sys/unix"

// nativeFreeSpace 查询目标路径所在存储卷的可用空间。
func nativeFreeSpace(path string) int64 {
	var value unix.Statfs_t
	if unix.Statfs(path, &value) != nil {
		return -1
	}
	return int64(value.Bavail) * int64(value.Bsize)
}
