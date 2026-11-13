package core

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
)

type nativeStorageInfo struct {
	Directory string `json:"directory"`
	Bytes     int64  `json:"bytes"`
	Free      int64  `json:"free"`
	Files     int    `json:"files"`
}

// storage 返回存储。
func (engine *nativeEngine) storage() (nativeStorageInfo, error) {
	manager := engine.downloads
	manager.mu.Lock()
	root := manager.root
	manager.mu.Unlock()
	info := nativeStorageInfo{Directory: root}
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return nil
		}
		if !entry.IsDir() {
			if stat, err := entry.Info(); err == nil {
				info.Bytes += stat.Size()
				info.Files++
			}
		}
		return nil
	})
	if err != nil {
		return info, errors.New("无法读取下载目录，请检查存储设备")
	}
	info.Free = nativeFreeSpace(root)
	return info, nil
}

// nativeDownloadLocation 下载原生Location。
func nativeDownloadLocation(directory string, defaultDownloads ...string) string {
	if nativePortableRoot(directory) != "" {
		path, _ := nativePortableDownloadLocation(directory)
		return path
	}
	var value struct {
		Directory string `json:"directory"`
		Mode      string `json:"mode"`
	}
	body, locationErr := os.ReadFile(filepath.Join(directory, "download-location.json"))
	if json.Unmarshal(body, &value) == nil && filepath.IsAbs(value.Directory) {
		if value.Mode == "default" && len(defaultDownloads) > 0 && filepath.IsAbs(defaultDownloads[0]) && nativeDefaultDownloadCanFollow(value.Directory, defaultDownloads[0]) {
			return filepath.Clean(defaultDownloads[0])
		}
		return filepath.Clean(value.Directory)
	}
	legacy := filepath.Join(directory, "downloads")
	if !os.IsNotExist(locationErr) {
		return legacy
	}
	if _, err := os.Lstat(legacy); !os.IsNotExist(err) {
		return legacy
	}
	if len(defaultDownloads) > 0 && filepath.IsAbs(defaultDownloads[0]) {
		return filepath.Clean(defaultDownloads[0])
	}
	return legacy
}

// nativeDefaultDownloadCanFollow 构造默认原生下载CanFollow。
func nativeDefaultDownloadCanFollow(previous, current string) bool {
	if filepath.Clean(previous) == filepath.Clean(current) {
		return true
	}
	if _, err := os.Lstat(previous); os.IsNotExist(err) {
		return true
	}
	if entries, err := os.ReadDir(previous); err == nil && len(entries) == 0 {
		return true
	}
	if info, err := os.Stat(filepath.Join(current, "index.json")); err == nil && info.Mode().IsRegular() {
		return true
	}
	return false
}

// nativeCopyTree 复制目录树中的文件到目标位置。
func nativeCopyTree(ctx context.Context, source, target string) error {
	return filepath.WalkDir(source, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return errors.New("下载目录中包含符号链接，无法迁移")
		}
		relative, err := filepath.Rel(source, path)
		if err != nil {
			return err
		}
		destination := filepath.Join(target, relative)
		if entry.IsDir() {
			return os.MkdirAll(destination, 0700)
		}
		input, err := os.Open(path)
		if err != nil {
			return err
		}
		defer input.Close()
		output, err := os.OpenFile(destination, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if err != nil {
			return err
		}
		defer output.Close()
		buffer := make([]byte, 512<<10)
		for {
			if err := ctx.Err(); err != nil {
				return err
			}
			count, readErr := input.Read(buffer)
			if count > 0 {
				written, err := output.Write(buffer[:count])
				if err != nil {
					return err
				}
				if written != count {
					return io.ErrShortWrite
				}
			}
			if readErr == io.EOF {
				break
			}
			if readErr != nil {
				return readErr
			}
		}
		return output.Sync()
	})
}

// moveDownloads 按存储设置执行下载目录迁移。
func (engine *nativeEngine) moveDownloads(ctx context.Context, parent string, exact ...bool) error {
	if nativePortableRoot(engine.directory) != "" {
		return errors.New("便携版下载保存在当前软件目录的 download 文件夹；更换位置请移动整个软件文件夹")
	}
	if !filepath.IsAbs(parent) {
		return errors.New("请选择有效的保存目录")
	}
	manager := engine.downloads
	resolved, err := filepath.EvalSymlinks(parent)
	exactTarget := len(exact) > 0 && exact[0]
	if os.IsNotExist(err) && exactTarget {
		var directory string
		directory, err = filepath.EvalSymlinks(filepath.Dir(filepath.Clean(parent)))
		if err == nil {
			resolved = filepath.Join(directory, filepath.Base(filepath.Clean(parent)))
		}
	}
	if err != nil {
		return errors.New("无法打开所选目录")
	}
	parent = resolved
	engine.mu.Lock()
	if engine.work["media"] || engine.work["mergeQueue"] || engine.work["mergeCleanup"] {
		engine.mu.Unlock()
		return errors.New("请等待本地媒体处理完成后再迁移")
	}
	defer engine.mu.Unlock()
	target := filepath.Join(filepath.Clean(parent), "zhenguoshang-downloads")
	if exactTarget {
		target = filepath.Clean(parent)
	}
	manager.mu.Lock()
	sourceMissing := false
	if manager.loadErr != nil {
		_, rootErr := os.Lstat(manager.root)
		_, locationErr := os.Lstat(filepath.Join(engine.directory, "download-location.json"))
		sourceMissing = manager.root == engine.defaultDownloadDirectory && len(manager.jobs) == 0 &&
			os.IsNotExist(rootErr) && os.IsNotExist(locationErr)
		if !sourceMissing {
			manager.mu.Unlock()
			return errors.New("下载记录无法读取，已停止迁移并保留原索引和文件；请先恢复下载记录")
		}
	}
	if manager.closed || manager.moving {
		manager.mu.Unlock()
		return errors.New("下载目录正在处理，请稍后重试")
	}
	if target == manager.root {
		manager.mu.Unlock()
		return nil
	}
	relative, relativeErr := filepath.Rel(manager.root, target)
	inverse, inverseErr := filepath.Rel(target, manager.root)
	if (relativeErr == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(os.PathSeparator))) ||
		(inverseErr == nil && inverse != ".." && !strings.HasPrefix(inverse, ".."+string(os.PathSeparator))) {
		manager.mu.Unlock()
		return errors.New("新目录不能与原下载目录互相包含")
	}
	targetExists := false
	if info, err := os.Stat(target); !os.IsNotExist(err) {
		entries, readErr := os.ReadDir(target)
		if !exactTarget || err != nil || !info.IsDir() || readErr != nil || len(entries) != 0 {
			manager.mu.Unlock()
			return errors.New("目标中已有应用下载目录，请选择空目录，避免覆盖文件")
		}
		targetExists = true
	}
	manager.moving = true
	for id, job := range manager.jobs {
		if job.State == "queued" || job.State == "downloading" {
			job.State = "paused"
		}
		if cancel := manager.active[id]; cancel != nil {
			cancel()
		}
	}
	var saveErr error
	if !sourceMissing {
		saveErr = manager.saveLocked()
	}
	manager.mu.Unlock()
	manager.workers.Wait()
	manager.mu.Lock()
	defer manager.mu.Unlock()
	defer func() { manager.moving = false }()
	if saveErr != nil {
		return saveErr
	}
	source := manager.root
	if !targetExists {
		if err := os.Mkdir(target, 0700); err != nil {
			return errors.New("无法创建目标目录，请检查权限或选择其他位置")
		}
	}
	if !sourceMissing {
		if err := nativeCopyTree(ctx, source, target); err != nil {
			_ = os.RemoveAll(target)
			return errors.New("文件迁移未完成，原文件已保留，请检查空间与目录权限")
		}
	}
	if err := ctx.Err(); err != nil {
		_ = os.RemoveAll(target)
		return err
	}
	data, _ := json.Marshal(map[string]string{"directory": target, "mode": "custom"})
	if err := nativeDownloadWrite(filepath.Join(engine.directory, "download-location.json"), data); err != nil {
		_ = os.RemoveAll(target)
		return errors.New("保存下载目录失败，原文件已保留")
	}
	manager.root = target
	manager.loadErr = nil
	if !sourceMissing {
		_ = os.RemoveAll(source)
	}
	return nil
}

// workLease 返回作品租约。
func (engine *nativeEngine) workLease(id, command string) (int, error) {
	engine.mu.Lock()
	defer engine.mu.Unlock()
	if engine.work == nil {
		engine.work = map[string]bool{}
	}
	if id != "" {
		if id != "media" && id != "storage" && id != "mergeQueue" && id != "mergeCleanup" {
			return 0, errors.New("无效的本地任务")
		}
		if command != "start" && command != "end" {
			return 0, errors.New("无效的本地任务操作")
		}
		manager := engine.downloads
		manager.mu.Lock()
		defer manager.mu.Unlock()
		if command == "start" {
			if manager.updateHandoff || engine.work[id] || manager.moving || id == "media" && engine.work["mergeCleanup"] ||
				id == "mergeCleanup" && engine.work["media"] {
				return 0, errors.New("已有本地任务正在运行，请稍后重试")
			}
			engine.work[id] = true
		} else {
			delete(engine.work, id)
		}
		manager.mediaBusy = engine.work["media"]
	}
	return len(engine.work) + len(engine.sourceTasks), nil
}
