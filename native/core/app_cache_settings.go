package core

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type nativeCacheSpec struct {
	key     string
	minimum int
}

var nativeCacheSpecs = []nativeCacheSpec{
	{"catalogMiB", 128}, {"sourceRecordsMiB", 4}, {"coverMiB", 256}, {"coverEntries", 2000},
	{"rankingMiB", 8}, {"rankingPages", 128}, {"suggestionEntries", 128}, {"inputEntries", 32},
	{"preloadMiB", 4}, {"playerMiB", 32},
}

type nativeCacheLimits map[string]int

// defaultNativeCacheLimits 构造默认原生缓存限额。
func defaultNativeCacheLimits() nativeCacheLimits {
	limits := nativeCacheLimits{}
	for _, spec := range nativeCacheSpecs {
		limits[spec.key] = spec.minimum * 2
	}
	return limits
}

// UnmarshalJSON 将 JSON 数据解码为缓存限额，并处理对应字段转换。
func (limits *nativeCacheLimits) UnmarshalJSON(data []byte) error {
	var values map[string]int
	if err := json.Unmarshal(data, &values); err != nil {
		return err
	}
	merged := defaultNativeCacheLimits()
	for key, value := range values {
		if _, ok := merged[key]; !ok {
			return errors.New("未知的缓存配置项")
		}
		merged[key] = value
	}
	*limits = merged
	return nil
}

// clone 复制缓存限额的当前状态快照。
func (limits nativeCacheLimits) clone() nativeCacheLimits {
	copy := nativeCacheLimits{}
	for key, value := range limits {
		copy[key] = value
	}
	return copy
}

// validate 校验缓存限额的字段与约束是否有效。
func (limits nativeCacheLimits) validate() error {
	if limits == nil {
		return nil
	}
	if len(limits) != len(nativeCacheSpecs) {
		return errors.New("缓存配置项不完整")
	}
	for _, spec := range nativeCacheSpecs {
		value := limits[spec.key]
		if value < spec.minimum || value > spec.minimum*8 {
			return fmt.Errorf("缓存 %s 的范围应为 %d 至 %d", spec.key, spec.minimum, spec.minimum*8)
		}
	}
	return nil
}

// cacheLimit 返回缓存限额。
func (d *Downloader) cacheLimit(key string) int {
	if d != nil {
		if limits := d.cachePolicy.Load(); limits != nil {
			if value := (*limits)[key]; value > 0 {
				return value
			}
		}
	}
	return defaultNativeCacheLimits()[key]
}

// cacheBytes 返回缓存字节。
func (d *Downloader) cacheBytes(key string) int { return d.cacheLimit(key) << 20 }

// cacheBytes 返回缓存字节。
func (engine *nativeEngine) cacheBytes(key string) int {
	return engine.downloader.cacheBytes(key)
}

type nativeCacheUsage struct {
	Value     int64  `json:"value"`
	Available bool   `json:"available"`
	Detail    string `json:"detail,omitempty"`
}

// nativeCacheFileUsage 统计指定缓存文件的大小与条目数量。
func nativeCacheFileUsage(path string) nativeCacheUsage {
	info, err := os.Stat(path)
	if os.IsNotExist(err) {
		return nativeCacheUsage{Available: true}
	}
	if err != nil || !info.Mode().IsRegular() {
		return nativeCacheUsage{Detail: "无法读取文件占用"}
	}
	return nativeCacheUsage{Value: info.Size(), Available: true}
}

// cacheUsage 返回缓存占用。
func (engine *nativeEngine) cacheUsage() map[string]nativeCacheUsage {
	usage := map[string]nativeCacheUsage{}
	for key, name := range map[string]string{"catalogMiB": "catalogs.json", "sourceRecordsMiB": "sources.json", "rankingMiB": "rankings.json"} {
		usage[key] = nativeCacheFileUsage(filepath.Join(engine.directory, name))
	}
	var bytes, entries int64
	files, err := os.ReadDir(filepath.Join(engine.directory, "covers-v1"))
	available := err == nil || os.IsNotExist(err)
	for _, file := range files {
		if file.IsDir() || !strings.HasSuffix(file.Name(), ".img") {
			continue
		}
		info, statErr := file.Info()
		if statErr != nil {
			available = false
			continue
		}
		if info.Mode().IsRegular() {
			bytes += info.Size()
			entries++
		}
	}
	usage["coverMiB"] = nativeCacheUsage{Value: bytes, Available: available}
	usage["coverEntries"] = nativeCacheUsage{Value: entries, Available: available}
	d := engine.downloader
	d.rankings.mu.Lock()
	usage["rankingPages"] = nativeCacheUsage{Value: int64(len(d.rankings.pages)), Available: true}
	d.rankings.mu.Unlock()
	client := d.hongguoClient()
	client.mu.Lock()
	usage["suggestionEntries"] = nativeCacheUsage{Value: int64(len(client.suggestions)), Available: true}
	client.mu.Unlock()
	engine.mu.Lock()
	stream := engine.stream
	engine.mu.Unlock()
	bytes = 0
	if stream != nil {
		stream.mu.Lock()
		sessions := make([]*nativeStreamSession, 0, len(stream.sessions))
		for _, session := range stream.sessions {
			sessions = append(sessions, session)
		}
		stream.mu.Unlock()
		for _, session := range sessions {
			session.mu.Lock()
			for _, asset := range session.assets {
				bytes += int64(len(asset.data))
			}
			session.mu.Unlock()
		}
	}
	usage["preloadMiB"] = nativeCacheUsage{Value: bytes, Available: true, Detail: "当前所有会话的前缀/清单合计；上限为单次预加载预算"}
	return usage
}

// validateCacheReduction 校验缓存Reduction。
func (engine *nativeEngine) validateCacheReduction(limits nativeCacheLimits) error {
	for key, name := range map[string]string{"catalogMiB": "catalogs.json", "sourceRecordsMiB": "sources.json"} {
		usage := nativeCacheFileUsage(filepath.Join(engine.directory, name))
		if !usage.Available {
			return errors.New("无法核对现有剧库或站源记录大小，未修改设置")
		}
		if usage.Value > int64(limits[key])<<20 {
			return fmt.Errorf("%s 当前占用超过拟设上限 %d MiB；请提高上限，原目录和记录不会被删除", name, limits[key])
		}
	}
	return nil
}
