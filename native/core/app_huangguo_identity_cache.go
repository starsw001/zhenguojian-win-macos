package core

import (
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
)

const huangguoIdentityCacheEpoch = "huangguoai-detail-identity-v1"

// huangguoIdentityCacheKey 组合来源身份字段，生成身份缓存键。
func huangguoIdentityCacheKey(key string) bool {
	return key == sourceHuangguoAI || strings.HasPrefix(key, sourceHuangguoAI+"|")
}

// rebuildHuangguoIdentityCache 重建黄果身份缓存。
func (engine *nativeEngine) rebuildHuangguoIdentityCache(epoch string) (int, error) {
	if buildAllSources != "true" || epoch == "" {
		return 0, nil
	}
	engine.mu.Lock()
	defer engine.mu.Unlock()
	if engine.catalogEpochs[huangguoIdentityCacheEpoch] == epoch {
		return 0, nil
	}
	file := filepath.Join(engine.directory, "catalogs.json")
	if info, err := os.Lstat(file); err == nil && (!info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0) {
		return 0, errors.New("目录缓存不是普通文件，原文件保留")
	}
	disk := map[string]json.RawMessage{}
	handle, err := os.Open(file)
	if err != nil && !os.IsNotExist(err) {
		return 0, errors.New("目录缓存无法读取，原文件保留")
	}
	if err == nil {
		limit := engine.cacheBytes("catalogMiB")
		info, statErr := handle.Stat()
		if statErr != nil || info.Size() > int64(limit) {
			_ = handle.Close()
			return 0, errors.New("目录缓存超过保护上限，原文件保留")
		}
		body, readErr := io.ReadAll(io.LimitReader(handle, int64(limit)+1))
		_ = handle.Close()
		if readErr != nil || len(body) > limit || json.Unmarshal(body, &disk) != nil || disk == nil {
			return 0, errors.New("目录缓存损坏或不完整，原文件保留")
		}
	}
	removed := 0
	if _, modern := disk["catalogs"]; !modern && len(disk) > 0 {
		legacy, _ := json.Marshal(disk)
		disk = map[string]json.RawMessage{"version": json.RawMessage(`3`), "catalogs": legacy, "states": json.RawMessage(`{}`)}
	}
	if len(disk) == 0 {
		disk["version"], disk["catalogs"], disk["states"] = json.RawMessage(`3`), json.RawMessage(`{}`), json.RawMessage(`{}`)
	}
	for _, field := range []string{"catalogs", "states", "categories"} {
		if len(disk[field]) == 0 || string(disk[field]) == "null" {
			continue
		}
		var entries map[string]json.RawMessage
		if json.Unmarshal(disk[field], &entries) != nil {
			return 0, errors.New("目录缓存结构无效，原文件保留")
		}
		for key := range entries {
			if huangguoIdentityCacheKey(key) {
				delete(entries, key)
				removed++
			}
		}
		disk[field], _ = json.Marshal(entries)
	}
	epochs := make(map[string]string, len(engine.catalogEpochs)+1)
	if raw := disk["catalogEpochs"]; len(raw) > 0 && string(raw) != "null" {
		if json.Unmarshal(raw, &epochs) != nil {
			return 0, errors.New("目录缓存代次无法读取，原文件保留")
		}
	}
	for key, value := range engine.catalogEpochs {
		epochs[key] = value
	}
	epochs[huangguoIdentityCacheEpoch] = epoch
	disk["catalogEpochs"], _ = json.Marshal(epochs)
	body, err := json.Marshal(disk)
	if err != nil || len(body) > engine.cacheBytes("catalogMiB") {
		return 0, errors.New("重建目录超过保护上限，原文件保留")
	}
	if err := writeNativeCacheFile(file, body); err != nil {
		return 0, errors.New("目录身份缓存重建失败，原文件和其他记录保留")
	}
	engine.catalogEpochs = epochs
	engine.huangguoFreshMetadata = nil
	for key := range engine.catalogs {
		if huangguoIdentityCacheKey(key) {
			delete(engine.catalogs, key)
		}
	}
	for key := range engine.catalogStates {
		if huangguoIdentityCacheKey(key) {
			delete(engine.catalogStates, key)
		}
	}
	for key := range engine.categoryOptions {
		if huangguoIdentityCacheKey(key) {
			delete(engine.categoryOptions, key)
		}
	}
	if engine.downloader != nil {
		engine.downloader.providerMu.Lock()
		engine.downloader.huangguoCatalogSections = nil
		engine.downloader.providerMu.Unlock()
	}
	return removed, nil
}
