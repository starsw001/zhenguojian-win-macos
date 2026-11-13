package core

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"golang.org/x/net/html"
)

var huangguoAICategorySlug = regexp.MustCompile(`^ai-[a-z0-9][a-z0-9-]{0,63}$`)

type huangguoCatalogOriginKey struct{}

type huangguoCatalogSessionKey struct{}

type huangguoCatalogSession struct {
	roots     []string
	pages     map[string]*html.Node
	seen      map[string]bool
	failures  []error
	firstRows []Drama
	firstMore bool
	onProbe   func(string, int)
}

// huangguoCatalogRequiresHuman 判断目录响应是否需要人工处理访问条件。
func huangguoCatalogRequiresHuman(err error) bool {
	var failure *huangguoFetchError
	var backoff *requestBackoff
	if errors.As(err, &failure) && failure.blocked || errors.As(err, &backoff) {
		return true
	}
	message := strings.ToLower(err.Error())
	for _, word := range []string{"验证码", "浏览器验证", "cloudflare", "限流", "授权", "登录", "状态码401", "状态码402", "状态码403", "状态码429"} {
		if strings.Contains(message, word) {
			return true
		}
	}
	return false
}

// huangguoCatalogInteger 返回黄果目录整数。
func huangguoCatalogInteger(value any) (int, bool) {
	if number, ok := value.(float64); ok {
		if number >= 0 && number <= 1000000000 && float64(int(number)) == number {
			return int(number), true
		}
		return 0, false
	}
	number, err := strconv.Atoi(strings.TrimSpace(fmt.Sprint(value)))
	return number, err == nil && number >= 0 && number <= 1000000000
}

// huangguoAIHasCatalogCollection 判断响应是否包含可识别的目录集合。
func huangguoAIHasCatalogCollection(value any, depth int) bool {
	if depth > 6 {
		return false
	}
	if _, ok := value.([]any); ok {
		return true
	}
	if object, ok := value.(map[string]any); ok {
		for _, key := range []string{"data", "items", "list", "results", "videos", "rows"} {
			if child, found := object[key]; found && huangguoAIHasCatalogCollection(child, depth+1) {
				return true
			}
		}
	}
	return false
}

// huangguoAICatalogContext 返回黄果AI目录上下文。
func (d *Downloader) huangguoAICatalogContext(ctx context.Context) (context.Context, string) {
	origin, _ := ctx.Value(huangguoCatalogOriginKey{}).(string)
	if origin == "" {
		d.providerMu.Lock()
		origin = d.huangguoCatalogOrigin
		d.providerMu.Unlock()
	}
	if origin == "" {
		return ctx, d.providerBaseURL(sourceHuangguoAI)
	}
	return context.WithValue(ctx, huangguoProbeOriginKey{}, origin), origin
}

// huangguoAICatalogCategories 返回黄果AI目录分类列表。
func (d *Downloader) huangguoAICatalogCategories() []nativeCategory {
	d.providerMu.Lock()
	defer d.providerMu.Unlock()
	if len(d.huangguoCatalogSections) > 0 {
		return append([]nativeCategory{}, d.huangguoCatalogSections...)
	}
	return append([]nativeCategory{}, nativeAICategories...)
}

// huangguoAISections 返回黄果AI分区列表。
func huangguoAISections(document *html.Node) []nativeCategory {
	sections := append([]nativeCategory{}, nativeAICategories...)
	seen := map[string]bool{}
	for _, entry := range sections {
		seen[entry.ID] = true
	}
	for _, anchor := range providerHTMLNodes(document, func(node *html.Node) bool {
		return node.Type == html.ElementNode && node.Data == "a"
	}) {
		address, err := url.Parse(providerHTMLAttr(anchor, "href"))
		if err != nil || address.IsAbs() || address.Host != "" {
			continue
		}
		id := strings.Trim(address.Path, "/")
		name := providerHTMLText(anchor)
		if !seen[id] && huangguoAICategorySlug.MatchString(id) && name != "" && len([]rune(name)) <= 48 {
			seen[id] = true
			sections = append(sections, nativeCategory{ID: id, Name: name})
		}
	}
	return sections
}

// huangguoAIPageHasMore 判断目录响应是否表明还有后续页面。
func huangguoAIPageHasMore(raw string, page, count int) bool {
	var envelope map[string]any
	if json.Unmarshal([]byte(raw), &envelope) != nil {
		return count >= 24
	}
	objects := []map[string]any{envelope}
	for index := 0; index < len(objects) && index < 16; index++ {
		for _, key := range []string{"data", "pagination", "pageInfo", "meta"} {
			if value, ok := objects[index][key].(map[string]any); ok {
				objects = append(objects, value)
			}
		}
	}
	for index := len(objects) - 1; index >= 0; index-- {
		object := objects[index]
		for _, key := range []string{"hasMore", "has_more", "hasNext", "has_next"} {
			if value, ok := object[key].(bool); ok {
				return value
			}
			if value, ok := object[key].(string); ok {
				if more, err := strconv.ParseBool(value); err == nil {
					return more
				}
			}
		}
		for _, key := range []string{"totalPages", "total_pages", "last_page", "pages"} {
			if value, ok := huangguoCatalogInteger(object[key]); ok && value >= 1 {
				return page < value
			}
		}
		if total, ok := huangguoCatalogInteger(object["total"]); ok {
			size := 24
			for _, key := range []string{"pageSize", "page_size", "per_page", "size"} {
				if value, ok := huangguoCatalogInteger(object[key]); ok && value > 0 {
					size = value
				}
			}
			return page*size < total
		}
	}
	return count >= 24
}

// fetchHuangguoAISectionPage 获取黄果AI分区页面。
func (d *Downloader) fetchHuangguoAISectionPage(ctx context.Context, page int, section nativeCategory) ([]Drama, bool, error) {
	ctx = context.WithValue(ctx, providerTextNoCacheKey{}, true)
	ctx, base := d.huangguoAICatalogContext(ctx)
	address := fmt.Sprintf("%s/api/videos/category/%s?sort=hot&page=%d&size=24", base, url.PathEscape(section.ID), max(1, page))
	body, err := d.fetchProviderText(ctx, address, base+"/"+section.ID+"/")
	if err != nil {
		return nil, false, err
	}
	var decoded any
	if json.Unmarshal([]byte(body), &decoded) != nil {
		return nil, false, errors.New("目录接口未返回有效JSON，未将此页当作更新完成")
	}
	if object, ok := decoded.(map[string]any); ok {
		if success, present := object["success"].(bool); present && !success {
			return nil, false, errors.New("目录接口返回失败状态，旧索引保留")
		}
		if code, present := object["code"].(float64); present && code != 0 && code != 200 {
			return nil, false, fmt.Errorf("目录接口返回状态码%g，旧索引保留", code)
		}
		if _, present := object["data"]; !present && object["items"] == nil && object["list"] == nil && object["results"] == nil {
			return nil, false, errors.New("目录接口缺少数据列表，未将空响应当作全量完成")
		}
	} else if _, ok := decoded.([]any); !ok {
		return nil, false, errors.New("目录接口没有返回列表，旧索引保留")
	}
	if !huangguoAIHasCatalogCollection(decoded, 0) {
		return nil, false, errors.New("目录接口没有有效数据列表，未将空对象或null当作目录完成")
	}
	items := parseHuangguoAIJSONCards([]byte(body), address, section.Name)
	return items, huangguoAIPageHasMore(body, max(1, page), len(items)), nil
}

// saveHuangguoCatalogProbe 保存黄果目录探测。
func (d *Downloader) saveHuangguoCatalogProbe(origin, message string) error {
	r := d.huangguoMirrorStore()
	r.mu.Lock()
	defer r.mu.Unlock()
	index := r.appendLocked(origin, time.Now().Format("2006-01-02"), "启动目录检查")
	if index >= 0 {
		now := time.Now()
		r.disk.Mirrors[index].CatalogCheckedAt = &now
		r.disk.Mirrors[index].CatalogMessage = message
	}
	return r.saveLocked()
}

// loadHuangguoCatalogOrigins 加载黄果目录来源列表。
func (d *Downloader) loadHuangguoCatalogOrigins(ctx context.Context) (*huangguoCatalogSession, error) {
	ctx = context.WithValue(ctx, providerTextNoCacheKey{}, true)
	previous, _, _ := d.huangguoMirrorStore().snapshot()
	known := map[string]bool{}
	for _, entry := range previous {
		known[entry.Origin] = true
	}
	publicationContext, cancel := context.WithTimeout(ctx, 45*time.Second)
	_, published, publicationError := d.discoverHuangguoCatalogCandidates(publicationContext, true)
	cancel()
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	var permanentLinks []string
	validPages := map[string]*html.Node{}
	var validPermanent []string
	for _, permanent := range huangguoAIPermanentURLs {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		body, actual, err := d.fetchHuangguoDirectText(ctx, strings.TrimRight(permanent, "/")+"/", "")
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		status := huangguoPublicationStatus{URL: permanent, CheckedAt: time.Now(), State: "failed"}
		if err == nil {
			document, _ := html.Parse(strings.NewReader(body))
			origin := publicHuangguoOrigin(actual)
			if origin != "" && len(parseHuangguoAIPopularTags(document, actual)) > 0 && providerHTMLFirstClass(document, "hg-card-grid") != nil {
				validPages[origin] = document
				validPermanent = append(validPermanent, origin)
			}
			permanentLinks = append(permanentLinks, parseHuangguoPublication(body)...)
			for _, script := range huangguoPublicationScripts(body, permanent) {
				js, scriptErr := d.fetchHuangguoPublicationText(ctx, script, permanent+"/")
				if scriptErr == nil {
					permanentLinks = append(permanentLinks, parseHuangguoPublication(js)...)
				}
			}
			status.State, status.Message = "ok", "永久入口已读取，目录类型另行核对；不代表实播验证通过"
		} else {
			status.Message = publicError(err).Error()
		}
		r := d.huangguoMirrorStore()
		r.mu.Lock()
		for _, origin := range permanentLinks {
			r.appendLocked(origin, time.Now().Format("2006-01-02"), permanent)
		}
		found := false
		for index, entry := range r.disk.Publications {
			if entry.URL == permanent {
				r.disk.Publications[index], found = status, true
			}
		}
		if !found {
			r.disk.Publications = append(r.disk.Publications, status)
		}
		saveErr := r.saveLocked()
		r.mu.Unlock()
		if saveErr != nil {
			return nil, saveErr
		}
		var failure *huangguoFetchError
		if errors.As(err, &failure) && failure.blocked {
			return nil, err
		}
	}
	roots := []string{}
	for _, origin := range append(append([]string{}, permanentLinks...), published...) {
		if !known[origin] {
			roots = append(roots, origin)
		}
	}
	roots = append(roots, validPermanent...)
	d.providerMu.Lock()
	lastOrigin := d.huangguoCatalogOrigin
	d.providerMu.Unlock()
	roots = append(roots, lastOrigin)
	sort.SliceStable(previous, func(i, j int) bool { return previous[i].AddedOn > previous[j].AddedOn })
	for _, entry := range previous {
		if usableHuangguoMirror(entry, sourceHuangguoAI) {
			roots = append(roots, entry.Origin)
		}
	}
	roots = append(roots, permanentLinks...)
	roots = append(roots, published...)
	roots = append(roots, d.providerBaseURL(sourceHuangguoAI))
	session := &huangguoCatalogSession{roots: roots, pages: validPages, seen: map[string]bool{}}
	if publicationError != nil {
		session.failures = append(session.failures, publicationError)
	}
	return session, nil
}

// chooseHuangguoCatalogOrigin 选择黄果目录来源。
func (d *Downloader) chooseHuangguoCatalogOrigin(ctx context.Context) (string, *html.Node, error) {
	session, _ := ctx.Value(huangguoCatalogSessionKey{}).(*huangguoCatalogSession)
	if session == nil {
		var err error
		session, err = d.loadHuangguoCatalogOrigins(ctx)
		if err != nil {
			return "", nil, err
		}
	}
	ctx = context.WithValue(ctx, providerTextNoCacheKey{}, true)
	for _, raw := range session.roots {
		if ctx.Err() != nil {
			return "", nil, ctx.Err()
		}
		origin := publicHuangguoOrigin(raw)
		if origin == "" || session.seen[origin] {
			continue
		}
		session.seen[origin] = true
		if session.onProbe != nil {
			session.onProbe(origin, len(session.seen))
		}
		document := session.pages[origin]
		actual := origin + "/"
		if document == nil {
			body, effective, err := d.fetchHuangguoDirectText(ctx, actual, "")
			if ctx.Err() != nil {
				return "", nil, ctx.Err()
			}
			if err != nil {
				_ = d.saveHuangguoCatalogProbe(origin, publicError(err).Error())
				session.failures = append(session.failures, fmt.Errorf("%s: %w", origin, err))
				var failure *huangguoFetchError
				if errors.As(err, &failure) && failure.blocked {
					return "", nil, err
				}
				continue
			}
			actual = effective
			document, _ = html.Parse(strings.NewReader(body))
		}
		if len(parseHuangguoAIPopularTags(document, actual)) == 0 || providerHTMLFirstClass(document, "hg-card-grid") == nil {
			message := "不是当前支持的AI目录页面，未用于全量索引；原播放验证状态保留"
			_ = d.saveHuangguoCatalogProbe(origin, message)
			session.failures = append(session.failures, fmt.Errorf("%s: %s", origin, message))
			continue
		}
		actualOrigin := publicHuangguoOrigin(actual)
		if actualOrigin == "" {
			continue
		}
		if actualOrigin != origin && session.seen[actualOrigin] {
			if err := d.saveHuangguoCatalogProbe(origin, "该地址跳转到本轮已检查的落点，不重复采集"); err != nil {
				return "", nil, err
			}
			continue
		}
		sections := huangguoAISections(document)
		probeContext := context.WithValue(ctx, huangguoCatalogOriginKey{}, actualOrigin)
		firstRows, firstMore, err := d.fetchHuangguoAISectionPage(probeContext, 1, sections[0])
		if err != nil {
			if ctx.Err() != nil {
				return "", nil, ctx.Err()
			}
			_ = d.saveHuangguoCatalogProbe(origin, "目录接口校验失败："+publicError(err).Error())
			session.failures = append(session.failures, fmt.Errorf("%s: %w", origin, err))
			if huangguoCatalogRequiresHuman(err) {
				return "", nil, err
			}
			continue
		}
		if err := d.saveHuangguoCatalogProbe(origin, "AI目录与分页接口可读取，未标记海报、选集或实播通过"); err != nil {
			return "", nil, err
		}
		session.seen[actualOrigin] = true
		session.firstRows, session.firstMore = firstRows, firstMore
		return actualOrigin, document, nil
	}
	return "", nil, fmt.Errorf("未选出可读取的新AI目录地址，缓存与失败记录已保留：%w", errors.Join(session.failures...))
}

// scheduleHuangguoStartupCatalog 调度黄果启动目录。
func (engine *nativeEngine) scheduleHuangguoStartupCatalog() {
	engine.mu.Lock()
	if engine.startupAIRequested {
		engine.mu.Unlock()
		return
	}
	engine.startupAIRequested = true
	ctx, cancel := context.WithCancel(context.Background())
	engine.startupAICancel = cancel
	engine.mu.Unlock()
	start := func() bool {
		if ctx.Err() != nil {
			return true
		}
		engine.mu.Lock()
		busy := engine.sourceTasks[sourceHuangguoAI] != nil && engine.sourceRecords[sourceHuangguoAI].Operation != "refreshAll"
		engine.mu.Unlock()
		if busy {
			return false
		}
		status, err := engine.startSourceTask(sourceHuangguoAI, "refreshAll", nativeDrama{})
		if err != nil {
			engine.changeSourceRecord(sourceHuangguoAI, func(record *nativeSourceRecord) {
				record.Operation, record.Stage, record.Error = "refreshAll", "启动全量更新未开始", publicError(err).Error()
			})
			return true
		}
		if status.Operation != "refreshAll" {
			return false
		}
		engine.mu.Lock()
		if ctx.Err() != nil {
			if task := engine.sourceTasks[sourceHuangguoAI]; task != nil {
				task.cancel()
			}
		} else {
			engine.startupAITask = engine.sourceTasks[sourceHuangguoAI]
		}
		engine.mu.Unlock()
		return true
	}
	if start() {
		return
	}
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case <-time.After(500 * time.Millisecond):
				if start() {
					return
				}
			}
		}
	}()
}

// stopHuangguoStartupCatalog 停止黄果启动目录。
func (engine *nativeEngine) stopHuangguoStartupCatalog() {
	engine.mu.Lock()
	defer engine.mu.Unlock()
	if engine.startupAICancel != nil {
		engine.startupAICancel()
		engine.startupAICancel = nil
	}
	if task := engine.startupAITask; task != nil && engine.sourceTasks[sourceHuangguoAI] == task {
		task.cancel()
	}
	engine.startupAITask, engine.startupAIRequested = nil, false
}

type huangguoCatalogSnapshot struct {
	origin     string
	categories []nativeCategory
	sections   []nativeCategory
	rows       []nativeDrama
	pages      int
}

// reportCatalogMetadataProgress 报告目录元数据进度。
func (engine *nativeEngine) reportCatalogMetadataProgress(stage, origin string) {
	engine.changeSourceRecord(sourceHuangguoAI, func(record *nativeSourceRecord) {
		record.Stage = stage
	})
	engine.downloader.recordDiagnostic(diagnosticEvent{
		Event: "catalog.metadata_progress", Level: "info", Source: sourceHuangguoAI,
		Message: stage + " · 地址：" + origin,
	})
}

// collectHuangguoCatalog 收集黄果目录。
func (engine *nativeEngine) collectHuangguoCatalog(ctx context.Context, origin string, document *html.Node, session *huangguoCatalogSession) (huangguoCatalogSnapshot, error) {
	snapshot := huangguoCatalogSnapshot{origin: origin}
	ctx = context.WithValue(ctx, huangguoCatalogOriginKey{}, origin)
	sections := huangguoAISections(document)
	snapshot.sections = sections
	snapshot.categories = append(append([]nativeCategory{{Name: "全部"}}, sections...), parseHuangguoAIPopularTags(document, origin+"/")...)
	positions := map[string]int{}
	stagedBytes := 0
	for index, section := range sections {
		seen := map[string]bool{}
		for page := 1; ; page++ {
			if ctx.Err() != nil {
				return snapshot, ctx.Err()
			}
			if page > 1000000 {
				return snapshot, errors.New("目录页码超出接口范围，未提交暂存目录")
			}
			engine.reportCatalogMetadataProgress(fmt.Sprintf("单地址暂存 %d/%d · %s · 第%d页；完成后替换目录缓存", index+1, len(sections), section.Name, page), origin)
			var rows []Drama
			var more bool
			var err error
			if index == 0 && page == 1 {
				rows, more = session.firstRows, session.firstMore
			} else {
				rows, more, err = engine.downloader.fetchHuangguoAISectionPage(ctx, page, section)
			}
			if err != nil {
				return snapshot, fmt.Errorf("%s 第%d页未完成：%w", section.Name, page, err)
			}
			fresh := 0
			for _, row := range rows {
				if !seen[row.ID] {
					seen[row.ID] = true
					fresh++
				}
				item := nativeNormalize(row)
				if position, found := positions[item.ID]; found {
					if strings.Join(strings.Fields(snapshot.rows[position].Title), " ") != strings.Join(strings.Fields(item.Title), " ") {
						return snapshot, errors.New("同一目录ID出现不同作品名称，未混合提交")
					}
					snapshot.rows[position] = mergeNativeDrama(snapshot.rows[position], item)
				} else {
					body, marshalErr := json.Marshal(item)
					if marshalErr != nil {
						return snapshot, marshalErr
					}
					stagedBytes += len(body)
					if stagedBytes > engine.cacheBytes("catalogMiB") {
						return snapshot, errNativeCatalogLimit
					}
					positions[item.ID] = len(snapshot.rows)
					snapshot.rows = append(snapshot.rows, item)
				}
			}
			if more && (len(rows) == 0 || page > 1 && fresh == 0) {
				return snapshot, fmt.Errorf("%s 第%d页分页未推进，未提交暂存目录", section.Name, page)
			}
			snapshot.pages++
			engine.changeSourceRecord(sourceHuangguoAI, func(record *nativeSourceRecord) {
				record.Completed, record.CatalogFetched = snapshot.pages, len(snapshot.rows)
			})
			if !more {
				break
			}
		}
	}
	if len(snapshot.rows) == 0 {
		return snapshot, errors.New("当前地址目录全部为空，旧缓存保留")
	}
	return snapshot, nil
}

// commitHuangguoCatalog 提交黄果目录。
func (engine *nativeEngine) commitHuangguoCatalog(snapshot huangguoCatalogSnapshot) error {
	engine.mu.Lock()
	defer engine.mu.Unlock()
	for index, row := range snapshot.rows {
		if fresh, found := engine.huangguoFreshMetadata[row.ID]; found && fresh.EpisodeCountVerified && ordinaryTitle(fresh.Title) == ordinaryTitle(row.Title) {
			snapshot.rows[index].Episodes, snapshot.rows[index].EpisodeCountVerified = fresh.Episodes, true
		}
	}
	catalogs := make(map[string][]nativeDrama, len(engine.catalogs)+1)
	states := make(map[string]nativeCatalogState, len(engine.catalogStates)+1)
	categories := make(map[string][]nativeCategory, len(engine.categoryOptions)+1)
	for key, rows := range engine.catalogs {
		if key != sourceHuangguoAI && !strings.HasPrefix(key, sourceHuangguoAI+"|") {
			catalogs[key] = rows
		}
	}
	for key, state := range engine.catalogStates {
		if key != sourceHuangguoAI && !strings.HasPrefix(key, sourceHuangguoAI+"|") {
			states[key] = state
		}
	}
	for key, rows := range engine.categoryOptions {
		if key != sourceHuangguoAI && !strings.HasPrefix(key, sourceHuangguoAI+"|") {
			categories[key] = rows
		}
	}
	catalogs[sourceHuangguoAI] = snapshot.rows
	states[sourceHuangguoAI] = nativeCatalogState{Page: snapshot.pages, HasMore: false, UpdatedAt: time.Now()}
	categories[sourceHuangguoAI] = snapshot.categories
	body, err := json.Marshal(nativeCatalogDisk{Version: 3, CatalogEpochs: engine.catalogEpochs, Catalogs: catalogs, States: states, Categories: categories, HongguoApp: engine.hongguoCatalog, Recommendations: engine.recommendations})
	if err == nil && len(body) > engine.cacheBytes("catalogMiB") {
		err = errNativeCatalogLimit
	}
	if err == nil {
		err = writeNativeCacheFile(filepath.Join(engine.directory, "catalogs.json"), body)
	}
	if err != nil {
		return nativeSaveError("剧库", err)
	}
	engine.catalogs, engine.catalogStates, engine.categoryOptions = catalogs, states, categories
	engine.catalogSaveError = nil
	engine.finishCatalogSaveLocked()
	engine.downloader.providerMu.Lock()
	engine.downloader.huangguoCatalogOrigin = snapshot.origin
	engine.downloader.huangguoCatalogSections = append([]nativeCategory{}, snapshot.sections...)
	engine.downloader.providerMu.Unlock()
	return nil
}

// refreshHuangguoAICatalog 刷新黄果AI目录。
func (engine *nativeEngine) refreshHuangguoAICatalog(ctx context.Context) error {
	engine.mu.Lock()
	engine.huangguoFreshMetadata = map[string]nativeDrama{}
	engine.mu.Unlock()
	engine.changeSourceRecord(sourceHuangguoAI, func(record *nativeSourceRecord) {
		record.Stage = "读取发布页与永久入口，本轮固定一个可用地址"
	})
	d := engine.downloader
	session, err := d.loadHuangguoCatalogOrigins(ctx)
	if err != nil {
		return err
	}
	session.onProbe = func(origin string, attempt int) {
		engine.reportCatalogMetadataProgress(fmt.Sprintf("检查第%d个元数据地址", attempt), origin)
	}
	ctx = context.WithValue(ctx, huangguoCatalogSessionKey{}, session)
	for {
		origin, document, err := d.chooseHuangguoCatalogOrigin(ctx)
		if err != nil {
			return err
		}
		snapshot, err := engine.collectHuangguoCatalog(ctx, origin, document, session)
		if err != nil {
			if saveErr := d.saveHuangguoCatalogProbe(origin, "本地址同步未完成，暂存数据未提交："+publicError(err).Error()); saveErr != nil {
				return errors.Join(err, saveErr)
			}
			if ctx.Err() != nil || errors.Is(err, errNativeCatalogLimit) || huangguoCatalogRequiresHuman(err) {
				return err
			}
			session.failures = append(session.failures, fmt.Errorf("%s: %w", origin, err))
			engine.changeSourceRecord(sourceHuangguoAI, func(record *nativeSourceRecord) {
				record.Stage = "当前地址中途不可用，丢弃暂存数据，尝试下一个地址；旧缓存仍保留"
			})
			continue
		}
		engine.changeSourceRecord(sourceHuangguoAI, func(record *nativeSourceRecord) {
			record.Stage = "同一地址目录获取完成，正在原子替换该源目录缓存"
		})
		engine.mu.Lock()
		oldIDs := map[string]bool{}
		for _, row := range engine.catalogs[sourceHuangguoAI] {
			oldIDs[row.ID] = true
		}
		engine.mu.Unlock()
		added := 0
		for _, row := range snapshot.rows {
			if !oldIDs[row.ID] {
				added++
			}
		}
		if err := engine.commitHuangguoCatalog(snapshot); err != nil {
			return err
		}
		engine.changeSourceRecord(sourceHuangguoAI, func(record *nativeSourceRecord) {
			record.Total, record.Completed, record.CatalogPages = snapshot.pages, snapshot.pages, snapshot.pages
			record.CatalogFetched, record.CatalogOrigin, record.CatalogFullAt, record.Added = len(snapshot.rows), origin, time.Now(), added
		})
		return d.saveHuangguoCatalogProbe(origin, fmt.Sprintf("单地址元数据同步完成：%d页、%d部；仅更新目录与分类缓存，未验证具体内容、播放或下载", snapshot.pages, len(snapshot.rows)))
	}
}

// searchHuangguoCatalog 搜索黄果目录。
func (engine *nativeEngine) searchHuangguoCatalog(query string, page int) nativeCatalogResult {
	all := engine.searchCachedCatalog(sourceHuangguoAI, query)
	page = max(1, page)
	start := min(len(all), (page-1)*60)
	end := min(len(all), start+60)
	result := nativeCatalogResult{Items: all[start:end], Page: page, HasMore: end < len(all), LocalSearch: true}
	engine.mu.Lock()
	record := engine.sourceRecords[sourceHuangguoAI]
	count := len(engine.catalogs[sourceHuangguoAI])
	engine.mu.Unlock()
	if record.Running && record.Operation == "refreshAll" {
		result.Warning = fmt.Sprintf("全量目录正在更新，已处理%d页；当前搜索已保存的%d部，更新后自动补全结果", record.Completed, count)
	} else if record.CatalogFullAt.IsZero() || record.Error != "" {
		result.Warning = fmt.Sprintf("全量目录尚未更新完成，当前仅搜索已保存的%d部；可在站源管理中更新", count)
		if record.Error != "" {
			result.Warning += "：" + record.Error
		}
	}
	return result
}
