//go:build !windows

package core

import (
	"context"
	"errors"
)

// decodeHuangguoMirrorPlayback 返回当前平台尚未接入原生播放解码确认的错误。
func decodeHuangguoMirrorPlayback(context.Context, string) (huangguoDecodeResult, error) {
	return huangguoDecodeResult{}, errors.New("当前平台尚未接入原生自动实播确认；新地址保留待验证，不冒充播放通过")
}

// decodeHuangguoMirrorPoster 返回当前平台尚未接入原生海报解码确认的错误。
func decodeHuangguoMirrorPoster(context.Context, string) (huangguoDecodeResult, error) {
	return huangguoDecodeResult{}, errors.New("当前平台尚未接入原生海报实解码确认，新地址不自动启用")
}
