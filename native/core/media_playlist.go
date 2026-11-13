package core

import (
	"math"

	"strconv"
	"strings"
	"time"
)

// m3u8Duration 根据 M3U8 媒体段信息计算播放时长。
func m3u8Duration(raw string) time.Duration {
	var total time.Duration
	for _, line := range strings.Split(raw, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "#EXTINF:") {
			continue
		}
		value := strings.TrimSpace(strings.TrimPrefix(line, "#EXTINF:"))
		if comma := strings.IndexByte(value, ','); comma >= 0 {
			value = value[:comma]
		}
		seconds, err := strconv.ParseFloat(strings.TrimSpace(value), 64)
		if err != nil || seconds < 0 || math.IsNaN(seconds) || math.IsInf(seconds, 0) {
			continue
		}
		total += time.Duration(seconds * float64(time.Second))
	}
	return total
}
