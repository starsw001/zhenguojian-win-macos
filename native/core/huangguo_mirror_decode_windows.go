package core

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// decodeHuangguoMirrorPlayback 解码黄果镜像播放。
func decodeHuangguoMirrorPlayback(ctx context.Context, address string) (huangguoDecodeResult, error) {
	executable, err := os.Executable()
	if err != nil {
		return huangguoDecodeResult{}, err
	}
	return decodeHuangguoMirrorPlaybackWithLibrary(ctx, address, filepath.Join(filepath.Dir(executable), "libmpv-2.dll"))
}

// decodeHuangguoMirrorPlaybackWithLibrary 解码黄果镜像播放包含媒体库。
func decodeHuangguoMirrorPlaybackWithLibrary(parent context.Context, address, path string) (huangguoDecodeResult, error) {
	return decodeHuangguoMirrorAssetWithLibrary(parent, address, path, false)
}

// decodeHuangguoMirrorPoster 解码黄果镜像海报。
func decodeHuangguoMirrorPoster(ctx context.Context, address string) (huangguoDecodeResult, error) {
	executable, err := os.Executable()
	if err != nil {
		return huangguoDecodeResult{}, err
	}
	return decodeHuangguoMirrorAssetWithLibrary(ctx, address, filepath.Join(filepath.Dir(executable), "libmpv-2.dll"), true)
}

// decodeHuangguoMirrorAssetWithLibrary 解码黄果镜像资源包含媒体库。
func decodeHuangguoMirrorAssetWithLibrary(parent context.Context, address, path string, poster bool) (huangguoDecodeResult, error) {
	if _, err := os.Stat(path); err != nil {
		return huangguoDecodeResult{}, errors.New("运行目录缺少随包libmpv-2.dll；未安装替代环境，地址保持未启用")
	}
	library, err := windows.LoadDLL(path)
	if err != nil {
		return huangguoDecodeResult{}, errors.New("无法加载随包视频解码器")
	}
	defer library.Release()
	functions := map[string]*windows.Proc{}
	for _, name := range []string{"mpv_create", "mpv_initialize", "mpv_set_option_string", "mpv_command", "mpv_get_property", "mpv_terminate_destroy"} {
		function, err := library.FindProc(name)
		if err != nil {
			return huangguoDecodeResult{}, err
		}
		functions[name] = function
	}
	handle, _, _ := functions["mpv_create"].Call()
	if handle == 0 {
		return huangguoDecodeResult{}, errors.New("无法创建隔离视频解码器")
	}
	defer functions["mpv_terminate_destroy"].Call(handle)
	option := func(name, value string) error {
		key, _ := windows.BytePtrFromString(name)
		text, _ := windows.BytePtrFromString(value)
		code, _, _ := functions["mpv_set_option_string"].Call(handle, uintptr(unsafe.Pointer(key)), uintptr(unsafe.Pointer(text)))
		runtime.KeepAlive(key)
		runtime.KeepAlive(text)
		if int32(code) < 0 {
			return fmt.Errorf("解码器选项%s失败：%d", name, int32(code))
		}
		return nil
	}
	for _, pair := range [][2]string{{"config", "no"}, {"load-scripts", "no"}, {"vo", "null"}, {"audio", "no"}, {"hwdec", "no"}, {"terminal", "no"}, {"idle", "yes"}, {"cache", "no"}, {"network-timeout", "6"}, {"demuxer-max-bytes", "4194304"}, {"demuxer-readahead-secs", "1"}} {
		if err := option(pair[0], pair[1]); err != nil {
			return huangguoDecodeResult{}, err
		}
	}
	if poster {
		if err := option("image-display-duration", "2"); err != nil {
			return huangguoDecodeResult{}, err
		}
	}
	code, _, _ := functions["mpv_initialize"].Call(handle)
	if int32(code) < 0 {
		return huangguoDecodeResult{}, errors.New("视频解码器初始化失败")
	}
	load, _ := windows.BytePtrFromString("loadfile")
	input, _ := windows.BytePtrFromString(address)
	args := []*byte{load, input, nil}
	code, _, _ = functions["mpv_command"].Call(handle, uintptr(unsafe.Pointer(&args[0])))
	runtime.KeepAlive(args)
	if int32(code) < 0 {
		return huangguoDecodeResult{}, errors.New("视频解码器未接受播放地址")
	}
	getInt := func(name string) int64 {
		var value int64
		key, _ := windows.BytePtrFromString(name)
		code, _, _ := functions["mpv_get_property"].Call(handle, uintptr(unsafe.Pointer(key)), 4, uintptr(unsafe.Pointer(&value)))
		runtime.KeepAlive(key)
		if int32(code) < 0 {
			return 0
		}
		return value
	}
	getDouble := func(name string) float64 {
		var value float64
		key, _ := windows.BytePtrFromString(name)
		code, _, _ := functions["mpv_get_property"].Call(handle, uintptr(unsafe.Pointer(key)), 5, uintptr(unsafe.Pointer(&value)))
		runtime.KeepAlive(key)
		if int32(code) < 0 {
			return 0
		}
		return value
	}
	ctx, cancel := context.WithTimeout(parent, 15*time.Second)
	defer cancel()
	first := -1.0
	for ctx.Err() == nil {
		position := getDouble("time-pos")
		width, height := getInt("video-out-params/w"), getInt("video-out-params/h")
		if poster && width > 0 && height > 0 {
			return huangguoDecodeResult{Width: int(width), Height: int(height)}, nil
		}
		if width > 0 && height > 0 && getDouble("estimated-vf-fps") > 0 {
			if first < 0 {
				first = position
			}
			if position-first >= .4 {
				return huangguoDecodeResult{Width: int(width), Height: int(height), PositionMS: int64((position - first) * 1000)}, nil
			}
		}
		select {
		case <-ctx.Done():
		case <-time.After(50 * time.Millisecond):
		}
	}
	return huangguoDecodeResult{}, errors.New("真实解码未在时限内返回视频帧和前进进度；未作为播放通过")
}
