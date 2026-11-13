package core

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestMetadataProgressHidesOriginAndKeepsFullAddressInDiagnosticLog 验证「Metadata Progress Hides Origin And Keeps Full Address In Diagnostic Log」回归场景。
func TestMetadataProgressHidesOriginAndKeepsFullAddressInDiagnosticLog(t *testing.T) {
	e := sourceFixtureEngine(t, func(r *http.Request) (*http.Response, error) {
		t.Fatal("progress reporting must not request the network")
		return nil, nil
	})
	stage := "单地址暂存 1/4 · 普通分类 · 第2页；完成后替换目录缓存"
	origin := "https://metadata.example.org:8443"
	e.reportCatalogMetadataProgress(stage, origin)
	if got := e.sourceStatus(sourceHuangguoAI).Stage; got != stage || strings.Contains(got, origin) {
		t.Fatal(got)
	}
	path := filepath.Join(e.directory, "logs", "app.log")
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var event diagnosticEvent
	if json.Unmarshal(body, &event) != nil || event.Event != "catalog.metadata_progress" || event.Level != "info" || event.Source != sourceHuangguoAI || !strings.Contains(event.Message, origin) || !strings.Contains(event.Message, stage) {
		t.Fatal(string(body))
	}
}

// TestMetadataProgressPreservesRawSourceIdentityAndOtherState 验证「Metadata Progress Preserves Raw Source Identity And Other State」回归场景。
func TestMetadataProgressPreservesRawSourceIdentityAndOtherState(t *testing.T) {
	e := sourceFixtureEngine(t, func(r *http.Request) (*http.Response, error) {
		t.Fatal("progress reporting must not inspect source contents")
		return nil, nil
	})
	e.sourceRecords[sourceHuangguoAI] = nativeSourceRecord{CatalogOrigin: "https://keep.example.org", MetadataCursor: "keep", Completed: 2, Total: 4, Added: 3}
	e.reportCatalogMetadataProgress("检查第2个元数据地址", "https://next.example.org")
	r := e.sourceRecords[sourceHuangguoAI]
	if r.CatalogOrigin != "https://keep.example.org" || r.MetadataCursor != "keep" || r.Completed != 2 || r.Total != 4 || r.Added != 3 {
		t.Fatal(r)
	}
}
