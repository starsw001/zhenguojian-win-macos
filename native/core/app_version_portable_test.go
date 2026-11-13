package core

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// TestVersionFolderRelocation 验证整目录移动后下载和日志跟随新路径且测试文件字节不变。
func TestVersionFolderRelocation(t *testing.T) {
	parent := t.TempDir()
	root := filepath.Join(parent, "first-location")
	data := filepath.Join(root, "app-data")
	e, err := newNativeEngine(data, filepath.Join(root, "download"), filepath.Join(root, "logs"))
	if err != nil {
		t.Fatal(err)
	}
	if e.downloads.loadErr != nil {
		t.Fatal(e.downloads.loadErr)
	}
	file := filepath.Join(e.downloads.root, "neutral-fixture.bin")
	if err := os.WriteFile(file, []byte("fixture bytes unchanged"), 0600); err != nil {
		t.Fatal(err)
	}
	settings := e.resourceSettings()
	settings.LogDirectory = filepath.Join(root, "logs", "selected")
	if _, err := e.saveResourceSettings(settings); err != nil {
		t.Fatal(err)
	}
	e.downloads.close()
	locationBody, _ := os.ReadFile(filepath.Join(data, "download-location.json"))
	var location map[string]string
	if json.Unmarshal(locationBody, &location) != nil || location["directory"] != "download" || location["mode"] != "portable" {
		t.Fatal("download path is not relative", string(locationBody))
	}
	settingsBody, _ := os.ReadFile(filepath.Join(data, "resource-settings.json"))
	var stored nativeResourceSettings
	if json.Unmarshal(settingsBody, &stored) != nil || stored.LogDirectory != "logs/selected" {
		t.Fatal("log path is not relative", string(settingsBody))
	}
	next := filepath.Join(parent, "moved-location")
	if err := os.Rename(root, next); err != nil {
		t.Fatal(err)
	}
	reopened, err := newNativeEngine(filepath.Join(next, "app-data"), filepath.Join(next, "download"), filepath.Join(next, "logs"))
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.downloads.close()
	if reopened.downloads.loadErr != nil || reopened.downloads.root != filepath.Join(next, "download") {
		t.Fatal(reopened.downloads.root, reopened.downloads.loadErr)
	}
	if reopened.resourceSettings().LogDirectory != filepath.Join(next, "logs", "selected") {
		t.Fatal(reopened.resourceSettings())
	}
	if body, err := os.ReadFile(filepath.Join(next, "download", "neutral-fixture.bin")); err != nil || string(body) != "fixture bytes unchanged" {
		t.Fatal("fixture file changed", err)
	}
	if _, err := os.Stat(root); !os.IsNotExist(err) {
		t.Fatal("old location recreated", err)
	}
}

// TestVersionFolderRejectsExternalStorage 验证便携版不通过配置访问外部下载或日志目录。
func TestVersionFolderRejectsExternalStorage(t *testing.T) {
	root, outside := t.TempDir(), t.TempDir()
	data := filepath.Join(root, "app-data")
	e, err := newNativeEngine(data, filepath.Join(root, "download"), filepath.Join(root, "logs"))
	if err != nil {
		t.Fatal(err)
	}
	defer e.downloads.close()
	settings := e.resourceSettings()
	settings.LogDirectory = outside
	if _, err := e.saveResourceSettings(settings); err == nil {
		t.Fatal("external log path accepted")
	}
	if err := e.moveDownloads(context.Background(), outside); err == nil {
		t.Fatal("external download migration accepted")
	}
	body, _ := json.Marshal(map[string]string{"directory": outside, "mode": "custom"})
	if err := os.WriteFile(filepath.Join(data, "download-location.json"), body, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := nativePortableDownloadLocation(data); err == nil {
		t.Fatal("external download config accepted")
	}
	entries, err := os.ReadDir(outside)
	if err != nil || len(entries) != 0 {
		t.Fatal("external directory modified", err)
	}
}
