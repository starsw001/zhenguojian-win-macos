package core

import (
	"errors"
	"strings"
)

const nativeCatalogMaxBytes = 256 << 20
const nativeSourceMaxBytes = 8 << 20

var errNativeCatalogLimit = errors.New("剧库超过已配置的本地保存上限，新内容尚未保存，已暂停继续加载")
var errNativeSourceLimit = errors.New("站源任务记录超过已配置的本地保存上限，最新记录尚未保存")

type nativePersistenceError struct {
	label string
	cause error
}

// Error 返回当前错误的可显示文本。
func (err *nativePersistenceError) Error() string {
	if errors.Is(err.cause, errNativeCatalogLimit) || errors.Is(err.cause, errNativeSourceLimit) {
		return err.cause.Error()
	}
	return err.label + "尚未保存，最新结果仅在本次运行中保留；请检查剩余空间和存储权限后重试保存"
}

// Unwrap 返回底层错误，供 errors.Is 和 errors.As 继续匹配。
func (err *nativePersistenceError) Unwrap() error { return err.cause }

// nativeSaveError 保存原生错误。
func nativeSaveError(label string, err error) error {
	if err == nil {
		return nil
	}
	return &nativePersistenceError{label: label, cause: err}
}

// joinNativeWarnings 合并原生警告列表。
func joinNativeWarnings(warnings ...string) string {
	seen := map[string]bool{}
	var values []string
	for _, warning := range warnings {
		if warning != "" && !seen[warning] {
			seen[warning] = true
			values = append(values, warning)
		}
	}
	return strings.Join(values, "；")
}

// retryCatalogSave 重试目录保存。
func (engine *nativeEngine) retryCatalogSave() (bool, error) {
	engine.mu.Lock()
	defer engine.mu.Unlock()
	if engine.catalogSaveError == nil {
		return false, nil
	}
	return true, engine.writeCatalogDiskLocked()
}

// finishCatalogSaveLocked 处理原生应用引擎的finish目录保存操作，供内部持锁路径调用。
func (engine *nativeEngine) finishCatalogSaveLocked() {
	changed := false
	for source, record := range engine.sourceRecords {
		if record.NeedsSave {
			record.NeedsSave = false
			if record.Error == "已恢复可读取的缓存，请重新更新" {
				record.Error = ""
			}
			if !record.Running {
				record.Stage = "已保存，可继续更新"
			}
			engine.sourceRecords[source] = record
			changed = true
		}
	}
	if changed {
		engine.saveSourceRecordsLocked()
	}
}

// markCatalogSavePendingLocked 标记目录保存Pending，供内部持锁路径调用。
func (engine *nativeEngine) markCatalogSavePendingLocked() {
	if engine.sourceRecords == nil {
		engine.sourceRecords = map[string]nativeSourceRecord{}
	}
	changed := false
	for key := range engine.catalogs {
		source, _, _ := strings.Cut(key, "|")
		record := engine.sourceRecords[source]
		if !isHuangguoProviderSource(source) || record.NeedsSave {
			continue
		}
		record.NeedsSave = true
		if !record.Running {
			record.Stage = "等待保存"
		}
		engine.sourceRecords[source] = record
		changed = true
	}
	if changed {
		engine.saveSourceRecordsLocked()
	}
}

// storageWarningLocked 返回存储警告，供内部持锁路径调用。
func (engine *nativeEngine) storageWarningLocked() string {
	var warnings []string
	for _, err := range []error{engine.catalogSaveError, engine.sourceSaveError} {
		if err != nil {
			warnings = append(warnings, err.Error())
		}
	}
	return joinNativeWarnings(warnings...)
}

// retrySourceSave 重试站源保存。
func (engine *nativeEngine) retrySourceSave(source string) nativeSourceStatus {
	engine.mu.Lock()
	defer engine.mu.Unlock()
	pending := false
	for id, record := range engine.sourceRecords {
		if record.Stage == "上次保存未完成" && record.Error == "已恢复可读取的缓存，请重新更新" && !record.Running {
			record.NeedsSave = true
			engine.sourceRecords[id] = record
		}
		pending = pending || record.NeedsSave
	}
	if engine.catalogSaveError != nil || pending {
		engine.writeCatalogDiskLocked()
	}
	if engine.sourceSaveError != nil {
		engine.saveSourceRecordsLocked()
	}
	return engine.sourceStatusLocked(source)
}
