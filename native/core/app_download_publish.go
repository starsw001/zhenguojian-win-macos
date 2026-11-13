package core

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// nativePublishedName 根据作品和分集信息生成下载成品名称。
func nativePublishedName(job nativeDownloadJob, name string) bool {
	parts := strings.Split(name, "/")
	identity := sha256.Sum256([]byte(job.Drama.ID))
	return len(parts) == 3 && parts[0] == "exports" && parts[1] != "" &&
		!strings.ContainsAny(name, "\\:\x00") && !strings.ContainsAny(parts[1], "\r\n") &&
		strings.HasSuffix(parts[1], "["+job.Drama.Source+"-"+hex.EncodeToString(identity[:])[:12]+"]") &&
		parts[2] == fmt.Sprintf("S01E%03d.mp4", job.Index) && filepath.ToSlash(filepath.Clean(name)) == name
}

// nativeRegularWithin 检查目标是否为指定目录内的普通文件。
func nativeRegularWithin(root, relative string) (os.FileInfo, error) {
	if info, err := os.Lstat(root); err != nil || info.Mode()&os.ModeSymlink != 0 {
		return nil, errors.New("下载根目录不可安全访问")
	}
	full := filepath.Join(root, filepath.FromSlash(relative))
	rel, err := filepath.Rel(root, full)
	if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return nil, errors.New("文件路径超出下载目录")
	}
	current := root
	for _, component := range strings.Split(filepath.ToSlash(rel), "/") {
		current = filepath.Join(current, component)
		info, err := os.Lstat(current)
		if err != nil {
			return nil, err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return nil, errors.New("下载目录包含符号链接，已保留文件")
		}
	}
	info, err := os.Stat(full)
	if err != nil || !info.Mode().IsRegular() {
		return nil, errors.New("MP4 成品不是普通文件")
	}
	return info, nil
}

// removeFiles 移除文件列表。
func (manager *nativeDownloads) removeFiles(record nativeDownloadRecord) error {
	if record.PublishedFile != "" {
		if !nativePublishedName(record.nativeDownloadJob, record.PublishedFile) {
			return errors.New("成品路径无效，已保留视频")
		}
		file := filepath.Join(manager.root, filepath.FromSlash(record.PublishedFile))
		if _, err := os.Lstat(file); err == nil {
			info, err := nativeRegularWithin(manager.root, record.PublishedFile)
			if err != nil || info.Size() != record.PublishedBytes {
				return errors.New("成品已改变，已保留视频")
			}
			if err := os.Remove(file); err != nil {
				return err
			}
		} else if !os.IsNotExist(err) {
			return err
		}
	}
	if err := os.RemoveAll(manager.jobDirectory(record.nativeDownloadJob)); err != nil {
		return err
	}
	return manager.removeEmptyPublishedDirectory(record)
}

// removeEmptyPublishedDirectory 移除空已发布目录。
func (manager *nativeDownloads) removeEmptyPublishedDirectory(record nativeDownloadRecord) error {
	if record.PublishedFile == "" {
		return nil
	}
	if !nativePublishedName(record.nativeDownloadJob, record.PublishedFile) {
		return errors.New("成品路径无效，已保留目录")
	}
	directory := filepath.Dir(filepath.Join(manager.root, filepath.FromSlash(record.PublishedFile)))
	for _, current := range []string{manager.root, filepath.Join(manager.root, "exports"), directory} {
		info, err := os.Lstat(current)
		if os.IsNotExist(err) {
			return nil
		}
		if err != nil {
			return err
		}
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return errors.New("导出目录包含链接或不是普通目录，已保留")
		}
	}
	entries, err := os.ReadDir(directory)
	if err != nil || len(entries) != 0 {
		return err
	}
	if err := os.Remove(directory); err != nil && !os.IsNotExist(err) {
		if entries, readErr := os.ReadDir(directory); readErr == nil && len(entries) > 0 {
			return nil
		}
		return fmt.Errorf("视频已删除，空剧集目录尚未清理：%w", err)
	}
	return nil
}

// publishedValid 判断已发布有效是否满足当前规则。
func (manager *nativeDownloads) publishedValid(record nativeDownloadRecord) bool {
	if !nativePublishedName(record.nativeDownloadJob, record.PublishedFile) || record.PublishedBytes <= 0 || len(record.PublishedSHA256) != 64 {
		return false
	}
	info, err := nativeRegularWithin(manager.root, record.PublishedFile)
	return err == nil && info.Size() == record.PublishedBytes
}

// publish 将下载任务的有效成品整理到发布目录。
func (manager *nativeDownloads) publish(ctx context.Context, input nativeInput) error {
	manager.mu.Lock()
	record := manager.jobs[input.JobID]
	if manager.loadErr != nil {
		manager.mu.Unlock()
		return manager.loadErr
	}
	if record == nil || !nativeDownloadAvailable(record.nativeDownloadJob) || manager.closed || manager.moving || manager.active[input.JobID] != nil {
		manager.mu.Unlock()
		return errors.New("下载任务尚未就绪，原文件保留")
	}
	job, root := record.nativeDownloadJob, manager.root
	manager.mu.Unlock()
	if (job.State != "completed" && job.State != "failed") || nativeDownloadVersion(job) != input.ExpectedVersions[job.ID] ||
		!nativePublishedName(job, input.PublishedFile) || input.PublishedBytes <= 0 {
		return errors.New("分集或成品路径已改变，已保留原文件")
	}
	info, err := nativeRegularWithin(root, input.PublishedFile)
	if err != nil || info.Size() != input.PublishedBytes {
		return errors.New("MP4 成品缺失或不完整，已保留缓存")
	}
	file, err := nativeOpenPublishedFile(filepath.Join(root, filepath.FromSlash(input.PublishedFile)))
	if err != nil {
		return err
	}
	hash := sha256.New()
	buffer := make([]byte, 1024*1024)
	for {
		if err = ctx.Err(); err != nil {
			break
		}
		var count int
		count, err = file.Read(buffer)
		if count > 0 {
			_, _ = hash.Write(buffer[:count])
		}
		if err != nil {
			break
		}
	}
	closeErr := file.Close()
	if err != io.EOF {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	if hex.EncodeToString(hash.Sum(nil)) != input.PublishedSHA256 {
		return errors.New("MP4 校验不一致，已保留原文件和缓存")
	}
	manager.mu.Lock()
	defer manager.mu.Unlock()
	record = manager.jobs[input.JobID]
	if record == nil || manager.root != root || manager.closed || manager.moving || manager.active[job.ID] != nil ||
		record.State != job.State || nativeDownloadVersion(record.nativeDownloadJob) != input.ExpectedVersions[job.ID] {
		return errors.New("下载任务已改变，已保留成品和缓存")
	}
	if record.PublishedSHA256 != "" && record.PublishedSHA256 != input.PublishedSHA256 {
		return errors.New("已登记的同集成品内容已改变，未覆盖记录或清理缓存，请手动确认")
	}
	verified, statErr := nativeRegularWithin(root, input.PublishedFile)
	if statErr != nil || verified.Size() != input.PublishedBytes || !os.SameFile(info, verified) {
		return errors.New("MP4 成品已被移动、删除或替换，原始缓存和记录已保留，不会自动重新下载")
	}
	old := *record
	record.PublishedFile, record.PublishedBytes, record.PublishedSHA256 = input.PublishedFile, input.PublishedBytes, input.PublishedSHA256
	record.Published, record.State, record.Error, record.Progress = false, "completed", "", 1
	if err = manager.saveLocked(); err != nil {
		*record = old
		return err
	}
	if err = manager.cleanPublishedCache(record.nativeDownloadJob); err != nil {
		return err
	}
	record.Published = true
	return manager.saveLocked()
}

// cleanPublishedCache 清理已发布缓存。
func (manager *nativeDownloads) cleanPublishedCache(job nativeDownloadJob) error {
	root := manager.root
	directory := manager.jobDirectory(job)
	relative, err := filepath.Rel(root, directory)
	if err != nil || relative == "." || filepath.IsAbs(relative) || strings.HasPrefix(relative, "..") {
		return errors.New("缓存路径无效，成品和记录已保存")
	}
	if _, err = os.Lstat(directory); err == nil {
		for current := directory; current != root; current = filepath.Dir(current) {
			info, err := os.Lstat(current)
			if err != nil || info.Mode()&os.ModeSymlink != 0 {
				return errors.New("缓存路径不可安全清理，MP4 和记录已保存")
			}
		}
		if err = os.RemoveAll(directory); err != nil {
			return errors.New("MP4 和记录已保存，缓存正在使用，可稍后重试清理")
		}
	} else if !os.IsNotExist(err) {
		return err
	}
	backups := filepath.Join(root, ".replaced-downloads")
	if info, statErr := os.Lstat(backups); statErr == nil {
		if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			return errors.New("原线路缓存路径不可安全清理，已保留文件")
		}
		entries, err := os.ReadDir(backups)
		if err != nil {
			return err
		}
		for _, entry := range entries {
			if !strings.HasPrefix(entry.Name(), job.ID+"-") {
				continue
			}
			if !entry.IsDir() || entry.Type()&os.ModeSymlink != 0 {
				return errors.New("原线路缓存不是受管目录，已保留文件")
			}
			relativeMarker := filepath.Join(".replaced-downloads", entry.Name(), "original", ".bundle")
			markerInfo, err := nativeRegularWithin(root, relativeMarker)
			if err != nil || markerInfo.Size() != 64 {
				return errors.New("原线路缓存标识不完整，已保留文件")
			}
			marker, err := os.ReadFile(filepath.Join(root, relativeMarker))
			if err != nil {
				return err
			}
			if _, err = hex.DecodeString(string(marker)); err != nil {
				return errors.New("原线路缓存标识无效，已保留文件")
			}
			if err = os.RemoveAll(filepath.Join(backups, entry.Name())); err != nil {
				return err
			}
		}
		if entries, err = os.ReadDir(backups); err == nil && len(entries) == 0 {
			_ = os.Remove(backups)
		}
	} else if !os.IsNotExist(statErr) {
		return statErr
	}
	if strings.HasPrefix(job.Folder, ".download-cache/") {
		for parent := filepath.Dir(directory); parent != root; parent = filepath.Dir(parent) {
			entries, err := os.ReadDir(parent)
			if err != nil || len(entries) != 0 {
				break
			}
			if err = os.Remove(parent); err != nil {
				break
			}
		}
	}
	return nil
}
