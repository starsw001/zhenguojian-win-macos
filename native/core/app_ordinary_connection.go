package core

import (
	"context"
	"encoding/base64"
	"errors"
	"strings"
	"time"
)

// checkOrdinaryEpisodeConnection 检查普通站源分集连接。
func (engine *nativeEngine) checkOrdinaryEpisodeConnection(ctx context.Context, input nativeInput) (map[string]any, error) {
	source, _, valid := splitProviderDramaID(input.Drama.ID)
	if !valid || !ordinarySource(source) || !nativeDramaAvailable(input.Drama) || input.Chapter.VIP {
		return nil, errors.New("仅支持已授权普通影视源的免费分集连接检查")
	}
	if err := validateOrdinaryDetail(input.Drama, Drama{}, "", []Chapter{input.Chapter}); err != nil {
		return nil, err
	}
	ctx = withFreshOrdinaryDetail(ctx, source)
	ctx = context.WithValue(ctx, sourceConnectivityOnlyKey{}, true)
	start := time.Now()
	media, err := engine.downloader.resolveProviderMedia(ctx, Task{DramaID: input.Drama.ID, DramaTitle: input.Drama.Title, Chapter: input.Chapter, Index: input.Index})
	if err != nil {
		return nil, err
	}
	ctx = providerMediaContext(ctx, media.credentials)
	key, address, err := engine.downloader.sourceProbeResources(ctx, media)
	if err != nil {
		return nil, err
	}
	if key != "" && len(media.CENCKey) != 16 && len(media.HLSKey) != 16 {
		var body []byte
		if strings.HasPrefix(key, "data:") {
			_, encoded, _ := strings.Cut(key, ",")
			body, err = base64.StdEncoding.DecodeString(encoded)
		} else {
			body, err = engine.downloader.sourceProbeBytes(ctx, key, media.Referer, true)
		}
		if err != nil {
			return nil, err
		}
		if len(body) != 16 {
			return nil, errors.New("媒体密钥长度无效，未尝试播放或解码")
		}
	}
	body, err := engine.downloader.sourceProbeBytes(ctx, address, media.Referer, false)
	if err != nil {
		return nil, err
	}
	if len(body) == 0 {
		return nil, errors.New("媒体连接返回空内容")
	}
	return map[string]any{"connected": true, "actualPlayback": false, "bytesRead": len(body), "elapsedMs": time.Since(start).Milliseconds()}, nil
}
