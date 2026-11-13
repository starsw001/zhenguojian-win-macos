package main

/*
#include <stdlib.h>
*/
import "C"
import (
	"duanjuapp/native/core"
	"unsafe"
)

// DuanjuRequest 接收 C 字符串形式的 JSON 请求并返回 C 分配的响应；调用方应使用 DuanjuFree 释放响应。
//
//export DuanjuRequest
func DuanjuRequest(input *C.char) *C.char {
	if input == nil {
		return C.CString(`{"ok":false,"error":"请求为空"}`)
	}
	return C.CString(core.NativeRequest(C.GoString(input)))
}

// DuanjuFree 释放跨语言请求返回的 C 字符串。
//
//export DuanjuFree
func DuanjuFree(value *C.char) { C.free(unsafe.Pointer(value)) }

// main 为 C 共享库提供 Go 入口，不启动客户端窗口。
func main() {}
