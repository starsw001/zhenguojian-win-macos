package core

import (
	"context"
	"crypto/tls"
	"duanjuapp/native/portable"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

type Config struct {
	dataDir          string
	MaxPagesPerSort  int
	PageSize         int
	Retries          int
	InsecureTLS      bool
	HuangguoAIURL    string
	HuangguoVideoURL string
	HuangdouURL      string
	HongguoURL       string
	HuangjuURL       string
	HuangjuAPIURL    string
	YeguoURL         string
	YeguoAPIURL      string
	DSDURL           string
	SoraniURL        string
	SoraniAPIURL     string
	GuipianURL       string
	HanxiaoquanURL   string
	Token            string
	AESKeyHex        string
	InterfaceKey     string
	ParamKey         string
	ParamIV          string
	APIBase          string
	CDNURL           string
}

type Downloader struct {
	cachePolicy             atomic.Pointer[nativeCacheLimits]
	rankings                rankingCache
	cfg                     Config
	client                  *http.Client
	huangdouDetails         map[string]huangdouDetailEntry
	huangdouDetailPending   map[string]*huangdouDetailCall
	providerMu              sync.Mutex
	providerHosts           map[string]string
	huangguoCatalogOrigin   string
	huangguoCatalogSections []nativeCategory
	limiter                 *requestLimiter
	proxyRouter             *proxyRouter
	hongguoMediaCache       hongguoMediaCache
	hongguoOnce             sync.Once
	hongguo                 *hongguoAppClient
	huangjuOnce             sync.Once
	huangju                 *huangjuAPIClient
	yeguoOnce               sync.Once
	yeguo                   *yeguoAPIClient
	soraniOnce              sync.Once
	sorani                  *soraniAPIClient
	dsdCatalog              dsdCatalogState
	diagnostics             *diagnosticLog
	apiMu                   sync.Mutex
	apiBase                 string
	apiFailures             map[string]time.Time
	legacyOnce              sync.Once
	legacy                  *legacyAPIClient
	previewMu               sync.Mutex
	previewSessions         map[string]*huangguoPreviewSession
	huangguoMirrorOnce      sync.Once
	huangguoMirrors         *huangguoMirrorRegistry
	huangguoDecode          func(context.Context, providerMedia) (huangguoDecodeResult, error)
	huangguoPoster          func(context.Context, Drama, string) (huangguoPosterResult, error)
}

// defaultConfig 构造默认配置。
func defaultConfig() Config { return Config{MaxPagesPerSort: 50, PageSize: 30, Retries: 2} }

type nativeDrama struct {
	EpisodeCountVerified bool     `json:"episodeCountVerified,omitempty"`
	MetadataSchema       int      `json:"metadataSchema"`
	ID                   string   `json:"id"`
	Source               string   `json:"source"`
	SourceID             string   `json:"sourceId"`
	Title                string   `json:"title"`
	Description          string   `json:"description"`
	Cover                string   `json:"cover"`
	Episodes             int      `json:"episodes"`
	Category             string   `json:"category"`
	VIP                  *bool    `json:"vip"`
	Heat                 string   `json:"heat,omitempty"`
	Views                string   `json:"views,omitempty"`
	OnlineDate           string   `json:"onlineDate,omitempty"`
	Tags                 []string `json:"tags,omitempty"`
	ReleaseStatus        string   `json:"releaseStatus,omitempty"`
}

type nativeInput struct {
	Username                string                  `json:"username"`
	Password                string                  `json:"password"`
	LicenseKey              string                  `json:"licenseKey"`
	CheckOnly               bool                    `json:"checkOnly"`
	PageOnly                bool                    `json:"pageOnly"`
	RebuildOrdinaryCatalogs bool                    `json:"rebuildOrdinaryCatalogs"`
	OnlineOnly              bool                    `json:"onlineOnly"`
	RefreshPublications     bool                    `json:"refreshPublications"`
	RefreshAICatalog        bool                    `json:"refreshAICatalog"`
	StopStartupCatalog      bool                    `json:"stopStartupCatalog"`
	PublishedFile           string                  `json:"publishedFile"`
	PublishedBytes          int64                   `json:"publishedBytes"`
	PublishedSHA256         string                  `json:"publishedSHA256"`
	Health                  *nativeSourceHealth     `json:"health"`
	LAN                     json.RawMessage         `json:"lan"`
	ExpectedVersions        map[string]string       `json:"expectedVersions"`
	SystemProxy             nativeSystemProxy       `json:"systemProxy"`
	Settings                nativeResourceSettings  `json:"settings"`
	JobIDs                  []string                `json:"jobIds"`
	PlaybackSession         string                  `json:"playbackSession"`
	StartMS                 int64                   `json:"startMs"`
	DurationMS              int64                   `json:"durationMs"`
	Board                   string                  `json:"board"`
	Entries                 []nativeDownloadEpisode `json:"entries"`
	JobID                   string                  `json:"jobId"`
	Command                 string                  `json:"command"`
	Action                  string                  `json:"action"`
	Directory               string                  `json:"directory"`
	DownloadDirectory       string                  `json:"downloadDirectory"`
	DefaultLogDirectory     string                  `json:"defaultLogDirectory"`
	Source                  string                  `json:"source"`
	Page                    int                     `json:"page"`
	Query                   string                  `json:"query"`
	Category                string                  `json:"category"`
	Drama                   nativeDrama             `json:"drama"`
	Chapter                 Chapter                 `json:"chapter"`
	Index                   int                     `json:"index"`
	Quality                 int                     `json:"quality"`
	RouteIndex              int                     `json:"routeIndex"`
	PreferredRoute          int                     `json:"preferredRoute"`
	Session                 string                  `json:"session"`
	Sequence                int64                   `json:"sequence"`
	Force                   bool                    `json:"force"`
}

type nativeCatalogResult struct {
	Pagination    *nativeCatalogPagination `json:"pagination,omitempty"`
	NextPage      int                      `json:"nextPage,omitempty"`
	SearchState   string                   `json:"searchState,omitempty"`
	Items         []nativeDrama            `json:"items"`
	HasMore       bool                     `json:"hasMore"`
	Page          int                      `json:"page"`
	Warning       string                   `json:"warning,omitempty"`
	LocalSearch   bool                     `json:"localSearch"`
	Fresh         bool                     `json:"fresh"`
	Authoritative bool                     `json:"authoritative,omitempty"`
	hongguo       *hongguoCatalogState
	saveError     error
}

type nativePlan struct {
	Routes          []nativeRouteStatus `json:"routes,omitempty"`
	ExpiresAt       int64               `json:"expiresAt,omitempty"`
	PrefetchedBytes int64               `json:"prefetchedBytes,omitempty"`
	DanmakuID       string              `json:"danmakuId,omitempty"`
	Local           bool                `json:"local"`
	URL             string              `json:"url"`
	Headers         map[string]string   `json:"headers"`
	Key             string              `json:"decryptionKey,omitempty"`
	Quality         int                 `json:"quality"`
	Qualities       []int               `json:"qualities"`
	Session         string              `json:"session,omitempty"`
	RouteIndex      int                 `json:"routeIndex"`
	RouteCount      int                 `json:"routeCount"`
}

type nativeEngine struct {
	updateClientOnce         sync.Once
	publicUpdateClient       *http.Client
	updateHandoff            bool
	ordinaryCachesReset      bool
	catalogEpochs            map[string]string
	huangguoFreshMetadata    map[string]nativeDrama
	lanMu                    sync.Mutex
	lan                      *nativeLANServer
	settingsMu               sync.Mutex
	settings                 nativeResourceSettings
	readMu                   sync.Mutex
	reads                    map[string]nativeReadRequest
	readCount                int
	work                     map[string]bool
	downloads                *nativeDownloads
	downloader               *Downloader
	directory                string
	defaultDownloadDirectory string
	defaultLogDirectory      string
	mu                       sync.Mutex
	catalogs                 map[string][]nativeDrama
	catalogStates            map[string]nativeCatalogState
	categoryOptions          map[string][]nativeCategory
	hongguoCatalog           *hongguoCatalogState
	recommendations          map[string]nativeRecommendationState
	catalogSaveError         error
	sourceSaveError          error
	covers                   *nativeCoverCache
	stream                   *nativeStreamServer
	playbacks                map[string]nativePlaybackChoice
	playbackMu               sync.Mutex
	playbackSequence         int64
	playbackCancel           context.CancelFunc
	sourceTasks              map[string]*nativeSourceTask
	sourceRecords            map[string]nativeSourceRecord
	sourceCatalogMu          sync.Mutex
	sourceCatalogs           map[string]chan struct{}
	startupAIRequested       bool
	startupAITask            *nativeSourceTask
	startupAICancel          context.CancelFunc
}

var nativeState struct {
	sync.Mutex
	engine *nativeEngine
}

// nativeText 将通用值转换为原生请求使用的字符串。
func nativeText(value any) string {
	switch v := value.(type) {
	case string:
		return strings.TrimSpace(v)
	case map[string]any:
		for _, key := range []string{"url", "src", "cover", "image", "pic"} {
			if text := nativeText(v[key]); text != "" {
				return text
			}
		}
	case []any:
		for _, entry := range v {
			if text := nativeText(entry); text != "" {
				return text
			}
		}
	case json.Number:
		return string(v)
	case float64:
		return strconv.Itoa(int(v))
	case int:
		return strconv.Itoa(v)
	}
	return ""
}

// nativeNormalize 规整原生请求输入中的字段与默认值。
func nativeNormalize(drama Drama) nativeDrama {
	cover := ""
	for _, value := range []any{drama.Cover, drama.CoverURL, drama.CoverURLSnake, drama.Image, drama.ImageURL, drama.ImageURLSnake, drama.Img, drama.Pic, drama.Picture, drama.Poster, drama.Thumb, drama.Thumbnail} {
		candidate := nativeText(value)
		if strings.HasPrefix(candidate, "//") {
			candidate = "https:" + candidate
		}
		if isProviderHTTPMediaURL(candidate) {
			cover = candidate
			break
		}
	}
	episodes := 0
	for _, value := range []any{drama.TotalEpisode, drama.TotalEpisodeSnake, drama.EpisodeCount, drama.EpisodeCountSnake, drama.ChapterCount, drama.ChapterCountSnake, drama.Total, drama.Episodes} {
		if count, _ := strconv.Atoi(nativeText(value)); count > episodes {
			episodes = count
		}
	}
	source, sourceID, _ := splitProviderDramaID(drama.ID)
	return nativeDrama{MetadataSchema: 1, ID: drama.ID, Source: source, SourceID: sourceID, Title: drama.DisplayTitle(),
		Description: firstNonEmpty(drama.Desc, drama.Intro), Cover: cover, Episodes: episodes,
		Category: nativeDramaCategory(drama), VIP: drama.VIP,
		Heat: drama.Heat, Views: drama.Views, OnlineDate: providerReleaseDate(drama.OnlineDate),
		Tags: append([]string(nil), drama.Tags...), ReleaseStatus: drama.ReleaseStatus}
}

// nativeDramaCategory 返回原生作品分类。
func nativeDramaCategory(drama Drama) string {
	category := firstNonEmpty(drama.CategoryName, drama.CategoryNameSnake, drama.TypeName, drama.TypeNameSnake, drama.SortName, drama.SortNameSnake, drama.Category)
	if category != "" {
		return category
	}
	switch drama.ChannelName {
	case "真人剧", "短剧", "漫剧", "AI 剧", "AI剧", "AI 短剧", "AI 漫剧", "AI 换脸", "AI 魔改":
		return drama.ChannelName
	}
	return ""
}

// newNativeEngine 创建原生引擎。
func newNativeEngine(directory string, defaultDownloads ...string) (*nativeEngine, error) {
	if !filepath.IsAbs(directory) {
		return nil, errors.New("应用数据目录无效")
	}
	if err := portable.EnsureDirectory(directory); err != nil {
		return nil, err
	}
	cfg := defaultConfig()
	cfg.dataDir = directory
	router := &proxyRouter{}
	d := &Downloader{cfg: cfg, providerHosts: map[string]string{}, proxyRouter: router,
		limiter: newRequestLimiter(3, 250*time.Millisecond), diagnostics: newDiagnosticLog(directory)}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.TLSClientConfig = &tls.Config{MinVersion: tls.VersionTLS12}
	transport.MaxIdleConnsPerHost = 8
	transport.ResponseHeaderTimeout = 20 * time.Second
	transport.Proxy = router.proxy
	cdn := newCDNTransport(transport, newDNSResolver(transport))
	d.client = &http.Client{Transport: newAutoProxyFallbackTransport(newHuangguoBrowserTransport(cdn, d), router, d.recordDiagnostic), Timeout: 45 * time.Second,
		CheckRedirect: func(request *http.Request, via []*http.Request) error {
			if len(via) >= 10 {
				return errors.New("站源重定向次数过多")
			}
			if len(via) > 0 && !strings.EqualFold(request.URL.Host, via[0].URL.Host) {
				request.Header.Del("X-Preview-Token")
			}
			return nil
		}}
	engine := &nativeEngine{downloader: d, directory: directory, catalogs: map[string][]nativeDrama{}, catalogStates: map[string]nativeCatalogState{}}
	engine.defaultLogDirectory = filepath.Join(directory, "logs")
	if len(defaultDownloads) > 1 && defaultDownloads[1] != "" {
		if !filepath.IsAbs(defaultDownloads[1]) {
			return nil, errors.New("默认日志目录必须是绝对路径")
		}
		engine.defaultLogDirectory = filepath.Clean(defaultDownloads[1])
	}
	if len(defaultDownloads) > 0 && defaultDownloads[0] != "" {
		if !filepath.IsAbs(defaultDownloads[0]) {
			return nil, errors.New("默认下载目录必须是绝对路径")
		}
		engine.defaultDownloadDirectory = filepath.Clean(defaultDownloads[0])
	}
	engine.loadResourceSettings()
	d.loadRankingCache()
	engine.loadCatalogCache()
	engine.loadSourceRecords()
	if origin := publicHuangguoOrigin(engine.sourceRecords[sourceHuangguoAI].CatalogOrigin); origin != "" {
		d.huangguoCatalogOrigin = origin
	}
	engine.covers = newNativeCoverCache(directory, d)
	engine.downloads = newNativeDownloads(engine)
	d.huangguoPoster = func(ctx context.Context, drama Drama, origin string) (huangguoPosterResult, error) {
		return engine.checkHuangguoPoster(ctx, drama, origin, decodeHuangguoMirrorPoster)
	}
	d.huangguoDecode = func(ctx context.Context, media providerMedia) (huangguoDecodeResult, error) {
		plan, err := engine.nativeOpenPlayback(ctx, nativePlaybackChoices(media, 480))
		if err != nil {
			return huangguoDecodeResult{}, err
		}
		defer engine.nativeReleasePlayback(plan.Session)
		return decodeHuangguoMirrorPlayback(ctx, plan.URL)
	}
	return engine, nil
}

// NativeRequest 将 JSON 请求分派到原生应用引擎，并序列化结果与错误信息。
func NativeRequest(raw string) (result string) {
	defer func() {
		if recover() != nil {
			result = `{"ok":false,"error":"本地核心处理失败，请重试"}`
		}
	}()
	var input nativeInput
	if len(raw) > 1<<20 || json.Unmarshal([]byte(raw), &input) != nil {
		return `{"ok":false,"error":"请求格式无效"}`
	}
	data, err := nativeDispatch(input)
	envelope := map[string]any{"ok": err == nil}
	if err != nil {
		envelope["error"] = publicError(err).Error()
		if errors.Is(err, errNativeLicense) {
			envelope["code"] = "license_required"
		}
		if errors.Is(err, errNativeLocalFile) {
			envelope["code"] = "local_media"
		}
		var backoff *requestBackoff
		if errors.As(err, &backoff) {
			envelope["code"] = "source_blocked"
			envelope["retryAt"] = backoff.until
		}
		source := canonicalProviderSource(input.Source)
		if source == "" {
			source = sourceFromDramaID(input.Drama.ID)
		}
		if !errors.Is(err, context.Canceled) && !errors.Is(err, errNativeLicense) && (input.Action == "catalog" || input.Action == "detail" || input.Action == "resolve") {
			nativeState.Lock()
			engine := nativeState.engine
			nativeState.Unlock()
			if engine != nil && nativeSourceAvailable(source) {
				engine.changeSourceRecord(source, func(record *nativeSourceRecord) {
					record.Error = publicError(err).Error()
					if backoff != nil {
						record.RetryAt = backoff.until
					}
					if !record.Running {
						record.Stage, record.FinishedAt = "访问失败", time.Now()
					}
				})
			}
		}
	} else {
		envelope["data"] = data
	}
	body, marshalErr := json.Marshal(envelope)
	if marshalErr != nil {
		return `{"ok":false,"error":"无法读取站源返回的数据"}`
	}
	return string(body)
}

// nativeDispatch 根据请求动作选择原生业务处理分支。
func nativeDispatch(input nativeInput) (any, error) {
	if strings.HasPrefix(input.Action, "license") {
		return nativeLicenseAction(input)
	}
	if err := nativeRequireLicense(input); err != nil {
		return nil, err
	}
	if err := nativeAuthorizeInput(input); err != nil {
		return nil, err
	}
	nativeState.Lock()
	if input.Action == "initialize" {
		if nativeState.engine == nil {
			engine, err := newNativeEngine(input.Directory, input.DownloadDirectory, input.DefaultLogDirectory)
			if err != nil {
				nativeState.Unlock()
				return nil, err
			}
			nativeState.engine = engine
		}
		engine := nativeState.engine
		nativeState.Unlock()
		if input.RebuildOrdinaryCatalogs {
			if _, err := engine.rebuildOrdinaryCatalogsWithEpoch(buildCatalogEpoch); err != nil {
				return nil, err
			}
			if _, err := engine.rebuildHuangguoIdentityCache(buildCatalogEpoch); err != nil {
				return nil, err
			}
		}
		if input.StopStartupCatalog {
			engine.stopHuangguoStartupCatalog()
		}
		if input.RefreshAICatalog && buildAllSources == "true" {
			engine.scheduleHuangguoStartupCatalog()
		} else if input.RefreshPublications && buildAllSources == "true" {
			engine.downloader.scheduleHuangguoDiscovery()
		}
		return map[string]any{"version": buildAppVersion, "standalone": true, "allSources": buildAllSources == "true", "catalogEpoch": buildCatalogEpoch, "cacheLimits": engine.resourceSettings().CacheLimits}, nil
	}
	engine := nativeState.engine
	nativeState.Unlock()
	if engine == nil {
		return nil, errors.New("应用核心尚未就绪，请重新打开应用")
	}
	duration := 60 * time.Second
	if input.Action == "moveDownloads" {
		duration = 10 * time.Minute
	} else if input.Action == "catalog" {
		duration = 90 * time.Second
	} else if input.Action == "downloadFolder" {
		duration = 3 * time.Second
	} else if input.Action == "danmaku" {
		duration = 10 * time.Second
	} else if input.Action == "preload" {
		duration = 15 * time.Second
	}
	ctx, cancel := context.WithTimeout(context.Background(), duration)
	defer cancel()
	if input.Action == "danmaku" || input.Action == "preload" || input.Action == "prepareHandoff" || input.Session != "" && (input.Action == "catalog" || input.Action == "categories" || input.Action == "suggestions" || input.Action == "recommendations" || input.Action == "metadata") {
		work, finish, err := engine.beginRead(ctx, input)
		if err != nil {
			return nil, err
		}
		defer finish()
		ctx = work
	}
	switch input.Action {
	case "lan":
		return engine.nativeLAN(ctx, input.Command, input.LAN)
	case "updateSystemProxy":
		return true, engine.updateSystemProxy(input.SystemProxy)
	case "resourceSettings":
		return engine.resourceSettings(), nil
	case "saveResourceSettings":
		return engine.saveResourceSettings(input.Settings)
	case "controlDownloadBatch":
		if input.Command == "remove" && input.Drama.ID != "" {
			return engine.removeDownloadCollection(ctx, input.Drama)
		}
		return engine.downloads.controlBatchExpected(ctx, input.JobIDs, input.Command, input.ExpectedVersions)
	case "preload":
		return engine.nativePreload(ctx, input)
	case "prepareHandoff":
		return engine.nativeResolve(ctx, input)
	case "danmaku":
		return engine.nativeDanmaku(ctx, input)
	case "updateBusy":
		return map[string]any{"busy": engine.activity(false) != nil}, nil
	case "updateCheck":
		return engine.updateCheck(ctx, input.Query)
	case "updateDownload":
		return engine.updateDownload(input.Query)
	case "updateStatus":
		return engine.updateStatusValue(), nil
	case "updateApply":
		return engine.updateApply()
	case "cancelRead":
		engine.cancelRead(input)
		return true, nil
	case "recommendations":
		return engine.nativeRecommendations(ctx, input)
	case "cachedRecommendations":
		return engine.cachedRecommendations(input.Category)
	case "rankingBoards":
		boards := []rankingBoard{}
		for _, board := range rankingBoards {
			if nativeSourceAvailable(board.Source) {
				boards = append(boards, board)
			}
		}
		return map[string]any{"items": boards}, nil
	case "rankings":
		return engine.nativeRanking(ctx, input)
	case "suggestions":
		items, err := engine.suggestions(ctx, input.Query)
		return map[string]any{"items": items}, err
	case "downloadDirectory":
		engine.downloads.mu.Lock()
		root := engine.downloads.root
		engine.downloads.mu.Unlock()
		return map[string]string{"directory": root}, nil
	case "storage":
		return engine.storage()
	case "moveDownloads":
		return true, engine.moveDownloads(ctx, input.Directory, input.Command == "exact")
	case "workLease":
		count, err := engine.workLease(input.JobID, input.Command)
		return map[string]int{"count": count}, err
	case "downloads":
		jobs, err := engine.downloads.snapshot()
		return map[string]any{"jobs": jobs}, err
	case "downloadFolder":
		directory, err := engine.downloads.downloadFolder(ctx, input.JobIDs)
		return map[string]string{"directory": directory}, err
	case "enqueueDownloads":
		added, err := engine.downloads.enqueueContext(ctx, input)
		return map[string]int{"added": added}, err
	case "controlDownloads":
		return true, engine.downloads.control(input.JobID, input.Command)
	case "publishDownload":
		return true, engine.downloads.publish(ctx, input)
	case "localPlayback":
		plan, found, err := engine.downloads.localPlan(input.Drama.ID, input.Index)
		if found || err != nil {
			engine.downloader.recordPlaybackResolution(input, true, err)
		}
		return plan, err
	case "catalog":
		return engine.nativeCatalog(ctx, input)
	case "cached":
		if !validNativeCategory(canonicalProviderSource(input.Source), input.Category) {
			return nil, errors.New("内容分类无效")
		}
		return engine.nativeCached(nativeCatalogKey(input.Source, input.Category)), nil
	case "cachedCategories":
		engine.mu.Lock()
		items := append([]nativeCategory{}, engine.categoryOptions[canonicalProviderSource(input.Source)]...)
		engine.mu.Unlock()
		return map[string]any{"items": items}, nil
	case "categories":
		items, err := engine.nativeCategories(ctx, input.Source, input.Force)
		return map[string]any{"items": items}, err
	case "sourceStatus":
		return engine.sourceStatus(input.Source), nil
	case "saveSourceHealth":
		return engine.saveSourceHealth(input.Source, input.Health)
	case "sourceJob":
		return engine.startSourceTask(input.Source, input.Command, input.Drama)
	case "cancelSourceJob":
		return engine.cancelSourceTask(input.Source), nil
	case "cover":
		return engine.loadCover(ctx, input.Drama, input.Force)
	case "prepareCover":
		return engine.prepareCover(ctx, input.Drama)
	case "detail":
		if input.CheckOnly && ordinarySource(input.Drama.Source) {
			ctx = context.WithValue(ctx, ordinaryDetailReadOnlyKey{}, true)
		}
		return engine.nativeDetail(ctx, input.Drama)
	case "checkEpisodeConnection":
		return engine.checkOrdinaryEpisodeConnection(ctx, input)
	case "metadata":
		return engine.nativeMetadata(ctx, input.Drama)
	case "checkPlaybackRoute":
		return engine.nativeCheckPlaybackRoute(ctx, input.Session, input.RouteIndex)
	case "resolve", "fallback", "selectPlaybackRoute":
		playback, finish, err := engine.nativeBeginPlayback(ctx, input.Sequence)
		if err != nil {
			return nil, err
		}
		defer finish()
		if input.Action == "selectPlaybackRoute" {
			return engine.nativeSelectPlaybackRoute(playback, input.Session, input.RouteIndex)
		}
		if input.Action == "fallback" {
			return engine.nativeNextPlayback(playback, input.Session)
		}
		return engine.nativeResolve(playback, input)
	case "cancelPlayback":
		engine.nativeCancelPlayback(input.Sequence)
		return true, nil
	case "release":
		engine.nativeReleasePlayback(input.Session)
		return true, nil
	default:
		return nil, errors.New("不支持的应用操作")
	}
}

// nativeCatalog 处理站源目录读取请求并汇总缓存与分页状态。
func (engine *nativeEngine) nativeCatalog(ctx context.Context, input nativeInput) (nativeCatalogResult, error) {
	source := canonicalProviderSource(input.Source)
	if input.PageOnly && ordinarySource(source) {
		ctx = context.WithValue(ctx, sourceRuleRefreshKey{}, true)
	}
	if !isHuangguoProviderSource(source) {
		return nativeCatalogResult{}, errors.New("请选择有效站源")
	}
	category := strings.TrimSpace(input.Category)
	if !validNativeCategory(source, category) {
		return nativeCatalogResult{}, errors.New("内容分类无效")
	}
	cacheKey := nativeCatalogKey(source, category)
	if err := ctx.Err(); err != nil {
		return nativeCatalogResult{}, err
	}
	page := max(1, input.Page)
	query := strings.TrimSpace(input.Query)
	if query == "" {
		if retried, err := engine.retryCatalogSave(); retried {
			cached := engine.nativeCached(cacheKey)
			refresh, _ := ctx.Value(sourceRuleRefreshKey{}).(bool)
			if !refresh && (err != nil || len(cached.Items) > 0) {
				return cached, nil
			}
		}
	}
	if query == "" && page == 1 && !input.Force {
		if cached := engine.nativeCached(cacheKey); cached.Fresh {
			return cached, nil
		}
	}
	d := engine.downloader
	result := nativeCatalogResult{Items: []nativeDrama{}, Page: page}
	if query != "" {
		if source == sourceHuangguoAI {
			return engine.searchHuangguoCatalog(query, page), nil
		}
		return engine.searchSourceCatalog(ctx, source, query, page, input.OnlineOnly)
	}
	if source != sourceHuangguoAI && input.Force {
		ctx = context.WithValue(ctx, providerTextNoCacheKey{}, true)
	}
	unlock, lockErr := engine.lockSourceCatalog(ctx, source)
	if lockErr != nil {
		return nativeCatalogResult{}, lockErr
	}
	defer unlock()
	if err := ctx.Err(); err != nil {
		return result, err
	}
	var items []Drama
	var err error
	cursor := &sourceCatalogCursor{}
	ctx = context.WithValue(ctx, sourceCatalogCursorKey{}, cursor)
	switch source {
	case sourceHongguo:
		if _, _, _, web := hongguoWebCategory(category); web {
			var response hongguoWebCatalogPage
			response, err = d.fetchHongguoWebCatalogPage(ctx, category, page)
			if err == nil {
				items = response.Items
				result.HasMore = response.Page < response.TotalPages
				cursor.Next = response.Page + 1
				sourceSetCatalogPagination(ctx, response.Page, response.TotalPages, "hongguo_web_category")
			}
			break
		}
		engine.mu.Lock()
		known := make(map[string]bool, len(engine.catalogs[cacheKey]))
		for _, drama := range engine.catalogs[cacheKey] {
			known[drama.ID] = true
		}
		engine.mu.Unlock()
		if refresh, _ := ctx.Value(sourceRuleRefreshKey{}).(bool); refresh {
			known = map[string]bool{}
		}
		ctx = context.WithValue(ctx, libraryKnownKey{}, known)
		if page > 1 {
			ctx = context.WithValue(ctx, libraryMoreKey{}, true)
		}
		client := d.hongguoClient()
		client.mu.Lock()
		blocked := time.Now().Before(client.categoryBlockedUntil)
		client.mu.Unlock()
		if blocked {
			// 分类接口熔断期内直接跳过，交给网页源回退
			items = nil
			err = fmt.Errorf("红果 App 分类接口暂不可用")
		} else {
			items, err = d.fetchHongguoAppCatalogCategory(ctx, category)
			if err != nil {
				client.mu.Lock()
				client.categoryBlockedUntil = time.Now().Add(5 * time.Minute)
				client.mu.Unlock()
			}
		}
		state := d.hongguoCatalogSnapshot()
		result.hongguo = state
		result.HasMore = hongguoCatalogHasMore(state)
		if category != "" && state != nil {
			result.HasMore = !state.Feeds["category:"+category].Exhausted
		}
		if len(items) == 0 && err != nil && ctx.Err() == nil && (category == "" || category == "short_play") {
			appErr := err
			var totalPages int
			var fallbackErr error
			for attempt := 0; attempt < 2; attempt++ {
				if attempt > 0 {
					select {
					case <-time.After(time.Second):
					case <-ctx.Done():
						return result, ctx.Err()
					}
				}
				items, totalPages, fallbackErr = d.fetchHongguoCategoryPage(ctx, "real-drama?page="+strconv.Itoa(page), "真人剧")
				if fallbackErr == nil {
					break
				}
			}
			if fallbackErr == nil {
				result.HasMore = page < totalPages
				err = nil
			} else {
				// 网页源失败时保留 App 接口的原始错误，并给出可读提示
				err = fmt.Errorf("红果接口暂时不可用（官方限制）：%v", appErr)
			}
		}
	case sourceHuangdou:
		client := newHuangdouAPIClient(d)
		var decoded any
		err = client.call(ctx, "/drama/list", map[string]any{"page": strconv.Itoa(page), "page_size": "30"}, &decoded)
		if err == nil {
			rows := huangdouList(decoded)
			for _, row := range rows {
				if drama := huangdouDramaFromMap(row); drama.ID != "" {
					items = append(items, drama)
				}
			}
			result.HasMore = len(rows) >= 30
		}
	case sourceHuangguoAI:
		items, result.HasMore, err = d.fetchHuangguoAICatalogPage(ctx, page, category)
	case sourceHuangju:
		items, result.HasMore, err = d.fetchHuangjuCatalogPage(ctx, page, category, "")
	case sourceYeguo:
		items, result.HasMore, err = d.fetchYeguoCatalogPage(ctx, page, category, "")
	case sourceDSD:
		items, result.HasMore, err = d.fetchDSDCatalogPage(ctx, page, category, "")
	case sourceSorani:
		items, result.HasMore, err = d.fetchSoraniCatalogPage(ctx, page, category, "")
	case sourceGuipian:
		items, result.HasMore, err = d.fetchGuipianCatalogPage(ctx, page, category, "")
	case sourceHanxiaoquan:
		items, result.HasMore, err = d.fetchHanxiaoquanCatalogPage(ctx, page, category, "")
	case sourceHuangguoVideo:
		address := fmt.Sprintf("%s/videos?page=%d", d.providerBaseURL(source), page)
		if category != "" {
			address += "&category=" + url.QueryEscape(category)
		}
		var body string
		body, err = d.fetchProviderText(ctx, address, d.providerBaseURL(source)+"/")
		if err == nil {
			items = parseHuangguoVideoCards(body, address)
		}
		result.HasMore = sourceHTMLHasMore(body, address, page)
	case sourceCloudFront:
		items, result.HasMore, err = d.fetchLegacyCatalogCategoryPage(ctx, page, category)
	default:
		if isDuanjuProviderSource(source) {
			items, result.HasMore, err = d.fetchDuanjuCatalogPage(ctx, source, page, category)
		}
	}
	if err != nil && len(items) == 0 {
		return result, err
	}
	_, _, _, hongguoWeb := hongguoWebCategory(category)
	if len(items) == 0 && page == 1 && !(source == sourceHongguo && hongguoWeb) && !(source == sourceHuangguoAI && validHuangguoAITagCategory(category)) && source != sourceHuangju && source != sourceYeguo && source != sourceDSD && source != sourceSorani && source != sourceGuipian && source != sourceHanxiaoquan && !isDuanjuProviderSource(source) {
		return result, errors.New("站源暂未返回剧集，请稍后刷新")
	}
	if err != nil {
		result.Warning = publicError(err).Error()
	}
	seen := map[string]bool{}
	for _, drama := range items {
		if drama.ID == "" || seen[drama.ID] {
			continue
		}
		seen[drama.ID] = true
		result.Items = append(result.Items, nativeNormalize(drama))
	}
	result.NextPage = cursor.Next
	result.Pagination = cursor.Pagination
	refresh, _ := ctx.Value(sourceRuleRefreshKey{}).(bool)
	result.Authoritative = (source == sourceHuangguoAI || refresh || source == sourceHongguo && hongguoWeb) && err == nil
	engine.saveCatalogCache(cacheKey, &result)
	return result, nil
}

// nativeDetail 取得指定作品的详情及分集信息。
func (engine *nativeEngine) nativeDetail(ctx context.Context, drama nativeDrama) (any, error) {
	source, sourceID, valid := splitProviderDramaID(drama.ID)
	if !valid {
		return nil, errors.New("剧集信息无效，请刷新剧库")
	}
	ctx = withFreshOrdinaryDetail(ctx, source)
	var title string
	var raw Drama
	var chapters []Chapter
	var err error
	switch source {
	case sourceHuangguoAI:
		if huangguoWorkOrigin(sourceID) == "" {
			return nil, errors.New("旧目录条目缺少域名身份，请更新目录后重新打开；配置、记录及已下载视频仍保留")
		}
		drama.Source, drama.SourceID = source, sourceID
		raw, chapters, err = engine.downloader.fetchHuangguoAIDetail(ctx, sourceID)
	case sourceHuangguoVideo:
		raw, chapters, err = engine.downloader.fetchHuangguoVideoDetail(ctx, sourceID)
	case sourceCloudFront:
		raw, chapters, err = engine.downloader.fetchLegacyDetail(ctx, sourceID)
	case sourceHuangju:
		raw, chapters, err = engine.downloader.fetchHuangjuDetail(ctx, sourceID)
	case sourceYeguo:
		raw, chapters, err = engine.downloader.fetchYeguoDetail(ctx, sourceID)
	case sourceDSD:
		raw, chapters, err = engine.downloader.fetchDSDDetail(ctx, sourceID)
	case sourceSorani:
		raw, chapters, err = engine.downloader.fetchSoraniDetail(ctx, sourceID)
	case sourceGuipian:
		raw, chapters, err = engine.downloader.fetchGuipianDetail(ctx, sourceID)
	case sourceHanxiaoquan:
		raw, chapters, err = engine.downloader.fetchHanxiaoquanDetail(ctx, sourceID)
	default:
		if isDuanjuProviderSource(source) {
			raw, chapters, err = engine.downloader.fetchDuanjuDetail(ctx, source, sourceID)
			break
		}
		title, chapters, err = engine.downloader.GetHuangguoChapters(ctx, source, sourceID)
	}
	if err != nil {
		return nil, err
	}
	if len(chapters) == 0 {
		return nil, errors.New("该剧暂时没有可播放的分集")
	}
	if ordinarySource(source) {
		if err := validateOrdinaryDetail(drama, raw, title, chapters); err != nil {
			return nil, err
		}
	} else if source == sourceHuangguoAI {
		if err := validateHuangguoWorkDetail(drama, raw, chapters); err != nil {
			return nil, err
		}
	}
	var integrity *nativeOrdinaryIntegrity
	integrityWarning := ""
	if ordinarySource(source) || source == sourceHuangguoAI {
		integrityWarning = ordinaryDetailCountWarning(raw, chapters)
		var checked nativeOrdinaryIntegrity
		chapters, checked, err = prepareOrdinaryChapters(raw, chapters)
		if err != nil {
			return nil, err
		}
		integrity = &checked
	}
	if title != "" && title != "短剧" {
		drama.Title = title
	}
	if raw.ID == drama.ID {
		drama = mergeNativeDrama(drama, nativeNormalize(raw))
	}
	if source == sourceHongguo {
		raw := engine.downloader.hongguoCachedDrama(Drama{ID: drama.ID, Title: drama.Title, Source: source})
		drama = mergeNativeDrama(drama, nativeNormalize(raw))
	}
	if source == sourceHuangdou {
		if row, err := engine.downloader.huangdouDetail(ctx, sourceID); err == nil {
			fresh := nativeNormalize(huangdouDramaFromMap(row))
			if fresh.ID == drama.ID {
				drama = mergeNativeDrama(drama, fresh)
			}
		}
	}
	drama.Source, drama.SourceID, drama.Episodes = source, sourceID, len(chapters)
	if ordinarySource(source) || source == sourceHuangguoAI {
		drama.Episodes = ordinaryEpisodeCount(chapters)
		drama.EpisodeCountVerified = true
	}
	if source == sourceHuangju || source == sourceYeguo {
		drama.Episodes = max(drama.Episodes, nativeNormalize(raw).Episodes)
	}
	warning := ""
	if ordinarySource(source) || source == sourceHuangguoAI {
		warning = integrityWarning
	}
	if source == sourceHuangguoAI {
		engine.mu.Lock()
		if engine.huangguoFreshMetadata == nil {
			engine.huangguoFreshMetadata = map[string]nativeDrama{}
		}
		engine.huangguoFreshMetadata[drama.ID] = drama
		engine.mu.Unlock()
	}
	if readOnly, _ := ctx.Value(ordinaryDetailReadOnlyKey{}).(bool); !readOnly {
		if err := engine.saveDetailMetadata(drama); err != nil {
			warning = joinNativeWarnings(warning, err.Error())
		}
	}
	return map[string]any{"drama": drama, "chapters": chapters, "warning": warning, "integrity": integrity}, nil
}

// nativeResolve 为指定作品与分集构造播放计划。
func (engine *nativeEngine) nativeResolve(ctx context.Context, input nativeInput) (nativePlan, error) {
	if _, _, valid := splitProviderDramaID(input.Drama.ID); !valid {
		return nativePlan{}, errors.New("剧集信息无效")
	}
	if !input.Force && engine.downloads != nil {
		if plan, found, err := engine.downloads.localPlan(input.Drama.ID, input.Index); found || err != nil {
			if input.Action != "prepareHandoff" || !errors.Is(err, errNativeLocalFile) {
				engine.downloader.recordPlaybackResolution(input, true, err)
				return plan, err
			}
		}
	}
	task := Task{DramaID: input.Drama.ID, DramaTitle: input.Drama.Title, Chapter: input.Chapter, Index: input.Index}
	if input.Force && sourceFromDramaID(input.Drama.ID) == sourceHongguo {
		ctx = context.WithValue(ctx, hongguoFreshMediaKey{}, true)
	}
	media, err := engine.downloader.resolvePlaybackMedia(ctx, task)
	if err != nil {
		engine.downloader.recordPlaybackResolution(input, false, err)
		return nativePlan{}, err
	}
	choice := nativePlaybackChoicesForSource(sourceFromDramaID(input.Drama.ID), media, input.Quality)
	if len(choice.media) == 0 {
		return nativePlan{}, errors.New("该集没有可用的播放地址")
	}
	choice.dramaID, choice.episodeIndex = input.Drama.ID, input.Index
	if series, video, valid := hongguoPlaybackIDs(task); valid {
		choice.danmakuSeries, choice.danmakuVideo = series, video
	}
	choice.checks = &nativeRouteChecks{results: map[int]nativeRouteStatus{}}
	var plan nativePlan
	for choice.index < len(choice.media) {
		plan, err = engine.nativeOpenPlayback(ctx, choice)
		if err == nil || nativeRouteMustStop(err) || ctx.Err() != nil {
			break
		}
		if choice.media[choice.index].pending == nil {
			break
		}
		choice.index++
	}
	engine.downloader.recordPlaybackResolution(input, false, err)
	return plan, err
}

// recordPlaybackResolution 记录播放Resolution。
func (d *Downloader) recordPlaybackResolution(input nativeInput, local bool, err error) {
	level, message := "info", "本集使用在线线路"
	if local {
		message = "本集使用已下载的本地文件，未请求线上取流"
	}
	if err != nil {
		level, message = "warning", fmt.Sprintf("本集播放地址解析失败（local=%t）：%v", local, publicError(err))
	}
	d.recordDiagnostic(diagnosticEvent{Event: "playback.resolve", Level: level, Source: input.Drama.Source,
		DramaID: input.Drama.ID, DramaTitle: input.Drama.Title, Episode: input.Index, Message: message})
}

// searchCachedCatalog 搜索已缓存目录。
func (engine *nativeEngine) searchCachedCatalog(source, query string) []nativeDrama {
	engine.mu.Lock()
	keys := make([]string, 0)
	for key := range engine.catalogs {
		if key == source || strings.HasPrefix(key, source+"|") {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	if len(keys) > 1 && keys[0] == source {
		keys = append(keys[1:], source)
	}
	var items []nativeDrama
	for _, key := range keys {
		items = mergeNativeCatalog(items, engine.catalogs[key])
	}
	engine.mu.Unlock()
	result := make([]nativeDrama, 0)
	for _, item := range items {
		if item.Source != "" && canonicalProviderSource(item.Source) != source {
			continue
		}
		if item.Source == "" {
			item.Source = source
		}
		text := hongguoSearchText(item.Title + " " + item.Description + " " + strings.Join(item.Tags, " "))
		matches := true
		for _, word := range strings.Fields(query) {
			matches = matches && strings.Contains(text, hongguoSearchText(word))
		}
		if matches {
			result = append(result, item)
		}
	}
	return result
}
