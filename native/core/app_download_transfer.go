package core

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

type nativeDownloadAsset struct {
	address string
	name    string
	data    []byte
	key     bool
}

type nativeDownloadBundle struct {
	assets    []nativeDownloadAsset
	playlists map[string][]byte
	quality   int
}

type nativeDownloadFileState struct {
	URL       string `json:"url"`
	Validator string `json:"validator"`
	Total     int64  `json:"total"`
	Complete  bool   `json:"complete"`
}

// transferMedia 返回传输媒体。
func (manager *nativeDownloads) transferMedia(ctx context.Context, job nativeDownloadJob, media providerMedia) (nativeDownloadResult, error) {
	ctx = providerMediaRequestContext(ctx, media)
	source := sourceFromDramaID(job.Drama.ID)
	ctx = context.WithValue(ctx, nativeDownloadSourceKey{}, source)
	bundle := nativeDownloadBundle{quality: media.Quality, playlists: map[string][]byte{}}
	parsed, err := url.Parse(media.URL)
	if err != nil || !isProviderHTTPMediaURL(media.URL) {
		return nativeDownloadResult{}, errors.New("下载地址无效")
	}
	hls := media.Playlist != "" || strings.HasSuffix(strings.ToLower(parsed.Path), ".m3u8")
	entry := "media.mp4"
	if hls {
		bundle, err = manager.downloadBundle(ctx, media, job.Quality)
		if err != nil {
			return nativeDownloadResult{}, err
		}
		entry = "index.m3u8"
	} else {
		bundle.assets = []nativeDownloadAsset{{address: media.URL, name: entry}}
	}
	directory := manager.jobDirectory(job)
	mediaKey := hex.EncodeToString(media.CENCKey)
	identity := nativeDownloadMediaIdentity(entry, media.URL, mediaKey, bundle.quality)
	if hls {
		for _, asset := range bundle.assets {
			identity += "\x00" + asset.address + "\x00" + string(asset.data)
		}
		encoded, _ := json.Marshal(bundle.playlists)
		identity += "\x00" + string(encoded)
	}
	fingerprint := sha256.Sum256([]byte(identity))
	marker := filepath.Join(directory, ".bundle")
	previous, _ := os.ReadFile(marker)
	if len(previous) > 0 && string(previous) != hex.EncodeToString(fingerprint[:]) {
		if source == sourceHongguo && !hls && media.Direct && media.credentials == nil {
			target := filepath.Join(directory, entry)
			if old, offset := nativeHongguoRefreshedPartial(target, media.URL, previous, mediaKey, bundle.quality); offset > 0 {
				size, restart, err := manager.downloadFileAttempt(ctx, media.URL, media.Referer, target, false, offset, old, func(received, total int64) {
					manager.progress(job.ID, received, total, float64(received)/float64(total))
				})
				if !restart {
					if err != nil {
						return nativeDownloadResult{}, err
					}
					if err := nativeDownloadWrite(marker, []byte(hex.EncodeToString(fingerprint[:]))); err != nil {
						return nativeDownloadResult{}, err
					}
					manager.progress(job.ID, size, size, 1)
					return nativeDownloadResult{file: entry, key: mediaKey, quality: bundle.quality}, nil
				}
			}
		}
		relative, err := filepath.Rel(manager.root, directory)
		if err != nil || relative == "." || strings.HasPrefix(relative, "..") {
			return nativeDownloadResult{}, errors.New("下载原件路径无效，已停止更换线路")
		}
		backups := filepath.Join(manager.root, ".replaced-downloads")
		if info, err := os.Lstat(backups); err == nil && info.Mode()&os.ModeSymlink != 0 {
			return nativeDownloadResult{}, errors.New("下载原件备份目录不能是符号链接")
		}
		if err := os.MkdirAll(backups, 0700); err != nil {
			return nativeDownloadResult{}, errors.New("无法创建下载原件备份目录")
		}
		backup, err := os.MkdirTemp(backups, job.ID+"-")
		if err != nil {
			return nativeDownloadResult{}, errors.New("无法保留旧的下载文件，已停止更换线路")
		}
		if err := os.Rename(directory, filepath.Join(backup, "original")); err != nil {
			return nativeDownloadResult{}, errors.New("无法保留旧的下载文件，已停止更换线路")
		}
	}
	if err := os.MkdirAll(directory, 0700); err != nil {
		return nativeDownloadResult{}, errors.New("无法创建下载文件，请检查存储空间")
	}
	if err := nativeDownloadWrite(marker, []byte(hex.EncodeToString(fingerprint[:]))); err != nil {
		return nativeDownloadResult{}, err
	}
	completedBytes, err := manager.downloadAssets(ctx, job, media, bundle, directory, hls)
	if err != nil {
		return nativeDownloadResult{}, err
	}
	if err := ctx.Err(); err != nil {
		return nativeDownloadResult{}, err
	}
	for name, body := range bundle.playlists {
		if err := nativeDownloadWrite(filepath.Join(directory, name), body); err != nil {
			return nativeDownloadResult{}, err
		}
	}
	manager.progress(job.ID, completedBytes, completedBytes, 1)
	return nativeDownloadResult{file: entry, key: hex.EncodeToString(media.CENCKey), quality: bundle.quality}, nil
}

// downloadAssets 下载资源列表。
func (manager *nativeDownloads) downloadAssets(ctx context.Context, job nativeDownloadJob, media providerMedia, bundle nativeDownloadBundle, directory string, hls bool) (int64, error) {
	if len(bundle.assets) == 0 {
		return 0, errors.New("播放列表没有可下载的媒体分片")
	}
	workers := 1
	if hls {
		manager.mu.Lock()
		workers = max(1, min(4, manager.concurrency))
		manager.mu.Unlock()
	}
	workers = min(workers, len(bundle.assets))
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	queue := make(chan int, len(bundle.assets))
	for index := range bundle.assets {
		queue <- index
	}
	close(queue)
	failures := make(chan error, 1)
	var group sync.WaitGroup
	var progressMu sync.Mutex
	sizes := make([]int64, len(bundle.assets))
	fractions := make([]float64, len(bundle.assets))
	var bytes int64
	fractionSum := 0.0
	update := func(index int, received, total int64) {
		progressMu.Lock()
		defer progressMu.Unlock()
		bytes += received - sizes[index]
		sizes[index] = received
		fraction := 0.0
		if total > 0 {
			fraction = min(1, float64(received)/float64(total))
		}
		fractionSum += fraction - fractions[index]
		fractions[index] = fraction
		overallTotal := int64(0)
		if !hls {
			overallTotal = total
		}
		manager.progress(job.ID, bytes, overallTotal, fractionSum/float64(len(bundle.assets)))
	}
	for worker := 0; worker < workers; worker++ {
		group.Add(1)
		go func() {
			defer group.Done()
			for index := range queue {
				if ctx.Err() != nil {
					return
				}
				asset := bundle.assets[index]
				target := filepath.Join(directory, asset.name)
				var size int64
				var err error
				if asset.data != nil {
					err = nativeDownloadWrite(target, asset.data)
					size = int64(len(asset.data))
				} else {
					size, err = manager.downloadFile(ctx, asset.address, media.Referer, target, asset.key, func(received, total int64) { update(index, received, total) })
				}
				if err != nil {
					select {
					case failures <- err:
					default:
					}
					cancel()
					return
				}
				update(index, size, size)
			}
		}()
	}
	group.Wait()
	select {
	case err := <-failures:
		return bytes, err
	default:
	}
	return bytes, ctx.Err()
}

// nativeDownloadMediaIdentity 提取下载媒体的身份信息，用于判断分段数据能否继续复用。
func nativeDownloadMediaIdentity(entry, address, key string, quality int) string {
	return entry + "\x00" + address + "\x00" + key + "\x00" + strconv.Itoa(quality)
}

// nativeStrongDownloadETag 筛选可用于下载续传校验的强 ETag。
func nativeStrongDownloadETag(value string) bool {
	if len(value) < 2 || len(value) > 512 || value[0] != '"' || value[len(value)-1] != '"' {
		return false
	}
	for index := 1; index < len(value)-1; index++ {
		b := value[index]
		if b < 0x21 || b == '"' || b == 0x7f {
			return false
		}
	}
	return true
}

// nativeHongguoRefreshedPartial 判断红果刷新后的媒体是否可以继续使用已有下载片段。
func nativeHongguoRefreshedPartial(target, address string, previous []byte, key string, quality int) (nativeDownloadFileState, int64) {
	var old nativeDownloadFileState
	if dir, err := os.Lstat(filepath.Dir(target)); err != nil || !dir.IsDir() {
		return old, 0
	}
	if _, err := os.Lstat(target); !os.IsNotExist(err) {
		return old, 0
	}
	if info, err := os.Lstat(target + ".json"); err != nil || !info.Mode().IsRegular() || info.Size() > 32<<10 {
		return old, 0
	}
	data, err := os.ReadFile(target + ".json")
	if err != nil || json.Unmarshal(data, &old) != nil || old.Complete || old.Total <= 0 || !nativeStrongDownloadETag(old.Validator) {
		return old, 0
	}
	if old.URL == address {
		return old, 0
	}
	domains := []string{"qznovelvod.com", "fqnovelvod.com", "douyinvod.com"}
	for _, candidate := range []string{old.URL, address} {
		if _, err := hongguoMultiCheckedURL(candidate, domains); err != nil {
			return old, 0
		}
	}
	from, oldErr := url.Parse(old.URL)
	to, newErr := url.Parse(address)
	if oldErr != nil || newErr != nil || from.Host != to.Host || from.Fragment != "" || to.Fragment != "" ||
		from.EscapedPath() == "/" || from.EscapedPath() == "" || from.EscapedPath() != to.EscapedPath() || from.RawQuery == to.RawQuery {
		return old, 0
	}
	fingerprint := sha256.Sum256([]byte(nativeDownloadMediaIdentity("media.mp4", old.URL, key, quality)))
	if string(previous) != hex.EncodeToString(fingerprint[:]) {
		return old, 0
	}
	info, err := os.Lstat(target + ".part")
	if err != nil || !info.Mode().IsRegular() || info.Size() <= 0 || info.Size() >= old.Total {
		return old, 0
	}
	return old, info.Size()
}

// nativeDownloadValidator 选择下载续传所需的响应校验标识。
func nativeDownloadValidator(header http.Header) string {
	if value := header.Get("ETag"); value != "" && !strings.HasPrefix(value, "W/") {
		return value
	}
	if value := header.Get("Last-Modified"); value != "" {
		if _, err := http.ParseTime(value); err == nil {
			return value
		}
	}
	return ""
}

// nativeDownloadRange 判断下载响应的 Content-Range 是否匹配预期续传区间。
func nativeDownloadRange(value string) (int64, int64, int64, bool) {
	var start, end, total int64
	if count, err := fmt.Sscanf(value, "bytes %d-%d/%d", &start, &end, &total); err != nil || count != 3 {
		return 0, 0, 0, false
	}
	return start, end, total, start >= 0 && end >= start && total > end
}

type nativeDownloadTransientFailure struct{ cause error }

// Error 返回当前错误的可显示文本。
func (failure *nativeDownloadTransientFailure) Error() string { return failure.cause.Error() }

// Unwrap 返回底层错误，供 errors.Is 和 errors.As 继续匹配。
func (failure *nativeDownloadTransientFailure) Unwrap() error { return failure.cause }

// downloadFile 执行带恢复与重试处理的单文件下载。
func (manager *nativeDownloads) downloadFile(ctx context.Context, address, referer, target string, key bool, progress func(int64, int64)) (int64, error) {
	for retry := 0; ; retry++ {
		if err := ctx.Err(); err != nil {
			return 0, err
		}
		size, err := manager.downloadFileOnce(ctx, address, referer, target, key, progress)
		var temporary *nativeDownloadTransientFailure
		if err == nil || !errors.As(err, &temporary) || retry >= 2 {
			return size, err
		}
		timer := time.NewTimer(time.Duration(1<<retry) * 500 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return size, ctx.Err()
		case <-timer.C:
		}
	}
}

// downloadFileOnce 执行单次文件下载流程并维护续传信息。
func (manager *nativeDownloads) downloadFileOnce(ctx context.Context, address, referer, target string, key bool, progress func(int64, int64)) (int64, error) {
	var metadata nativeDownloadFileState
	if data, err := os.ReadFile(target + ".json"); err == nil {
		_ = json.Unmarshal(data, &metadata)
	}
	if metadata.URL != address {
		metadata = nativeDownloadFileState{}
	}
	if metadata.Complete {
		if info, err := os.Stat(target); err == nil && info.Size() == metadata.Total && info.Size() > 0 && (!key || info.Size() == 16) {
			progress(info.Size(), info.Size())
			return info.Size(), nil
		}
	}
	partial := target + ".part"
	offset := int64(0)
	if metadata.Validator != "" && !key {
		if info, err := os.Stat(partial); err == nil && info.Size() > 0 && (metadata.Total <= 0 || info.Size() <= metadata.Total) {
			offset = info.Size()
		}
	}
	for attempt := 0; attempt < 2; attempt++ {
		size, restart, err := manager.downloadFileAttempt(ctx, address, referer, target, key, offset, metadata, progress)
		if !restart {
			return size, err
		}
		offset, metadata = 0, nativeDownloadFileState{}
	}
	return 0, errors.New("服务器无法恢复下载，请重试")
}

// downloadFileAttempt 执行一次文件传输并校验响应与续传范围。
func (manager *nativeDownloads) downloadFileAttempt(ctx context.Context, address, referer, target string, key bool, offset int64, old nativeDownloadFileState, progress func(int64, int64)) (int64, bool, error) {
	if err := manager.downloadSourceBackoff(ctx); err != nil {
		return 0, false, err
	}
	if !isProviderHTTPMediaURL(address) {
		return 0, false, errors.New("媒体下载地址无效")
	}
	requestCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	inactivity := time.AfterFunc(30*time.Second, cancel)
	defer inactivity.Stop()
	request, err := http.NewRequestWithContext(requestCtx, http.MethodGet, address, nil)
	if err != nil {
		return 0, false, err
	}
	request.Header.Set("User-Agent", userAgent)
	request.Header.Set("Referer", referer)
	request.Header.Set("Accept-Encoding", "identity")
	if offset > 0 {
		request.Header.Set("Range", fmt.Sprintf("bytes=%d-", offset))
		request.Header.Set("If-Range", old.Validator)
	}
	renewed := offset > 0 && old.URL != address
	client := *manager.engine.downloader.client
	client.Timeout = 0
	if renewed {
		client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	}
	response, err := manager.engine.downloader.doMediaRequestWithClient(request, &client)
	if err != nil {
		var backoff *requestBackoff
		if errors.As(err, &backoff) {
			return 0, false, backoff
		}
		return 0, false, &nativeDownloadTransientFailure{errors.New("下载连接中断，请检查网络后继续")}
	}
	defer response.Body.Close()
	if backoff := manager.observeDownloadBackoff(ctx, response); backoff != nil {
		return 0, false, backoff
	}
	if nativeMediaAccessStopsSwitch(response, nil) || response.Header.Get("Retry-After") != "" {
		return 0, false, nativeMediaAccessStopError(response)
	}
	if renewed && response.StatusCode >= 300 && response.StatusCode < 400 {
		return 0, false, errors.New("新下载地址要求重定向，原续传文件已保留")
	}
	if offset > 0 && (response.StatusCode == http.StatusRequestedRangeNotSatisfiable || response.StatusCode == http.StatusPreconditionFailed) {
		return 0, true, nil
	}
	if response.StatusCode != http.StatusOK && response.StatusCode != http.StatusPartialContent {
		failure := nativeDownloadError(response.StatusCode)
		if response.Header.Get("Retry-After") == "" && (response.StatusCode == http.StatusRequestTimeout || response.StatusCode == http.StatusInternalServerError || response.StatusCode == http.StatusBadGateway || response.StatusCode == http.StatusServiceUnavailable || response.StatusCode == http.StatusGatewayTimeout) {
			failure = &nativeDownloadTransientFailure{failure}
		}
		return 0, false, failure
	}
	if encoding := response.Header.Get("Content-Encoding"); encoding != "" && encoding != "identity" {
		return 0, false, errors.New("服务器返回了压缩媒体响应，无法安全续传")
	}
	contentType := strings.ToLower(response.Header.Get("Content-Type"))
	if !key && (strings.Contains(contentType, "text/html") || strings.Contains(contentType, "application/json")) {
		body, _ := io.ReadAll(io.LimitReader(response.Body, 64<<10))
		if nativeMediaAccessStopsSwitch(response, body) {
			return 0, false, nativeMediaAccessStopError(response)
		}
		return 0, false, errors.New("站源返回了错误页面，请刷新下载地址后重试")
	}
	if renewed {
		kind, _, _ := strings.Cut(contentType, ";")
		if kind = strings.TrimSpace(kind); kind != "video/mp4" && kind != "application/octet-stream" {
			return 0, false, errors.New("新下载地址未返回可验证的 MP4，原续传文件已保留")
		}
		if response.StatusCode == http.StatusOK {
			return 0, true, nil
		}
		start, end, total, valid := nativeContentRange(response.Header.Get("Content-Range"))
		tags := response.Header.Values("ETag")
		if !valid || start != offset || total != old.Total || end != total-1 ||
			response.ContentLength != total-offset || len(tags) != 1 || tags[0] != old.Validator {
			return 0, true, nil
		}
		if info, err := os.Lstat(target + ".part"); err != nil || !info.Mode().IsRegular() || info.Size() != offset {
			return 0, false, errors.New("原续传文件已变化，已停止追加")
		}
	}
	if response.StatusCode == http.StatusOK {
		offset = 0
	}
	total := response.ContentLength
	validator := nativeDownloadValidator(response.Header)
	if response.StatusCode == http.StatusPartialContent {
		start, end, full, valid := nativeDownloadRange(response.Header.Get("Content-Range"))
		if !valid || start != offset || (response.ContentLength >= 0 && response.ContentLength != end-start+1) {
			return 0, false, errors.New("服务器返回的续传范围不正确，已停止下载")
		}
		if offset > 0 && ((validator != "" && validator != old.Validator) || (old.Total > 0 && old.Total != full)) {
			return 0, true, nil
		}
		total = full
	}
	if key && (total > 64 || offset > 0) {
		return 0, false, errors.New("视频密钥格式无效")
	}
	metadata := nativeDownloadFileState{URL: address, Validator: validator, Total: total}
	if renewed {
		metadata.URL = old.URL
	}
	encoded, _ := json.Marshal(metadata)
	if err := nativeDownloadWrite(target+".json", encoded); err != nil {
		return 0, false, err
	}
	flags := os.O_CREATE | os.O_WRONLY
	if offset == 0 {
		flags |= os.O_TRUNC
	} else {
		flags |= os.O_APPEND
	}
	file, err := os.OpenFile(target+".part", flags, 0600)
	if err != nil {
		return 0, false, errors.New("无法写入下载文件，请检查存储空间")
	}
	received := offset
	buffer := make([]byte, 64<<10)
	for {
		n, readErr := response.Body.Read(buffer)
		if n > 0 {
			if renewed && int64(n) > total-received {
				err = errors.New("服务器返回了超出续传范围的数据，已停止追加")
				break
			}
			if key && received+int64(n) > 64 {
				err = errors.New("视频密钥过大")
				break
			}
			written, writeErr := file.Write(buffer[:n])
			received += int64(written)
			if writeErr != nil || written != n {
				err = errors.New("写入下载文件失败，请检查剩余空间")
				break
			}
			inactivity.Reset(30 * time.Second)
			progress(received, total)
		}
		if readErr != nil {
			if readErr != io.EOF {
				err = &nativeDownloadTransientFailure{errors.New("媒体文件未接收完整，点击继续下载")}
			}
			break
		}
	}
	if err == nil {
		err = requestCtx.Err()
		if err != nil && ctx.Err() == nil {
			err = &nativeDownloadTransientFailure{errors.New("媒体读取超时，点击继续下载")}
		}
	}
	if err == nil && (received == 0 || (total >= 0 && received != total)) {
		err = &nativeDownloadTransientFailure{errors.New("媒体文件未接收完整，点击继续下载")}
	}
	if err == nil && key && received != 16 {
		err = errors.New("视频密钥长度无效")
	}
	if err == nil {
		err = file.Sync()
	}
	closeErr := file.Close()
	if err != nil {
		return received, false, err
	}
	if closeErr != nil {
		return received, false, closeErr
	}
	if err = os.Rename(target+".part", target); err != nil {
		return received, false, err
	}
	metadata.URL, metadata.Complete, metadata.Total = address, true, received
	encoded, _ = json.Marshal(metadata)
	if err = nativeDownloadWrite(target+".json", encoded); err != nil {
		return received, false, err
	}
	return received, false, nil
}

// downloadPlaylist 下载并整理播放清单及其依赖资源。
func (manager *nativeDownloads) downloadPlaylist(ctx context.Context, address, referer string) (string, string, error) {
	return manager.engine.downloader.fetchMediaPlaylist(ctx, address, referer)
}
