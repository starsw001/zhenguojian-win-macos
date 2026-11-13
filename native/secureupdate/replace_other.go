//go:build !windows

package secureupdate

import "os"

// replaceFile 替换文件。
func replaceFile(a, b string) error { return os.Rename(a, b) }
