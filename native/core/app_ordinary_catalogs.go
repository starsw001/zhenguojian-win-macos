package core

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

type ordinaryDetailFreshKey struct{}
type ordinaryDetailReadOnlyKey struct{}

// ordinarySource 判断站源是否属于普通影视源范围。
func ordinarySource(source string) bool {
	switch source {
	case sourceHongguo, sourceHanxiaoquan, sourceGuipian, sourceSorani,
		sourceYaguo, sourceMaoguo, sourceFanguo, sourceGuanguo, sourceHeguo, sourceXingguo,
		sourceHuaguo, sourceNiuguo, sourceWangguo, sourceFaguo, sourcePiguo, sourceWuguo:
		return true
	}
	return false
}

// ordinaryCacheKey 生成普通站源缓存使用的键。
func ordinaryCacheKey(key string) bool {
	source, _, _ := strings.Cut(key, "|")
	return ordinarySource(source) && nativeSourceAvailable(source)
}

// ordinaryTitle 规整普通站源作品标题，用于后续匹配。
func ordinaryTitle(title string) string {
	return strings.ToLower(strings.Join(strings.Fields(title), " "))
}

// validateOrdinaryDetail 校验普通站源详情。
func validateOrdinaryDetail(selected nativeDrama, raw Drama, fetchedTitle string, chapters []Chapter) error {
	source, sourceID, valid := splitProviderDramaID(selected.ID)
	if !valid || selected.Source != source {
		return errors.New("所选普通站点的剧集标识无效，请刷新目录")
	}
	if raw.ID != "" && raw.ID != selected.ID || raw.Source != "" && raw.Source != source || raw.SourceID != "" && raw.SourceID != sourceID {
		return errors.New("站点详情标识与所选作品不符，已阻止混入分集")
	}
	title := raw.DisplayTitle()
	if raw.ID == "" && fetchedTitle != "" {
		title = fetchedTitle
	}
	expected := ordinaryTitle(selected.Title)
	if title != "" && title != "短剧" && expected != "" && expected != "短剧" && !strings.Contains(ordinaryTitle(title), expected) {
		return errors.New("站点详情名称与所选作品不符，请刷新目录或切换站点")
	}
	for _, chapter := range chapters {
		for _, alternate := range chapter.Alternatives {
			if !sameEpisodeRoute(chapter, alternate) || len(alternate.Alternatives) != 0 ||
				!strings.HasPrefix(alternate.ID, providerChapterID(source, sourceID, "")) {
				return errors.New("备用线路与当前作品或分集不符")
			}
		}
		if chapter.Source != "" && chapter.Source != source {
			return errors.New("详情包含其他站源的分集，已停止使用")
		}
		if _, _, identified := splitProviderDramaID(chapter.ID); identified && !strings.HasPrefix(chapter.ID, providerChapterID(source, sourceID, "")) {
			return errors.New("详情分集不属于所选作品，已停止使用")
		}
		if owner, _, identified := maccmsEpisodeRoute(chapter.PageURL); identified && owner != sourceID {
			return errors.New("详情分集播放页指向其他作品，已停止使用")
		}
	}
	_, _, err := prepareOrdinaryChapters(raw, chapters)
	return err
}

// ordinaryEpisodeCount 统计普通站源详情中可识别的分集数量。
func ordinaryEpisodeCount(chapters []Chapter) int {
	seen := map[string]bool{}
	for index, chapter := range chapters {
		seen[chapter.EpisodeString(index+1)] = true
	}
	return len(seen)
}

// ordinaryDetailCountWarning 生成普通站源详情集数与目录标记不一致时的提示。
func ordinaryDetailCountWarning(raw Drama, chapters []Chapter) string {
	declared, actual := nativeNormalize(raw).Episodes, ordinaryEpisodeCount(chapters)
	if actual == 0 {
		return ""
	}
	_, integrity, err := prepareOrdinaryChapters(raw, chapters)
	if err != nil {
		return err.Error()
	}
	if len(integrity.MissingNumbers) == 0 && (declared == 0 || declared == actual) && integrity.InferredNumbers == 0 {
		return ""
	}
	seen := map[string]bool{}
	for index, chapter := range chapters {
		seen[chapter.EpisodeString(index+1)] = true
	}
	var missing []string
	for _, number := range integrity.MissingNumbers {
		if len(missing) >= 12 {
			break
		}
		if !seen[strconv.Itoa(number)] {
			missing = append(missing, strconv.Itoa(number))
		}
	}
	note := ""
	if len(missing) > 0 {
		note = "（未返回第" + strings.Join(missing, "、") + "集"
		if len(integrity.MissingNumbers) > len(missing) {
			note += "等"
		}
		note += "）"
	}
	label := fmt.Sprintf("源站标注%d集，", declared)
	if declared == 0 {
		label = "源站未声明总集数，"
	}
	message := fmt.Sprintf("%s当前接口实际返回%d个独立分集%s；按本站实际返回显示，不从其他作品或站点补集。", label, actual, note)
	if integrity.InferredNumbers > 0 {
		message += fmt.Sprintf("其中%d条未提供编号，按本站列表顺序展示，尚不能确认编号完整。", integrity.InferredNumbers)
	}
	return message
}

// rebuildOrdinaryCatalogs 重建普通站源Catalogs。
func (engine *nativeEngine) rebuildOrdinaryCatalogs() (map[string]int, error) {
	return engine.rebuildOrdinaryCatalogsWithEpoch("")
}

// rebuildOrdinaryCatalogsWithEpoch 重建普通站源Catalogs包含Epoch。
func (engine *nativeEngine) rebuildOrdinaryCatalogsWithEpoch(epoch string) (map[string]int, error) {
	engine.mu.Lock()
	defer engine.mu.Unlock()
	stats := map[string]int{"catalogKeys": 0, "catalogRows": 0, "states": 0, "categories": 0}
	edition := "hongguoshang"
	if buildAllSources == "true" {
		edition = "zhenguoshang"
	}
	if epoch != "" && engine.catalogEpochs[edition] == epoch {
		engine.ordinaryCachesReset = true
		return stats, nil
	}
	if engine.ordinaryCachesReset {
		return stats, nil
	}
	epochs := make(map[string]string, len(engine.catalogEpochs)+1)
	for key, value := range engine.catalogEpochs {
		epochs[key] = value
	}
	if epoch != "" {
		epochs[edition] = epoch
	}
	file := filepath.Join(engine.directory, "catalogs.json")
	if info, err := os.Lstat(file); err == nil && (!info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0) {
		return stats, errors.New("目录缓存不是普通文件，原文件已保留")
	}
	handle, err := os.Open(file)
	if err != nil && !os.IsNotExist(err) {
		return stats, errors.New("无法读取目录缓存，原文件已保留")
	}
	if err == nil {
		info, statErr := handle.Stat()
		limit := engine.cacheBytes("catalogMiB")
		if statErr != nil || info.Size() > int64(limit) {
			_ = handle.Close()
			return stats, errors.New("目录缓存超过已设置上限，原文件已保留")
		}
		body, readErr := io.ReadAll(io.LimitReader(handle, int64(limit)+1))
		_ = handle.Close()
		if readErr != nil || len(body) > limit {
			return stats, errors.New("目录缓存无法完整读取，原文件已保留")
		}
		var disk map[string]json.RawMessage
		if json.Unmarshal(body, &disk) != nil || disk == nil {
			return stats, errors.New("目录缓存损坏，原文件已保留")
		}
		if _, schema := disk["catalogs"]; schema {
			for _, field := range []string{"catalogs", "states", "categories"} {
				raw := disk[field]
				if len(raw) == 0 || string(raw) == "null" {
					continue
				}
				var entries map[string]json.RawMessage
				if json.Unmarshal(raw, &entries) != nil {
					return stats, errors.New("目录缓存结构无效，原文件已保留")
				}
				for key, value := range entries {
					if ordinaryCacheKey(key) {
						if field == "catalogs" {
							stats["catalogKeys"]++
							var rows []json.RawMessage
							_ = json.Unmarshal(value, &rows)
							stats["catalogRows"] += len(rows)
						} else {
							stats[field]++
						}
						delete(entries, key)
					}
				}
				disk[field], _ = json.Marshal(entries)
			}
			if raw := disk["hongguoApp"]; len(raw) > 0 && string(raw) != "null" {
				var state map[string]json.RawMessage
				if json.Unmarshal(raw, &state) != nil {
					return stats, errors.New("红果目录游标无法读取，原缓存已保留")
				}
				state["feeds"] = json.RawMessage(`{}`)
				disk["hongguoApp"], _ = json.Marshal(state)
			}
			if _, found := disk["recommendations"]; found {
				disk["recommendations"] = json.RawMessage(`{}`)
			}
		} else {
			for key, value := range disk {
				if ordinaryCacheKey(key) {
					stats["catalogKeys"]++
					var rows []json.RawMessage
					_ = json.Unmarshal(value, &rows)
					stats["catalogRows"] += len(rows)
					delete(disk, key)
				}
			}
			if epoch != "" {
				legacy, _ := json.Marshal(disk)
				disk = map[string]json.RawMessage{"version": json.RawMessage(`3`), "catalogs": legacy, "states": json.RawMessage(`{}`)}
			}
		}
		if epoch != "" {
			disk["catalogEpochs"], _ = json.Marshal(epochs)
		}
		updated, marshalErr := json.Marshal(disk)
		if marshalErr != nil {
			return stats, marshalErr
		}
		if err := writeNativeCacheFile(file, updated); err != nil {
			return stats, errors.New("普通源目录缓存重建失败，原文件和其他记录已保留")
		}
	} else if epoch != "" {
		body, marshalErr := json.Marshal(nativeCatalogDisk{Version: 3, CatalogEpochs: epochs, Catalogs: map[string][]nativeDrama{}, States: map[string]nativeCatalogState{}})
		if marshalErr != nil {
			return stats, marshalErr
		}
		if err := writeNativeCacheFile(file, body); err != nil {
			return stats, errors.New("无法保存新包目录缓存标识，原记录已保留")
		}
	}
	engine.catalogEpochs = epochs
	for key := range engine.catalogs {
		if ordinaryCacheKey(key) {
			delete(engine.catalogs, key)
		}
	}
	for key := range engine.catalogStates {
		if ordinaryCacheKey(key) {
			delete(engine.catalogStates, key)
		}
	}
	for key := range engine.categoryOptions {
		if ordinaryCacheKey(key) {
			delete(engine.categoryOptions, key)
		}
	}
	engine.recommendations = map[string]nativeRecommendationState{}
	if engine.downloader != nil {
		client := engine.downloader.hongguoClient()
		client.catalogMu.Lock()
		client.mu.Lock()
		client.state.Feeds = map[string]hongguoCatalogCursor{}
		client.details = map[string]hongguoDetailEntry{}
		client.searches = map[string]hongguoSearchEntry{}
		engine.hongguoCatalog = cloneHongguoCatalogState(&client.state)
		client.mu.Unlock()
		client.catalogMu.Unlock()
	}
	engine.ordinaryCachesReset = true
	return stats, nil
}

// withFreshOrdinaryDetail 在需要时刷新普通站源详情，再执行后续操作。
func withFreshOrdinaryDetail(ctx context.Context, source string) context.Context {
	if ordinarySource(source) {
		ctx = context.WithValue(ctx, providerTextNoCacheKey{}, true)
		return context.WithValue(ctx, ordinaryDetailFreshKey{}, true)
	}
	return ctx
}
