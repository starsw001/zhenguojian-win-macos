package core

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"sync"
	"time"
)

var errNativeLocalFile = errors.New("本地文件缺失或不完整，请重新下载或选择在线播放")

type nativeDownloadEpisode struct {
	Chapter Chapter `json:"chapter"`
	Index   int     `json:"index"`
}

type nativeDownloadJob struct {
	PreferredRoute    int         `json:"preferredRoute,omitempty"`
	PreferredRouteKey string      `json:"preferredRouteKey,omitempty"`
	ID                string      `json:"id"`
	Drama             nativeDrama `json:"drama"`
	Chapter           Chapter     `json:"chapter"`
	Index             int         `json:"index"`
	Quality           int         `json:"quality"`
	ActualQuality     int         `json:"actualQuality"`
	State             string      `json:"state"`
	Archived          bool        `json:"archived,omitempty"`
	Folder            string      `json:"folder,omitempty"`
	Bytes             int64       `json:"bytes"`
	Total             int64       `json:"total"`
	Progress          float64     `json:"progress"`
	Error             string      `json:"error,omitempty"`
	Created           int64       `json:"created"`
	Revision          int64       `json:"revision,omitempty"`
	Published         bool        `json:"published,omitempty"`
}

type nativeDownloadRecord struct {
	nativeDownloadJob
	File            string `json:"file,omitempty"`
	Key             string `json:"key,omitempty"`
	PublishedFile   string `json:"publishedFile,omitempty"`
	PublishedBytes  int64  `json:"publishedBytes,omitempty"`
	PublishedSHA256 string `json:"publishedSHA256,omitempty"`
}

type nativeDownloadResult struct {
	file    string
	key     string
	quality int
	chapter *Chapter
}

type nativeDownloads struct {
	updateHandoff     bool
	mu                sync.Mutex
	root              string
	engine            *nativeEngine
	jobs              map[string]*nativeDownloadRecord
	active            map[string]context.CancelFunc
	closed            bool
	moving            bool
	mediaBusy         bool
	loadErr           error
	workers           sync.WaitGroup
	lastSaved         time.Time
	concurrency       int
	sourceConcurrency int
	sourceStarts      map[string]time.Time
	sourceBackoffs    map[string]*requestBackoff
	queueTimer        *time.Timer
	queueGeneration   uint64
	queueDelay        func() time.Duration
	bySource          bool
	resolve           func(context.Context, nativeDownloadJob) (providerMedia, error)
}

// nativeDownloadID 根据下载任务的关键字段生成任务标识。
func nativeDownloadID(drama string, episode int) string {
	hash := sha256.Sum256([]byte(drama + "\x00" + strconv.Itoa(episode)))
	return hex.EncodeToString(hash[:16])
}

// nativeDownloadVersion 取得下载任务用于并发控制的版本标识。
func nativeDownloadVersion(job nativeDownloadJob) string {
	version := fmt.Sprintf("%d-%d-%d", job.Created, job.Bytes, job.ActualQuality)
	if job.Revision > 0 {
		version += "-" + strconv.FormatInt(job.Revision, 10)
	}
	return version
}

// unarchivedCountLocked 返回未归档数量，供内部持锁路径调用。
func (manager *nativeDownloads) unarchivedCountLocked() int {
	count := 0
	for _, job := range manager.jobs {
		if !job.Archived {
			count++
		}
	}
	return count
}

// newNativeDownloads 创建原生下载任务。
func newNativeDownloads(engine *nativeEngine) *nativeDownloads {
	manager := &nativeDownloads{root: nativeDownloadLocation(engine.directory, engine.defaultDownloadDirectory), engine: engine,
		jobs: map[string]*nativeDownloadRecord{}, active: map[string]context.CancelFunc{}}
	settings := engine.resourceSettings()
	manager.concurrency, manager.bySource = settings.DownloadConcurrency, settings.DownloadBySource
	manager.sourceConcurrency = settings.DownloadSourceConcurrency
	manager.resolve = func(ctx context.Context, job nativeDownloadJob) (providerMedia, error) {
		return engine.downloader.resolvePlaybackMedia(ctx, Task{
			DramaID: job.Drama.ID, DramaTitle: job.Drama.Title, Chapter: job.Chapter, Index: job.Index})
	}
	manager.loadErr = manager.load()
	if manager.loadErr == nil && engine.defaultDownloadDirectory != "" {
		location := filepath.Join(engine.directory, "download-location.json")
		body, readErr := os.ReadFile(location)
		var saved struct {
			Directory string `json:"directory"`
			Mode      string `json:"mode"`
		}
		updateDefault := json.Unmarshal(body, &saved) == nil && saved.Mode == "default" && saved.Directory != manager.root && manager.root == engine.defaultDownloadDirectory
		if os.IsNotExist(readErr) || updateDefault {
			mode := "custom"
			if manager.root == engine.defaultDownloadDirectory {
				mode = "default"
			}
			data, _ := json.Marshal(map[string]string{"directory": manager.root, "mode": mode})
			if err := nativeDownloadWrite(location, data); err != nil {
				manager.loadErr = errors.New("无法保存下载位置，请检查应用数据目录权限")
			}
		}
	}
	return manager
}

// load 从本地文件恢复下载任务与任务状态。
func (manager *nativeDownloads) load() error {
	if err := os.MkdirAll(manager.root, 0700); err != nil {
		return errors.New("无法创建下载目录，请在设置的「下载目录与空间」选择可写位置，不需要管理员权限")
	}
	path := filepath.Join(manager.root, "index.json")
	info, err := os.Stat(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil || !info.Mode().IsRegular() || info.Size() > 32<<20 {
		return errors.New("无法读取下载记录，原文件已保留")
	}
	data, err := os.ReadFile(path)
	var records []*nativeDownloadRecord
	if err != nil || json.Unmarshal(data, &records) != nil || records == nil {
		return errors.New("下载记录无法解析，原文件已保留")
	}
	seen := map[string]bool{}
	for _, record := range records {
		if record == nil || record.Index < 1 || record.ID != nativeDownloadID(record.Drama.ID, record.Index) || seen[record.ID] {
			return errors.New("下载记录条目无效，原索引和文件已保留")
		}
		seen[record.ID] = true
		if record.Folder != "" && record.Folder != record.Drama.Source+"/"+record.ID &&
			record.Folder != ".download-cache/"+record.ID && record.Folder != ".download-cache/"+record.Drama.Source+"/"+record.ID {
			return errors.New("下载目录记录无效，原文件已保留")
		}
		if record.File != "" && record.File != "media.mp4" && record.File != "index.m3u8" {
			return errors.New("下载记录文件信息无效，原索引和文件已保留")
		}
		if record.PublishedFile != "" && !nativePublishedName(record.nativeDownloadJob, record.PublishedFile) {
			return errors.New("MP4 成品记录路径无效，原索引和视频已保留")
		}
		switch record.State {
		case "removing", "downloading", "queued", "paused", "failed", "completed":
		default:
			return errors.New("下载记录状态无效，原索引和文件已保留")
		}
	}
	for _, record := range records {
		record.Drama = migrateNativeDrama(record.Drama)
		if !nativeDownloadAvailable(record.nativeDownloadJob) {
			manager.jobs[record.ID] = record
			continue
		}
		switch record.State {
		case "removing":
			if err := manager.removeFiles(*record); err != nil {
				return err
			}
			continue
		case "downloading", "queued":
			record.State = "paused"
			record.Error = "上次下载已暂停，点击继续"
		case "paused", "failed", "completed":
		default:
			continue
		}
		manager.jobs[record.ID] = record
	}
	return nil
}

// nativeDownloadWrite 下载原生写入。
func nativeDownloadWrite(path string, data []byte) error {
	file, err := os.CreateTemp(filepath.Dir(path), ".download-write-")
	if err != nil {
		return err
	}
	temporary := file.Name()
	defer os.Remove(temporary)
	if err = file.Chmod(0600); err == nil {
		_, err = file.Write(data)
	}
	if err == nil {
		err = file.Sync()
	}
	closeErr := file.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	return os.Rename(temporary, path)
}

// saveLocked 将下载任务快照写入本地存储，供内部持锁流程调用。
func (manager *nativeDownloads) saveLocked() error {
	if manager.loadErr != nil {
		return manager.loadErr
	}
	records := make([]nativeDownloadRecord, 0, len(manager.jobs))
	for _, job := range manager.jobs {
		records = append(records, *job)
	}
	sort.Slice(records, func(i, j int) bool { return records[i].Created < records[j].Created })
	data, err := json.Marshal(records)
	if err != nil {
		return err
	}
	if len(data) > 32<<20 {
		return errors.New("下载记录超过保存上限，原索引已保留")
	}
	if err = nativeDownloadWrite(filepath.Join(manager.root, "index.json"), data); err != nil {
		return errors.New("保存下载记录失败，请检查剩余存储空间")
	}
	manager.lastSaved = time.Now()
	return nil
}

// snapshot 返回当前下载任务的快照与持久化状态。
func (manager *nativeDownloads) snapshot() ([]nativeDownloadJob, error) {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	if manager.loadErr != nil {
		return nil, manager.loadErr
	}
	jobs := make([]nativeDownloadJob, 0, len(manager.jobs))
	for _, record := range manager.jobs {
		if nativeDownloadAvailable(record.nativeDownloadJob) {
			jobs = append(jobs, record.nativeDownloadJob)
		}
	}
	sort.Slice(jobs, func(i, j int) bool {
		if jobs[i].Created == jobs[j].Created {
			return jobs[i].Index < jobs[j].Index
		}
		return jobs[i].Created > jobs[j].Created
	})
	return jobs, nil
}

// enqueue 将请求中的分集加入下载队列。
func (manager *nativeDownloads) enqueue(input nativeInput) (int, error) {
	return manager.enqueueContext(context.Background(), input)
}

// enqueueContext 加入队列：上下文。
func (manager *nativeDownloads) enqueueContext(ctx context.Context, input nativeInput) (int, error) {
	if !nativeDramaAvailable(input.Drama) {
		return 0, errNativeBuildSource
	}
	if _, _, valid := splitProviderDramaID(input.Drama.ID); !valid {
		return 0, errors.New("剧集信息无效")
	}
	if len(input.Entries) == 0 || len(input.Entries) > 500 {
		return 0, errors.New("请选择 1 至 500 集加入下载")
	}
	for _, entry := range input.Entries {
		if !nativeChapterAvailable(input.Drama, entry.Chapter) {
			return 0, errNativeBuildSource
		}
		if entry.Index < 1 || entry.Index > 100000 {
			return 0, errors.New("下载集数无效")
		}
	}
	if input.PreferredRoute < 0 || input.PreferredRoute > 1000 {
		return 0, errors.New("下载候选地址无效")
	}
	preferredKey, preferredEpisode := "", 0
	if input.PreferredRoute > 0 && input.Session != "" {
		manager.engine.mu.Lock()
		choice, ok := manager.engine.playbacks[input.Session]
		manager.engine.mu.Unlock()
		if !ok || choice.dramaID != input.Drama.ID || input.PreferredRoute > len(choice.media) {
			return 0, errors.New("所选候选地址已失效，请重新选择")
		}
		preferredKey, preferredEpisode = nativeRouteKey(choice.media[input.PreferredRoute-1]), choice.episodeIndex
	}
	manager.mu.Lock()
	defer manager.mu.Unlock()
	if manager.closed || manager.moving || manager.updateHandoff {
		return 0, errors.New("下载目录正在迁移、正在切换软件版本或队列已关闭，请稍后重试")
	}
	if manager.loadErr != nil {
		return 0, manager.loadErr
	}
	added := []string{}
	previous := map[string]nativeDownloadRecord{}
	repaired := 0
	tasks := manager.unarchivedCountLocked()
	rollback := func() {
		for id, old := range previous {
			*manager.jobs[id] = old
		}
		for _, id := range added {
			delete(manager.jobs, id)
		}
	}
	for _, entry := range input.Entries {
		if err := ctx.Err(); err != nil {
			rollback()
			return 0, err
		}
		id := nativeDownloadID(input.Drama.ID, entry.Index)
		routeKey := ""
		if entry.Index == preferredEpisode {
			routeKey = preferredKey
		}
		if existing := manager.jobs[id]; existing != nil {
			if input.Force {
				if _, changed := previous[id]; !changed {
					previous[id] = *existing
				}
				existing.Drama = mergeNativeDrama(existing.Drama, input.Drama)
			}
			if (input.Force || input.PreferredRoute > 0) && existing.State == "failed" || (existing.State == "completed" || existing.State == "failed" && existing.File != "") && !manager.localFilesValid(*existing) {
				if _, changed := previous[id]; !changed {
					previous[id] = *existing
				}
				if existing.Archived {
					if tasks >= 5000 {
						rollback()
						return 0, errors.New("下载任务已满，请先清理已完成任务")
					}
					tasks++
				}
				repaired++
				existing.Chapter, existing.Quality = entry.Chapter, input.Quality
				existing.PreferredRoute = input.PreferredRoute
				existing.PreferredRouteKey = routeKey
				existing.State, existing.Error, existing.Archived = "queued", "", false
			}
			continue
		}
		if tasks >= 5000 {
			rollback()
			return 0, errors.New("下载记录已满，请清理不再需要的任务")
		}
		manager.jobs[id] = &nativeDownloadRecord{nativeDownloadJob: nativeDownloadJob{
			ID: id, Drama: input.Drama, Chapter: entry.Chapter, Index: entry.Index,
			Quality: input.Quality, PreferredRoute: input.PreferredRoute, PreferredRouteKey: routeKey, State: "queued", Created: time.Now().UnixMilli()}}
		manager.jobs[id].Folder = ".download-cache/" + id
		if manager.bySource {
			manager.jobs[id].Folder = ".download-cache/" + input.Drama.Source + "/" + id
		}
		added = append(added, id)
		tasks++
	}
	if err := manager.saveLocked(); err != nil {
		rollback()
		return 0, err
	}
	manager.scheduleLocked()
	return len(added) + repaired, nil
}

// control 执行指定下载任务的控制动作。
func (manager *nativeDownloads) control(id, action string) error {
	return manager.controlExpected(id, action, "")
}

// controlExpected 校验任务预期版本后执行单条下载控制。
func (manager *nativeDownloads) controlExpected(id, action, version string) error {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	if manager.loadErr != nil {
		return manager.loadErr
	}
	if manager.closed || manager.moving || manager.updateHandoff {
		return errors.New("下载目录正在迁移、正在切换软件版本或队列已关闭，请稍后重试")
	}
	if action == "remove" && manager.mediaBusy {
		return errors.New("请等待合并或导出完成后再删除分集")
	}
	if version != "" {
		job := manager.jobs[id]
		if job == nil {
			return nil
		}
		if action != "remove" || job.State != "completed" || nativeDownloadVersion(job.nativeDownloadJob) != version {
			return errors.New("原分集已改变，已保留文件")
		}
	}
	previous := map[string]nativeDownloadRecord{}
	cancelIDs := []string{}
	remember := func(job *nativeDownloadRecord) { previous[job.ID] = *job }
	switch action {
	case "pauseAll", "resumeAll":
		for _, job := range manager.jobs {
			if !nativeDownloadAvailable(job.nativeDownloadJob) {
				continue
			}
			if action == "pauseAll" && (job.State == "queued" || job.State == "downloading") {
				remember(job)
				job.State = "paused"
				cancelIDs = append(cancelIDs, job.ID)
			}
			if action == "resumeAll" && (job.State == "paused" || job.State == "failed") {
				remember(job)
				job.State, job.Error, job.Archived = "queued", "", false
			}
		}
	case "pause", "resume", "remove", "archive", "restore":
		job := manager.jobs[id]
		if job == nil {
			return errors.New("下载任务不存在，请刷新列表")
		}
		if !nativeDownloadAvailable(job.nativeDownloadJob) {
			return errNativeBuildSource
		}
		remember(job)
		switch action {
		case "archive", "restore":
			if job.State != "completed" {
				return errors.New("只能清理已完成的下载任务，未完成的分集请先暂停或取消")
			}
			if action == "restore" && job.Archived && manager.unarchivedCountLocked() >= 5000 {
				return errors.New("下载任务已满，请先清理其他已完成任务；视频仍保留")
			}
			job.Archived = action == "archive"
		case "pause":
			if job.State == "queued" || job.State == "downloading" {
				job.State = "paused"
				cancelIDs = append(cancelIDs, id)
			}
		case "resume":
			if job.State == "paused" || job.State == "failed" {
				job.State, job.Error, job.Archived = "queued", "", false
			}
		case "remove":
			job.State, job.Error = "removing", ""
			cancelIDs = append(cancelIDs, id)
		}
	default:
		return errors.New("不支持的下载操作")
	}
	if err := manager.saveLocked(); err != nil {
		for key, value := range previous {
			*manager.jobs[key] = value
		}
		return err
	}
	for _, key := range cancelIDs {
		if cancel := manager.active[key]; cancel != nil {
			cancel()
		}
	}
	if action == "remove" && manager.active[id] == nil {
		job := manager.jobs[id]
		if err := manager.removeFiles(*job); err != nil {
			job.Error = "文件正在使用或无法删除，请退出播放后重试"
			_ = manager.saveLocked()
			return errors.New(job.Error)
		}
		delete(manager.jobs, id)
		if err := manager.saveLocked(); err != nil {
			job.Error = "文件已删除，记录尚未清理，请重试"
			manager.jobs[id] = job
			return err
		}
	}
	manager.scheduleLocked()
	return nil
}

// scheduleLocked 调度下载任务管理器，供内部持锁路径调用。
func (manager *nativeDownloads) scheduleLocked() {
	if manager.updateHandoff {
		return
	}
	if nativeLicenseEnabled() && !nativeLicenseManager().Allowed() {
		return
	}
	manager.stopQueueTimerLocked()
	if manager.closed || manager.moving {
		return
	}
	for len(manager.active) < max(1, min(10, manager.concurrency)) {
		var next *nativeDownloadRecord
		var wake time.Time
		now := time.Now()
		activeSources := map[string]int{}
		for id := range manager.active {
			if record := manager.jobs[id]; record != nil {
				activeSources[sourceFromDramaID(record.Drama.ID)]++
			}
		}
		for _, candidate := range manager.jobs {
			if candidate.State != "queued" || manager.active[candidate.ID] != nil || !nativeDownloadAvailable(candidate.nativeDownloadJob) {
				continue
			}
			source := sourceFromDramaID(candidate.Drama.ID)
			if activeSources[source] >= max(1, min(10, manager.sourceConcurrency)) {
				continue
			}
			ready := manager.sourceReadyLocked(source)
			if ready.After(now) {
				if wake.IsZero() || ready.Before(wake) {
					wake = ready
				}
				continue
			}
			if next == nil || candidate.Created < next.Created ||
				(candidate.Created == next.Created && candidate.Index < next.Index) ||
				(candidate.Created == next.Created && candidate.Index == next.Index && candidate.ID < next.ID) {
				next = candidate
			}
		}
		if next == nil {
			manager.wakeQueueLocked(wake)
			return
		}
		ctx, cancel := context.WithCancel(context.Background())
		ctx = context.WithValue(ctx, nativeDownloadSourceKey{}, sourceFromDramaID(next.Drama.ID))
		ctx = context.WithValue(ctx, nativeDownloadManagerKey{}, manager)
		next.State, next.Error = "downloading", ""
		manager.active[next.ID] = cancel
		if err := manager.saveLocked(); err != nil {
			next.State, next.Error = "failed", err.Error()
			delete(manager.active, next.ID)
			cancel()
			return
		}
		copy := next.nativeDownloadJob
		manager.delaySourceLocked(sourceFromDramaID(next.Drama.ID))
		manager.workers.Add(1)
		go manager.run(ctx, copy)
	}
}

// progress 更新下载任务的字节数与进度。
func (manager *nativeDownloads) progress(id string, downloaded, total int64, progress float64) {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	job := manager.jobs[id]
	if job == nil || job.State != "downloading" {
		return
	}
	job.Bytes, job.Total, job.Progress = downloaded, total, max(0, min(progress, 1))
	if time.Since(manager.lastSaved) >= time.Second {
		_ = manager.saveLocked()
	}
}

// run 执行一项下载任务，并维护进度、完成或失败状态。
func (manager *nativeDownloads) run(ctx context.Context, job nativeDownloadJob) {
	defer manager.workers.Done()
	result, err := manager.transfer(ctx, job)
	interrupted := ctx.Err() != nil
	manager.mu.Lock()
	defer manager.mu.Unlock()
	if cancel := manager.active[job.ID]; cancel != nil {
		cancel()
	}
	delete(manager.active, job.ID)
	source := sourceFromDramaID(job.Drama.ID)
	manager.delaySourceLocked(source)
	var backoff *requestBackoff
	if errors.As(err, &backoff) {
		manager.recordSourceBackoffLocked(source, backoff)
	} else if !interrupted && source == sourceHongguo && errors.Is(err, errHongguoEmptyPrimaryPlayback) {
		manager.recordSourceBackoffLocked(source, &requestBackoff{host: "红果取流接口", status: http.StatusOK, until: time.Now().Add(time.Minute), reason: "取流响应为空，暂缓后续红果下载"})
		err = fmt.Errorf("%w；后续红果下载任务暂缓 1 分钟", err)
	}
	record := manager.jobs[job.ID]
	if record == nil {
		return
	}
	switch {
	case record.State == "removing":
		if removeErr := manager.removeFiles(*record); removeErr == nil {
			delete(manager.jobs, job.ID)
		} else {
			record.State, record.Error = "failed", "文件无法删除，请稍后重试"
		}
	case err == nil:
		record.State, record.Progress = "completed", 1
		record.File, record.Key, record.ActualQuality = result.file, result.key, result.quality
		if result.chapter != nil {
			record.Chapter = *result.chapter
		}
		record.Revision++
		record.Published, record.PublishedFile, record.PublishedBytes, record.PublishedSHA256 = false, "", 0, ""
		record.Error = ""
	case record.State == "downloading":
		record.State = "failed"
		if interrupted {
			record.Error = "下载已中断，点击重试可继续"
		} else {
			record.Error = publicError(err).Error()
		}
	}
	if saveErr := manager.saveLocked(); saveErr != nil && manager.jobs[job.ID] != nil {
		record.State, record.Error = "failed", saveErr.Error()
	}
	manager.scheduleLocked()
}

// transfer 按任务计划下载媒体，并返回下载成品信息。
func (manager *nativeDownloads) transfer(ctx context.Context, job nativeDownloadJob) (nativeDownloadResult, error) {
	if err := manager.downloadSourceBackoff(ctx); err != nil {
		return nativeDownloadResult{}, err
	}
	media, err := manager.resolve(ctx, job)
	if err != nil {
		return nativeDownloadResult{}, err
	}
	choices := nativePlaybackChoicesForSource(sourceFromDramaID(job.Drama.ID), media, job.Quality).media
	if len(choices) == 0 {
		return nativeDownloadResult{}, errors.New("该集没有可用的下载地址")
	}
	var result nativeDownloadResult
	choices = nativeDownloadRouteOrder(choices, job.PreferredRoute, true, job.PreferredRouteKey)
	for _, option := range choices {
		if err = ctx.Err(); err != nil {
			break
		}
		if err = manager.downloadSourceBackoff(ctx); err != nil {
			break
		}
		resolved, resolveErr := manager.engine.downloader.materializePlaybackMedia(ctx, option)
		err = resolveErr
		if err == nil {
			result, err = manager.transferMedia(ctx, job, resolved)
		}
		if err == nil {
			return result, nil
		}
		if nativeRouteMustStop(err) {
			break
		}
	}
	var temporary *nativeDownloadTransientFailure
	if sourceFromDramaID(job.Drama.ID) == sourceWuguo && ctx.Err() == nil && errors.As(err, &temporary) {
		if recovered, alternateErr := manager.tryWuguoDownloadLine(ctx, job); alternateErr == nil {
			return recovered, nil
		}
	}
	return nativeDownloadResult{}, err
}

// tryWuguoDownloadLine 尝试伍果下载Line。
func (manager *nativeDownloads) tryWuguoDownloadLine(ctx context.Context, job nativeDownloadJob) (nativeDownloadResult, error) {
	_, id, _ := splitProviderDramaID(job.Drama.ID)
	_, chapters, err := manager.engine.downloader.fetchDuanjuDetail(ctx, sourceWuguo, id)
	if err != nil {
		return nativeDownloadResult{}, err
	}
	attempts := 0
	var candidates []Chapter
	for _, chapter := range chapters {
		candidates = append(candidates, chapter)
		candidates = append(candidates, chapter.Alternatives...)
	}
	for _, chapter := range candidates {
		chapter.Alternatives = nil
		if chapter.VIP || chapter.EpisodeString(0) != strconv.Itoa(job.Index) || chapter.VideoURL == job.Chapter.VideoURL && chapter.PageURL == job.Chapter.PageURL {
			continue
		}
		if attempts >= 2 || ctx.Err() != nil {
			break
		}
		attempts++
		alternate := job
		alternate.Chapter = chapter
		media, resolveErr := manager.resolve(ctx, alternate)
		if resolveErr != nil {
			err = resolveErr
			if nativeRouteMustStop(err) {
				return nativeDownloadResult{}, err
			}
			continue
		}
		options := nativePlaybackChoices(media, job.Quality).media
		for _, option := range options[:min(3, len(options))] {
			var result nativeDownloadResult
			resolved, resolveErr := manager.engine.downloader.materializePlaybackMedia(ctx, option)
			err = resolveErr
			if err == nil {
				result, err = manager.transferMedia(ctx, alternate, resolved)
			}
			if nativeRouteMustStop(err) {
				return nativeDownloadResult{}, err
			}
			if err == nil {
				result.chapter = &chapter
				return result, nil
			}
		}
	}
	if err == nil {
		err = errors.New("没有其他可用的免费分集线路")
	}
	return nativeDownloadResult{}, err
}

// localPlan 为已下载分集构造本地播放计划。
func (manager *nativeDownloads) localPlan(drama string, index int) (nativePlan, bool, error) {
	if !nativeDramaAvailable(nativeDrama{ID: drama}) {
		return nativePlan{}, false, errNativeBuildSource
	}
	manager.mu.Lock()
	defer manager.mu.Unlock()
	record := manager.jobs[nativeDownloadID(drama, index)]
	if record == nil {
		return nativePlan{}, false, nil
	}
	if !nativeDownloadAvailable(record.nativeDownloadJob) {
		return nativePlan{}, false, errNativeBuildSource
	}
	if record.State != "completed" {
		if record.File != "" {
			return nativePlan{}, true, errNativeLocalFile
		}
		return nativePlan{}, false, nil
	}
	path := filepath.Join(manager.jobDirectory(record.nativeDownloadJob), record.File)
	if !manager.localFilesValid(*record) {
		record.State, record.Error = "failed", "本地文件缺失，请重新下载"
		record.Archived = false
		_ = manager.saveLocked()
		return nativePlan{}, true, errNativeLocalFile
	}
	if manager.publishedValid(*record) {
		path = filepath.Join(manager.root, filepath.FromSlash(record.PublishedFile))
		return nativePlan{URL: path, Local: true, Quality: record.ActualQuality,
			Qualities: []int{}, Headers: map[string]string{}, RouteCount: 1}, true, nil
	}
	return nativePlan{URL: path, Key: record.Key, Local: true, Quality: record.ActualQuality,
		Qualities: []int{}, Headers: map[string]string{}, RouteCount: 1}, true, nil
}

// close 停止下载管理器并释放其任务资源。
func (manager *nativeDownloads) close() {
	manager.mu.Lock()
	manager.closed = true
	manager.stopQueueTimerLocked()
	for id, cancel := range manager.active {
		if job := manager.jobs[id]; job != nil && job.State == "downloading" {
			job.State = "paused"
		}
		cancel()
	}
	_ = manager.saveLocked()
	manager.mu.Unlock()
	manager.workers.Wait()
}

// nativeDownloadError 下载原生错误。
func nativeDownloadError(status int) error {
	return fmt.Errorf("媒体服务器返回 HTTP %d，请稍后重试", status)
}
