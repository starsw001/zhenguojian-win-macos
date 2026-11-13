package core

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

type huangguoMirrorRecord struct {
	MediaReachable   bool       `json:"mediaReachable,omitempty"`
	CatalogCheckedAt *time.Time `json:"catalogCheckedAt,omitempty"`
	CatalogMessage   string     `json:"catalogMessage,omitempty"`
	Origin           string     `json:"origin"`
	Kind             string     `json:"kind"`
	AddedOn          string     `json:"addedOn"`
	AddedFrom        string     `json:"addedFrom"`
	State            string     `json:"state"`
	Message          string     `json:"message"`
	LastError        string     `json:"lastError,omitempty"`
	CheckedAt        time.Time  `json:"checkedAt"`
	RetryAt          time.Time  `json:"retryAt"`
	EffectiveOrigin  string     `json:"effectiveOrigin,omitempty"`
	DuplicateOf      string     `json:"duplicateOf,omitempty"`
	Poster           bool       `json:"poster"`
	PosterWidth      int        `json:"posterWidth"`
	PosterHeight     int        `json:"posterHeight"`
	PosterBytes      int64      `json:"posterBytes"`
	PosterMessage    string     `json:"posterMessage"`
	Browse           bool       `json:"browse"`
	Selection        bool       `json:"selection"`
	Playback         bool       `json:"playback"`
	CatalogCount     int        `json:"catalogCount"`
	EpisodeCount     int        `json:"episodeCount"`
	SampleID         string     `json:"sampleId,omitempty"`
	Width            int        `json:"width"`
	Height           int        `json:"height"`
	PositionMS       int64      `json:"positionMs"`
}

type huangguoPublicationStatus struct {
	URL       string    `json:"url"`
	State     string    `json:"state"`
	Message   string    `json:"message"`
	CheckedAt time.Time `json:"checkedAt"`
	Added     int       `json:"added"`
}

type huangguoMirrorDisk struct {
	Version      int                         `json:"version"`
	Mirrors      []huangguoMirrorRecord      `json:"mirrors"`
	Publications []huangguoPublicationStatus `json:"publications"`
}

type huangguoDecodeResult struct {
	Width      int   `json:"width"`
	Height     int   `json:"height"`
	PositionMS int64 `json:"positionMs"`
}

type huangguoPosterResult struct {
	Width   int
	Height  int
	Bytes   int64
	Message string
}

type huangguoMirrorRegistry struct {
	mu            sync.Mutex
	disk          huangguoMirrorDisk
	directory     string
	storageError  string
	loadFailed    bool
	refreshing    bool
	lastDiscovery time.Time
	lastRecovery  time.Time
	checkGate     chan struct{}
}

// normalizeHuangguoOrigin 规整黄果来源。
func normalizeHuangguoOrigin(raw string) string {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Scheme != "https" && u.Scheme != "http" || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Path != "" && u.Path != "/" {
		return ""
	}
	host := strings.ToLower(u.Hostname())
	if strings.Contains(host, ":") {
		host = "[" + host + "]"
	}
	if port := u.Port(); port != "" && !(u.Scheme == "https" && port == "443" || u.Scheme == "http" && port == "80") {
		host = net.JoinHostPort(u.Hostname(), port)
	}
	return u.Scheme + "://" + host
}

// publicHuangguoOrigin 构造公开黄果来源。
func publicHuangguoOrigin(raw string) string {
	origin := normalizeHuangguoOrigin(raw)
	u, err := url.Parse(origin)
	if err != nil || origin == "" || u.Scheme != "https" || !strings.Contains(u.Hostname(), ".") || strings.HasSuffix(u.Hostname(), ".local") || strings.HasSuffix(u.Hostname(), ".localhost") {
		return ""
	}
	if ip := net.ParseIP(u.Hostname()); ip != nil {
		return ""
	}
	return origin
}

// huangguoSource 判断黄果站源是否满足当前规则。
func huangguoSource(source string) bool {
	return source == sourceHuangguoAI || source == sourceHuangguoVideo
}

// huangguoMirrorStore 返回黄果镜像更新存储。
func (d *Downloader) huangguoMirrorStore() *huangguoMirrorRegistry {
	d.huangguoMirrorOnce.Do(func() {
		r := &huangguoMirrorRegistry{directory: d.cfg.dataDir, checkGate: make(chan struct{}, 1), disk: huangguoMirrorDisk{Version: 1}}
		d.huangguoMirrors = r
		if r.directory != "" {
			path := filepath.Join(r.directory, "huangguo-mirrors.json")
			if info, err := os.Stat(path); err == nil {
				var body []byte
				var readErr error
				if info.Size() <= 4<<20 {
					body, readErr = os.ReadFile(path)
				}
				if info.Size() > 4<<20 || readErr != nil || json.Unmarshal(body, &r.disk) != nil || r.disk.Version != 1 {
					r.loadFailed, r.storageError = true, "黄果备用记录无法读取，原文件保留，未覆盖"
					r.disk = huangguoMirrorDisk{Version: 1}
				}
			} else if !os.IsNotExist(err) {
				r.loadFailed, r.storageError = true, "黄果备用记录读取失败，原文件保留"
			}
		}
		old := r.disk.Mirrors
		r.disk.Mirrors = nil
		for _, record := range old {
			record.Origin = normalizeHuangguoOrigin(record.Origin)
			if record.Origin != "" && r.indexLocked(record.Origin) < 0 {
				if record.State == "checking" {
					record.State, record.Message = "pending", "上次检测已中断，地址和历史失败原因保留"
				}
				r.disk.Mirrors = append(r.disk.Mirrors, record)
			}
		}
		var seeds []huangguoMirrorRecord
		_ = json.Unmarshal([]byte(huangguoMirrorSeedJSON), &seeds)
		for _, seed := range seeds {
			index := r.appendLocked(seed.Origin, seed.AddedOn, seed.AddedFrom)
			if index >= 0 && seed.CheckedAt.After(r.disk.Mirrors[index].CheckedAt) {
				previous := r.disk.Mirrors[index]
				seed.AddedOn, seed.AddedFrom = previous.AddedOn, previous.AddedFrom
				if seed.LastError == "" {
					seed.LastError = previous.LastError
				}
				r.disk.Mirrors[index] = seed
			}
		}
		var candidates []huangguoMirrorRecord
		_ = json.Unmarshal([]byte(huangguoPublicationCandidateJSON), &candidates)
		for _, candidate := range candidates {
			r.appendLocked(candidate.Origin, candidate.AddedOn, candidate.AddedFrom)
		}
		for _, origin := range huangguoPendingBaseURLs {
			r.appendLocked(origin, "2026-11-13", "用户登记")
		}
		for _, origin := range []string{d.providerBaseURL(sourceHuangguoAI), d.providerBaseURL(sourceHuangguoVideo)} {
			r.appendLocked(origin, "2026-10-23", "原有主站或备用地址")
		}
		var initialPublications []huangguoPublicationStatus
		_ = json.Unmarshal([]byte(huangguoPublicationSeedJSON), &initialPublications)
		for _, address := range huangguoPublicationURLs {
			initial := huangguoPublicationStatus{URL: address, State: "pending", Message: "失效恢复时读取；当前未请求"}
			for _, seed := range initialPublications {
				if seed.URL == address {
					initial = seed
				}
			}
			found := false
			for index, p := range r.disk.Publications {
				if p.URL == address {
					found = true
					if initial.CheckedAt.After(p.CheckedAt) {
						r.disk.Publications[index] = initial
					}
				}
			}
			if !found {
				r.disk.Publications = append(r.disk.Publications, initial)
			}
		}
	})
	return d.huangguoMirrors
}

// indexLocked 返回索引，供内部持锁路径调用。
func (r *huangguoMirrorRegistry) indexLocked(origin string) int {
	for i := range r.disk.Mirrors {
		if r.disk.Mirrors[i].Origin == origin {
			return i
		}
	}
	return -1
}

// appendLocked 追加黄果镜像Registry，供内部持锁路径调用。
func (r *huangguoMirrorRegistry) appendLocked(raw, date, from string) int {
	origin := normalizeHuangguoOrigin(raw)
	if origin == "" {
		return -1
	}
	if i := r.indexLocked(origin); i >= 0 {
		return i
	}
	if len(r.disk.Mirrors) >= 2048 {
		return -1
	}
	if date == "" {
		date = time.Now().Format("2006-01-02")
	}
	r.disk.Mirrors = append(r.disk.Mirrors, huangguoMirrorRecord{Origin: origin, Kind: "unknown", AddedOn: date, AddedFrom: from, State: "pending", Message: "待核验浏览、选集和真实解码，不参与自动切换"})
	return len(r.disk.Mirrors) - 1
}

// saveLocked 保存黄果镜像Registry，供内部持锁路径调用。
func (r *huangguoMirrorRegistry) saveLocked() (err error) {
	if r.directory == "" {
		return nil
	}
	if r.loadFailed {
		return errors.New(r.storageError)
	}
	defer func() {
		if err != nil {
			r.storageError = "黄果备用记录保存失败，原文件保留：" + publicError(err).Error()
		} else {
			r.storageError = ""
		}
	}()
	r.foldDuplicatesLocked()
	body, err := json.MarshalIndent(r.disk, "", "  ")
	if err != nil || len(body) > 4<<20 {
		return errors.New("黄果备用记录超出保存上限，原文件保留")
	}
	file, err := os.CreateTemp(r.directory, ".huangguo-mirrors-*")
	if err != nil {
		return err
	}
	name := file.Name()
	defer os.Remove(name)
	if _, err = file.Write(body); err == nil {
		err = file.Sync()
	}
	closeErr := file.Close()
	if err == nil {
		err = closeErr
	}
	if err == nil {
		err = os.Rename(name, filepath.Join(r.directory, "huangguo-mirrors.json"))
	}
	return err
}

// snapshot 返回黄果镜像Registry的当前状态快照。
func (r *huangguoMirrorRegistry) snapshot() ([]huangguoMirrorRecord, []huangguoPublicationStatus, string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.foldDuplicatesLocked()
	return append([]huangguoMirrorRecord(nil), r.disk.Mirrors...), append([]huangguoPublicationStatus(nil), r.disk.Publications...), r.storageError
}

// foldDuplicatesLocked 处理黄果镜像Registry的foldDuplicates操作，供内部持锁路径调用。
func (r *huangguoMirrorRegistry) foldDuplicatesLocked() {
	canonical := map[string]int{}
	for index := range r.disk.Mirrors {
		entry := &r.disk.Mirrors[index]
		entry.DuplicateOf = ""
		if !huangguoSource(entry.Kind) || entry.EffectiveOrigin == "" {
			continue
		}
		key := entry.Kind + "\x00" + entry.EffectiveOrigin
		previous, found := canonical[key]
		if !found || entry.Origin == entry.EffectiveOrigin && r.disk.Mirrors[previous].Origin != entry.EffectiveOrigin {
			canonical[key] = index
		}
	}
	for index := range r.disk.Mirrors {
		entry := &r.disk.Mirrors[index]
		if !huangguoSource(entry.Kind) || entry.EffectiveOrigin == "" {
			continue
		}
		parent := canonical[entry.Kind+"\x00"+entry.EffectiveOrigin]
		if parent != index {
			entry.DuplicateOf = r.disk.Mirrors[parent].Origin
		}
	}
}

// huangguoKindForURL 返回黄果Kind对应URL。
func (d *Downloader) huangguoKindForURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	origin := providerMediaOrigin(u)
	r := d.huangguoMirrorStore()
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, entry := range r.disk.Mirrors {
		if huangguoSource(entry.Kind) && (entry.Origin == origin || entry.EffectiveOrigin == origin) {
			return entry.Kind
		}
	}
	return ""
}

// providerSourceForURL 根据地址判断其所属站源。
func (d *Downloader) providerSourceForURL(raw string) string {
	source := providerSourceForURL(raw)
	if source != "" && !huangguoSource(source) {
		return source
	}
	if kind := d.huangguoKindForURL(raw); kind != "" {
		return kind
	}
	if source != "" {
		return source
	}
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	origin := providerMediaOrigin(u)
	ai := origin == normalizeHuangguoOrigin(firstNonEmpty(d.cfg.HuangguoAIURL, huangguoAIBaseURL))
	video := origin == normalizeHuangguoOrigin(firstNonEmpty(d.cfg.HuangguoVideoURL, huangguoVideoBaseURL))
	if ai && !video {
		return sourceHuangguoAI
	}
	if video && !ai {
		return sourceHuangguoVideo
	}
	return ""
}

// usableHuangguoMirror 判断镜像记录是否满足当前候选筛选条件。
func usableHuangguoMirror(entry huangguoMirrorRecord, source string) bool {
	if source == sourceHuangguoVideo && entry.Kind == source && entry.State == "connected" && entry.DuplicateOf == "" {
		return entry.Browse && entry.Selection && entry.MediaReachable && entry.Poster && entry.PosterWidth > 0 && entry.PosterHeight > 0 && entry.PosterBytes > 0
	}
	return entry.Kind == source && entry.State == "ready" && entry.DuplicateOf == "" && entry.Poster && entry.PosterWidth > 0 && entry.PosterHeight > 0 && entry.PosterBytes > 0 && entry.Browse && entry.Selection && entry.Playback && entry.Width > 0 && entry.Height > 0 && entry.PositionMS >= 400
}

// huangguoCandidates 返回黄果候选列表。
func (d *Downloader) huangguoCandidates(source, raw string) []string {
	u, err := url.Parse(raw)
	if err != nil {
		return []string{raw}
	}
	entries, _, _ := d.huangguoMirrorStore().snapshot()
	d.providerMu.Lock()
	preferred := d.providerHosts[source]
	d.providerMu.Unlock()
	origins := []string{}
	anchor := firstNonEmpty(preferred, d.providerBaseURL(source))
	anchorDate := ""
	for _, entry := range entries {
		if usableHuangguoMirror(entry, source) && (entry.Origin == anchor || entry.EffectiveOrigin == anchor) {
			anchorDate = entry.AddedOn
		}
	}
	sort.SliceStable(entries, func(i, j int) bool {
		if entries[i].AddedOn != entries[j].AddedOn {
			return entries[i].AddedOn > entries[j].AddedOn
		}
		return entries[i].CheckedAt.After(entries[j].CheckedAt)
	})
	if anchorDate != "" {
		for _, entry := range entries {
			if usableHuangguoMirror(entry, source) && entry.AddedOn > anchorDate {
				origins = append(origins, entry.Origin)
			}
		}
	}
	for _, entry := range entries {
		if preferred != "" && usableHuangguoMirror(entry, source) && (entry.Origin == preferred || entry.EffectiveOrigin == preferred) {
			origins = append(origins, entry.Origin)
		}
	}
	origins = append(origins, d.providerBaseURL(source))
	for _, entry := range entries {
		if usableHuangguoMirror(entry, source) {
			origins = append(origins, entry.Origin)
		}
	}
	seen := map[string]bool{}
	result := []string{}
	for _, origin := range origins {
		route := origin
		for _, entry := range entries {
			if entry.Origin == origin && entry.EffectiveOrigin != "" {
				route = entry.EffectiveOrigin
				break
			}
		}
		candidate := rehostProviderURL(u, origin)
		if candidate != "" && !seen[route] {
			seen[route] = true
			result = append(result, candidate)
		}
	}
	return result
}

// markHuangguoFailure 记录镜像请求失败与对应的后续检查状态。
func (d *Downloader) markHuangguoFailure(raw string, err error) {
	u, parseErr := url.Parse(raw)
	if parseErr != nil {
		return
	}
	r := d.huangguoMirrorStore()
	r.mu.Lock()
	defer r.mu.Unlock()
	origin := providerMediaOrigin(u)
	index := r.indexLocked(origin)
	if index < 0 {
		return
	}
	entry := &r.disk.Mirrors[index]
	entry.State, entry.Message, entry.LastError = "failed", publicError(err).Error(), publicError(err).Error()
	entry.RetryAt = time.Now().Add(5 * time.Minute)
	_ = r.saveLocked()
}

// checkHuangguoMirrors 检查黄果镜像列表。
func (engine *nativeEngine) checkHuangguoMirrors(ctx context.Context, source string) error {
	if !huangguoSource(source) {
		return errors.New("当前站源不是黄果")
	}
	if source == sourceHuangguoVideo {
		ctx = context.WithValue(ctx, sourceConnectivityOnlyKey{}, true)
	}
	d := engine.downloader
	_, _ = d.discoverHuangguoMirrors(ctx, true)
	entries, _, _ := d.huangguoMirrorStore().snapshot()
	count := 0
	for index, entry := range entries {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if huangguoSource(entry.Kind) && entry.Kind != source {
			continue
		}
		engine.changeSourceRecord(source, func(record *nativeSourceRecord) {
			record.Stage, record.Completed, record.Total = "逐条检测黄果备用地址", index, len(entries)
		})
		result := d.checkHuangguoMirror(ctx, entry.Origin)
		if usableHuangguoMirror(result, source) {
			count++
		}
	}
	if count == 0 {
		return fmt.Errorf("未取得可用备用地址；全部地址和逐条失败原因已保留")
	}
	return nil
}
