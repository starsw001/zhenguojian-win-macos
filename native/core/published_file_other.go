//go:build !windows

package core

import "os"

// nativeOpenPublishedFile 打开已发布的本地成品文件。
func nativeOpenPublishedFile(path string) (*os.File, error) {
	return os.Open(path)
}
