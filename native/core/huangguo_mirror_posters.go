package core

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// checkHuangguoPoster 检查黄果海报。
func (engine *nativeEngine) checkHuangguoPoster(ctx context.Context, drama Drama, origin string, decode func(context.Context, string) (huangguoDecodeResult, error)) (huangguoPosterResult, error) {
	cfg := engine.downloader.cfg
	cfg.HuangguoAIURL, cfg.HuangguoVideoURL = origin, origin
	d := &Downloader{cfg: cfg, client: engine.downloader.client, providerHosts: map[string]string{}, diagnostics: engine.downloader.diagnostics}
	directory := filepath.Join(engine.directory, "mirror-checks")
	cache := newNativeCoverCache(directory, d)
	path, err := cache.loadAddress(ctx, nativeNormalize(drama), true)
	if err != nil {
		return huangguoPosterResult{}, err
	}
	info, err := os.Stat(path)
	if err != nil || info.Size() <= 0 {
		return huangguoPosterResult{}, errors.New("海报缓存为空或不可读")
	}
	coverEngine := &nativeEngine{downloader: d, directory: directory, covers: cache}
	prepared, err := coverEngine.prepareCover(ctx, nativeNormalize(drama))
	if err != nil {
		return huangguoPosterResult{}, err
	}
	values, ok := prepared.(map[string]any)
	if !ok {
		return huangguoPosterResult{}, errors.New("海报准备结果无效")
	}
	input, _ := values["input"].(string)
	if input == "" {
		input = path
	}
	if input != path && filepath.Dir(input) == cache.directory && strings.HasPrefix(filepath.Base(input), ".decode-") {
		defer os.Remove(input)
	}
	decoded, err := decode(ctx, input)
	if err != nil {
		return huangguoPosterResult{}, err
	}
	if decoded.Width <= 0 || decoded.Height <= 0 {
		return huangguoPosterResult{}, errors.New("海报未返回真实解码尺寸")
	}
	return huangguoPosterResult{Width: decoded.Width, Height: decoded.Height, Bytes: info.Size(), Message: fmt.Sprintf("真实海报解码%d×%d，%d字节", decoded.Width, decoded.Height, info.Size())}, nil
}
