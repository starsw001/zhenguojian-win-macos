package secureupdate

import "golang.org/x/sys/windows"

// replaceFile 替换文件。
func replaceFile(a, b string) error {
	x, e := windows.UTF16PtrFromString(a)
	if e != nil {
		return e
	}
	y, e := windows.UTF16PtrFromString(b)
	if e != nil {
		return e
	}
	return windows.MoveFileEx(x, y, windows.MOVEFILE_REPLACE_EXISTING|windows.MOVEFILE_WRITE_THROUGH)
}
