package portable

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
)

// DataDirectory 返回当前版本目录内的数据目录，不查找旧版或系统用户目录。
func DataDirectory(root string) (string, error) {
	if !filepath.IsAbs(root) {
		return "", errors.New("软件目录必须是绝对路径")
	}
	path := filepath.Join(filepath.Clean(root), "app-data")
	return path, NoLinks(path)
}

// NoLinks 拒绝路径及其现有父目录中的链接。
func NoLinks(path string) error {
	for path = filepath.Clean(path); ; path = filepath.Dir(path) {
		info, err := os.Lstat(path)
		if err != nil && !os.IsNotExist(err) {
			return err
		}
		if err == nil && info.Mode()&os.ModeSymlink != 0 {
			return errors.New("软件数据目录不能包含链接")
		}
		if filepath.Dir(path) == path {
			return nil
		}
	}
}

// EnsureDirectory 在拒绝链接后创建数据目录，不回退到共享目录。
func EnsureDirectory(path string) error {
	if err := NoLinks(path); err != nil {
		return err
	}
	if err := os.MkdirAll(path, 0700); err != nil {
		return err
	}
	return NoLinks(path)
}

// MutableDirectory 判断当前版本根目录下的非运行文件目录。
func MutableDirectory(name string) bool {
	return strings.EqualFold(name, "app-data") || strings.EqualFold(name, "download") || strings.EqualFold(name, "logs")
}
