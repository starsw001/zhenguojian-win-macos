package core

import (
	"errors"
	"runtime"
	"unsafe"

	"golang.org/x/sys/windows"
)

const sourceSessionPersistent = true

func sourceSessionProtect(value []byte, protect bool) ([]byte, error) {
	if len(value) == 0 || len(value) > 128<<10 {
		return nil, errors.New("站源登录状态大小无效")
	}
	input := windows.DataBlob{Size: uint32(len(value)), Data: &value[0]}
	var output windows.DataBlob
	var err error
	if protect {
		err = windows.CryptProtectData(&input, nil, nil, 0, nil, windows.CRYPTPROTECT_UI_FORBIDDEN, &output)
	} else {
		err = windows.CryptUnprotectData(&input, nil, nil, 0, nil, windows.CRYPTPROTECT_UI_FORBIDDEN, &output)
	}
	runtime.KeepAlive(value)
	if err != nil {
		return nil, errors.New("无法在当前 Windows 用户和电脑上读取或保存站源登录状态，请重新登录")
	}
	defer windows.LocalFree(windows.Handle(unsafe.Pointer(output.Data)))
	return append([]byte(nil), unsafe.Slice(output.Data, output.Size)...), nil
}
