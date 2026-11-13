package core

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
)

// jobDirectory 取得下载任务对应的本地目录。
func (manager *nativeDownloads) jobDirectory(job nativeDownloadJob) string {
	if job.Folder != "" {
		return filepath.Join(manager.root, filepath.FromSlash(job.Folder))
	}
	return filepath.Join(manager.root, job.ID)
}

type nativeDownloadFolderFile struct {
	relative string
	size     int64
}

// downloadFolder 定位已登记的下载成品目录，找不到时返回对应错误。
func (manager *nativeDownloads) downloadFolder(ctx context.Context, ids []string) (string, error) {
	if len(ids) > 100000 {
		return "", errors.New("要打开的下载任务过多，请选择单集")
	}
	manager.mu.Lock()
	if manager.loadErr != nil {
		err := manager.loadErr
		manager.mu.Unlock()
		return "", err
	}
	if manager.closed || manager.moving {
		manager.mu.Unlock()
		return "", errors.New("下载目录暂不可用，请稍后重试")
	}
	root := manager.root
	published, direct := []nativeDownloadFolderFile{}, []nativeDownloadFolderFile{}
	for _, id := range ids {
		record := manager.jobs[id]
		if record == nil || record.State != "completed" || !nativeDownloadAvailable(record.nativeDownloadJob) {
			continue
		}
		if record.PublishedBytes > 0 && len(record.PublishedSHA256) == 64 && nativePublishedName(record.nativeDownloadJob, record.PublishedFile) {
			published = append(published, nativeDownloadFolderFile{record.PublishedFile, record.PublishedBytes})
		}
		if record.File == "media.mp4" && record.Key == "" {
			folder := record.Folder
			if folder == "" {
				folder = record.ID
			}
			direct = append(direct, nativeDownloadFolderFile{folder + "/media.mp4", record.Bytes})
		}
	}
	manager.mu.Unlock()

	for _, candidates := range [][]nativeDownloadFolderFile{published, direct} {
		for _, candidate := range candidates {
			if err := ctx.Err(); err != nil {
				return "", errors.New("读取下载目录超时，请检查磁盘后重试")
			}
			info, err := nativeRegularWithin(root, candidate.relative)
			if err == nil && info.Size() > 0 && (candidate.size <= 0 || info.Size() == candidate.size) {
				manager.mu.Lock()
				ready := !manager.closed && !manager.moving && manager.root == root
				manager.mu.Unlock()
				if !ready {
					return "", errors.New("下载目录已变更，请重试")
				}
				return filepath.Dir(filepath.Join(root, filepath.FromSlash(candidate.relative))), nil
			}
		}
	}
	return "", nil
}

// localFilesValid 检查已登记的本地成品是否仍满足文件约束。
func (manager *nativeDownloads) localFilesValid(record nativeDownloadRecord) bool {
	if manager.publishedValid(record) {
		return true
	}
	if record.File == "" {
		return false
	}
	directory := manager.jobDirectory(record.nativeDownloadJob)
	seen := map[string]bool{}
	var validate func(string, int) bool
	validate = func(name string, depth int) bool {
		if depth > 8 || len(seen) > 20100 || filepath.Base(name) != name || strings.ContainsAny(name, "/\\:") {
			return false
		}
		if seen[name] {
			return true
		}
		seen[name] = true
		file := filepath.Join(directory, name)
		info, err := os.Stat(file)
		if err != nil || !info.Mode().IsRegular() || info.Size() <= 0 {
			return false
		}
		if name == "media.mp4" && record.Bytes > 0 && info.Size() != record.Bytes {
			return false
		}
		if !strings.HasSuffix(name, ".m3u8") {
			return true
		}
		if info.Size() > 4<<20 {
			return false
		}
		data, err := os.ReadFile(file)
		if err != nil || !strings.HasPrefix(strings.TrimSpace(string(data)), "#EXTM3U") {
			return false
		}
		for _, line := range strings.Split(string(data), "\n") {
			line = strings.TrimSpace(line)
			if line != "" && !strings.HasPrefix(line, "#") && !validate(line, depth+1) {
				return false
			}
			for _, match := range nativePlaylistURI.FindAllStringSubmatch(line, -1) {
				if !validate(match[1], depth+1) {
					return false
				}
			}
		}
		return true
	}
	return validate(record.File, 0)
}

type nativeDownloadControlResult struct {
	Completed []string          `json:"completed"`
	Failures  map[string]string `json:"failures"`
}

// controlBatch 返回控制批量。
func (manager *nativeDownloads) controlBatch(ids []string, command string) (nativeDownloadControlResult, error) {
	return manager.controlBatchExpected(context.Background(), ids, command, nil)
}

// controlBatchExpected 按预期版本批量执行下载任务控制，返回各条目的处理结果。
func (manager *nativeDownloads) controlBatchExpected(ctx context.Context, ids []string, command string, expected map[string]string) (nativeDownloadControlResult, error) {
	result := nativeDownloadControlResult{Completed: []string{}, Failures: map[string]string{}}
	if len(ids) == 0 || len(ids) > 500 {
		return result, errors.New("请选择 1 至 500 个下载任务")
	}
	switch command {
	case "pause", "resume", "remove", "archive", "restore":
	default:
		return result, errors.New("批量下载操作无效")
	}
	seen := map[string]bool{}
	for _, id := range ids {
		if seen[id] {
			continue
		}
		seen[id] = true
		if ctx.Err() != nil {
			result.Failures[id] = "操作已停止，尚未处理此任务"
			continue
		}
		if expected != nil && expected[id] == "" {
			result.Failures[id] = "原分集校验信息缺失，已保留文件"
			continue
		}
		if err := manager.controlExpected(id, command, expected[id]); err != nil {
			result.Failures[id] = publicError(err).Error()
		} else {
			result.Completed = append(result.Completed, id)
		}
	}
	return result, nil
}
