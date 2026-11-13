package core

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

type nativeCollectionMedia struct {
	ID    string      `json:"id"`
	Drama nativeDrama `json:"drama"`
	File  string      `json:"file"`
	Kind  string      `json:"kind"`
}

// nativeCollectionPath 取得下载合集对应的本地目录。
func nativeCollectionPath(root, relative string) (string, error) {
	relative = strings.ReplaceAll(relative, "\\", "/")
	if relative == "" || strings.ContainsAny(relative, ":\x00") || strings.HasPrefix(relative, "/") {
		return "", errors.New("合集文件路径无效，已保留记录")
	}
	for _, part := range strings.Split(relative, "/") {
		if part == "" || part == "." || part == ".." {
			return "", errors.New("合集文件路径越界，已保留记录")
		}
	}
	full := filepath.Join(root, filepath.FromSlash(relative))
	current := root
	for _, part := range append([]string{""}, strings.Split(relative, "/")...) {
		if part != "" {
			current = filepath.Join(current, part)
		}
		info, err := os.Lstat(current)
		if os.IsNotExist(err) {
			return full, nil
		}
		if err != nil {
			return "", err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return "", errors.New("合集目录包含链接，已保留文件和记录")
		}
	}
	return full, nil
}

// removeDownloadCollection 移除下载合集。
func (engine *nativeEngine) removeDownloadCollection(ctx context.Context, drama nativeDrama) (nativeDownloadControlResult, error) {
	result := nativeDownloadControlResult{Completed: []string{}, Failures: map[string]string{}}
	if drama.ID == "" || !nativeDramaAvailable(drama) {
		return result, errNativeBuildSource
	}
	if _, err := engine.workLease("mergeCleanup", "start"); err != nil {
		return result, err
	}
	defer engine.workLease("mergeCleanup", "end")
	manager := engine.downloads
	manager.mu.Lock()
	if manager.closed || manager.moving || manager.loadErr != nil {
		manager.mu.Unlock()
		return result, errors.New("下载记录尚未就绪或目录正在迁移，已保留文件")
	}
	root := manager.root
	records := []nativeDownloadRecord{}
	for _, record := range manager.jobs {
		if record.Drama.ID == drama.ID {
			records = append(records, *record)
		}
	}
	manager.mu.Unlock()
	sort.Slice(records, func(i, j int) bool { return records[i].ID < records[j].ID })
	identity := sha256.Sum256([]byte(drama.ID))
	suffix := "[" + drama.Source + "-" + hex.EncodeToString(identity[:])[:12] + "]"
	ownedFolder := func(folder string) bool {
		parts := strings.Split(strings.ReplaceAll(folder, "\\", "/"), "/")
		return len(parts) == 2 && parts[0] == "exports" && strings.HasSuffix(parts[1], suffix)
	}
	allowed := map[string]bool{}
	folders := map[string]bool{}
	files := map[string]string{}
	addFile := func(relative string, remove bool) error {
		full, err := nativeCollectionPath(root, relative)
		if err != nil {
			return err
		}
		if info, err := os.Lstat(full); err == nil && !info.Mode().IsRegular() {
			return errors.New("合集媒体不是普通文件，已保留记录")
		} else if err != nil && !os.IsNotExist(err) {
			return err
		}
		allowed[filepath.Clean(full)] = true
		if remove {
			files[filepath.Clean(full)] = relative
		}
		return nil
	}
	for _, record := range records {
		if record.PublishedFile != "" {
			if !nativePublishedName(record.nativeDownloadJob, record.PublishedFile) {
				return result, errors.New("合集成品归属不匹配，已保留文件")
			}
			if err := addFile(record.PublishedFile, false); err != nil {
				return result, err
			}
			folders[filepath.Dir(filepath.Join(root, filepath.FromSlash(record.PublishedFile)))] = true
		}
		cache := manager.jobDirectory(record.nativeDownloadJob)
		relative, err := filepath.Rel(root, cache)
		if err != nil || filepath.Base(cache) != record.ID {
			return result, errors.New("合集缓存路径无效，已保留文件")
		}
		if _, err := nativeCollectionPath(root, relative); err != nil {
			return result, err
		}
	}
	index, indexErr := nativeCollectionPath(root, "media-library.json")
	if indexErr != nil {
		return result, indexErr
	}
	data := map[string]json.RawMessage{}
	rows, remaining := []json.RawMessage{}, []json.RawMessage{}
	body, err := os.ReadFile(index)
	exists := !os.IsNotExist(err)
	if err != nil && exists {
		return result, err
	}
	if exists {
		if len(body) > 16<<20 || json.Unmarshal(body, &data) != nil || json.Unmarshal(data["items"], &rows) != nil {
			return result, errors.New("本地媒体记录无法读取，已保留文件")
		}
		for _, row := range rows {
			var item nativeCollectionMedia
			if json.Unmarshal(row, &item) != nil {
				return result, errors.New("本地媒体条目无效，已保留文件")
			}
			if item.Drama.ID != drama.ID {
				remaining = append(remaining, row)
				continue
			}
			relative := strings.ReplaceAll(item.File, "\\", "/")
			parts := strings.Split(relative, "/")
			if item.Drama.Source != drama.Source {
				return result, errors.New("合集站源标识冲突，已保留文件")
			}
			if item.Kind == "merged" {
				if len(parts) != 3 || parts[0] != "library" || parts[1] != item.ID || !(parts[2] == "full.mp4" || parts[2] == "full.mkv") {
					return result, errors.New("合并成品路径归属无效，已保留文件")
				}
			} else if item.Kind == "export" || item.Kind == "special" {
				if len(parts) < 3 || !ownedFolder(strings.Join(parts[:2], "/")) {
					return result, errors.New("导出合集目录归属无效，已保留文件")
				}
				if !strings.HasSuffix(parts[len(parts)-1], ".mp4") && !strings.HasSuffix(parts[len(parts)-1], ".mkv") {
					return result, errors.New("导出媒体扩展名无效，已保留文件")
				}
			} else {
				return result, errors.New("合集包含未知媒体类型，已保留记录")
			}
			if err := addFile(relative, true); err != nil {
				return result, err
			}
			if item.Kind == "merged" {
				folders[filepath.Dir(filepath.Join(root, filepath.FromSlash(relative)))] = true
			} else {
				folders[filepath.Join(root, filepath.FromSlash(strings.Join(parts[:2], "/")))] = true
			}
		}
		var mapped map[string]string
		if raw := data["showFolders"]; len(raw) > 0 {
			if json.Unmarshal(raw, &mapped) != nil {
				return result, errors.New("导出目录映射无效，已保留文件")
			}
			if folder := mapped[drama.ID]; folder != "" {
				if !ownedFolder(folder) {
					return result, errors.New("合集目录映射归属无效，已保留文件")
				}
				full, err := nativeCollectionPath(root, folder)
				if err != nil {
					return result, err
				}
				folders[full] = true
				for id, other := range mapped {
					if id != drama.ID && strings.EqualFold(filepath.Clean(filepath.Join(root, other)), full) {
						return result, errors.New("合集目录被其他作品共用，已保留文件")
					}
				}
				delete(mapped, drama.ID)
				data["showFolders"], _ = json.Marshal(mapped)
			}
		}
		if raw := data["specialNumbers"]; len(raw) > 0 {
			var numbers map[string]json.RawMessage
			if json.Unmarshal(raw, &numbers) != nil {
				return result, errors.New("特别篇记录无效，任务记录已保留")
			}
			for key := range numbers {
				if strings.HasPrefix(key, drama.ID+"\x00") {
					delete(numbers, key)
				}
			}
			data["specialNumbers"], _ = json.Marshal(numbers)
		}
		for _, row := range remaining {
			var item nativeCollectionMedia
			_ = json.Unmarshal(row, &item)
			full, err := nativeCollectionPath(root, item.File)
			if err == nil {
				for folder := range folders {
					rel, _ := filepath.Rel(folder, full)
					if rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel) {
						return result, errors.New("其他作品引用了该合集目录，已保留文件")
					}
				}
			}
		}
	}
	directories := map[string]bool{}
	for folder := range folders {
		relative, err := filepath.Rel(root, folder)
		if err != nil {
			return result, err
		}
		if _, err := nativeCollectionPath(root, relative); err != nil {
			return result, err
		}
		err = filepath.WalkDir(folder, func(file string, entry os.DirEntry, walkErr error) error {
			if os.IsNotExist(walkErr) {
				return nil
			}
			if walkErr != nil {
				return walkErr
			}
			if entry.Type()&os.ModeSymlink != 0 {
				return errors.New("合集目录包含链接，已保留文件")
			}
			if entry.IsDir() {
				directories[file] = true
				return nil
			}
			if !allowed[filepath.Clean(file)] {
				return fmt.Errorf("合集目录仍有未登记文件 %s，已保留文件和任务，请先手动处理", filepath.Base(file))
			}
			return nil
		})
		if err != nil {
			return result, err
		}
	}
	for _, record := range records {
		if record.State == "queued" || record.State == "downloading" {
			if err := manager.control(record.ID, "pause"); err != nil {
				return result, err
			}
		}
	}
	deadline := time.Now().Add(10 * time.Second)
	for {
		manager.mu.Lock()
		active := false
		for _, record := range records {
			active = active || manager.active[record.ID] != nil
		}
		manager.mu.Unlock()
		if !active {
			break
		}
		if time.Now().After(deadline) {
			return result, errors.New("下载写入尚未停止，任务记录已保留，请稍后重试")
		}
		select {
		case <-ctx.Done():
			return result, ctx.Err()
		case <-time.After(20 * time.Millisecond):
		}
	}
	for full, relative := range files {
		if err := ctx.Err(); err != nil {
			return result, err
		}
		checked, err := nativeCollectionPath(root, relative)
		if err != nil || checked != full {
			return result, errors.New("删除前媒体路径发生变化，已保留记录")
		}
		if err := os.Remove(full); err != nil && !os.IsNotExist(err) {
			return result, fmt.Errorf("视频正在使用或无法删除，任务记录已保留，请退出播放后重试：%w", err)
		}
	}
	for _, record := range records {
		relative, err := filepath.Rel(root, manager.jobDirectory(record.nativeDownloadJob))
		if err != nil {
			return result, err
		}
		if _, err := nativeCollectionPath(root, relative); err != nil {
			return result, err
		}
		if err := manager.removeFiles(record); err != nil {
			return result, fmt.Errorf("分集无法删除，任务记录已保留：%w", err)
		}
	}
	ordered := []string{}
	for dir := range directories {
		ordered = append(ordered, dir)
	}
	sort.Slice(ordered, func(i, j int) bool { return len(ordered[i]) > len(ordered[j]) })
	for _, dir := range ordered {
		if err := os.Remove(dir); err != nil && !os.IsNotExist(err) {
			return result, fmt.Errorf("合集目录未能清空，任务记录已保留：%w", err)
		}
	}
	if exists {
		data["items"], _ = json.Marshal(remaining)
		updated, err := json.Marshal(data)
		if err != nil {
			return result, err
		}
		if err := nativeDownloadWrite(index, updated); err != nil {
			return result, errors.New("视频已删除，媒体记录尚未清理；下载任务记录已保留，请重试")
		}
	}
	ids := []string{}
	for _, record := range records {
		ids = append(ids, record.ID)
	}
	for start := 0; start < len(ids); start += 500 {
		batch, err := manager.controlBatchExpected(ctx, ids[start:min(start+500, len(ids))], "remove", nil)
		if err != nil {
			return result, err
		}
		result.Completed = append(result.Completed, batch.Completed...)
		for id, failure := range batch.Failures {
			result.Failures[id] = failure
		}
	}
	return result, nil
}
