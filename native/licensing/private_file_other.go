//go:build !windows

package licensing

import "os"

// RestrictPrivateFile 限制本地私有授权文件的访问权限。
func RestrictPrivateFile(path string) error { return os.Chmod(path, 0600) }
