package licensing

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"runtime"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

// machineHash 生成用于授权设备绑定的本机摘要。
func machineHash() (string, error) {
	key, err := registry.OpenKey(registry.LOCAL_MACHINE, `SOFTWARE\Microsoft\Cryptography`, registry.QUERY_VALUE|registry.WOW64_64KEY)
	if err != nil {
		return "", errors.New("无法读取本机授权标识")
	}
	defer key.Close()
	value, _, err := key.GetStringValue("MachineGuid")
	if err != nil || strings.TrimSpace(value) == "" {
		return "", errors.New("本机授权标识无效")
	}
	firmware := windows.NewLazySystemDLL("kernel32.dll").NewProc("GetSystemFirmwareTable")
	size, _, _ := firmware.Call(0x52534d42, 0, 0, 0)
	if size < 8 || size > 1<<20 {
		return "", errors.New("无法读取本机硬件授权标识，请联系管理员")
	}
	buffer := make([]byte, size)
	written, _, _ := firmware.Call(0x52534d42, 0, uintptr(unsafe.Pointer(&buffer[0])), size)
	if written != size {
		return "", errors.New("本机硬件授权标识读取不完整")
	}
	uuid, err := smbiosUUID(buffer)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256([]byte("guoapp-license-v1\x00" + strings.ToLower(strings.TrimSpace(value)) + "\x00" + hex.EncodeToString(uuid)))
	return hex.EncodeToString(digest[:]), nil
}

// platformProtect 使用当前平台的数据保护机制封装授权数据。
func platformProtect(value []byte) ([]byte, error) {
	return dpapi(value, true)
}

// platformUnprotect 使用当前平台的数据保护机制解封授权数据。
func platformUnprotect(value []byte) ([]byte, error) {
	return dpapi(value, false)
}

// dpapi 调用 Windows DPAPI 保护或解封当前用户的授权数据。
func dpapi(value []byte, protect bool) ([]byte, error) {
	if len(value) == 0 || len(value) > 512<<10 {
		return nil, errors.New("本地授权数据大小无效")
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
		return nil, errors.New("本地授权数据无法在当前 Windows 用户和电脑上解密")
	}
	defer windows.LocalFree(windows.Handle(unsafe.Pointer(output.Data)))
	return append([]byte(nil), unsafe.Slice(output.Data, output.Size)...), nil
}
