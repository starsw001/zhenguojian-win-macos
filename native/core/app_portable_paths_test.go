package core

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestPortableLogsDefaultCustomRestartAndRestore 验证「Portable Logs Default Custom Restart And Restore」回归场景。
func TestPortableLogsDefaultCustomRestartAndRestore(t *testing.T) {
	data, program, custom := t.TempDir(), t.TempDir(), t.TempDir()
	defaults := filepath.Join(program, "logs")
	e, err := newNativeEngine(data, filepath.Join(program, "download"), defaults)
	if err != nil {
		t.Fatal(err)
	}
	defer e.downloads.close()
	e.downloader.recordDiagnostic(diagnosticEvent{Event: "portable-test", Level: "info", Message: "neutral"})
	before, err := os.ReadFile(filepath.Join(defaults, "app.log"))
	if err != nil {
		t.Fatal(err)
	}
	s := e.resourceSettings()
	if s.LogDirectory != "" || s.LogPath != filepath.Join(defaults, "app.log") || s.DefaultLogDirectory != defaults {
		t.Fatal(s)
	}
	s.LogDirectory = custom
	s, err = e.saveResourceSettings(s)
	if err != nil {
		t.Fatal(err)
	}
	if s.LogDirectory != custom || s.LogPath != filepath.Join(custom, "app.log") {
		t.Fatal(s)
	}
	e.downloader.recordDiagnostic(diagnosticEvent{Event: "custom-test", Level: "info", Message: "neutral"})
	if _, err = os.Stat(filepath.Join(custom, "app.log")); err != nil {
		t.Fatal(err)
	}
	current, _ := os.ReadFile(filepath.Join(defaults, "app.log"))
	if string(current) != string(before) {
		t.Fatal("old logs moved or overwritten")
	}
	other, err := newNativeEngine(data, filepath.Join(program, "download"), filepath.Join(t.TempDir(), "logs"))
	if err != nil {
		t.Fatal(err)
	}
	defer other.downloads.close()
	if other.downloader.diagnostics.filePath() != filepath.Join(custom, "app.log") {
		t.Fatal("explicit log path lost after restart")
	}
	s.LogDirectory = ""
	s, err = e.saveResourceSettings(s)
	if err != nil {
		t.Fatal(err)
	}
	if s.LogPath != filepath.Join(defaults, "app.log") {
		t.Fatal("default not restored", s)
	}
	if _, err = os.Stat(filepath.Join(custom, "app.log")); err != nil {
		t.Fatal("old custom log was deleted", err)
	}
}

// TestPortableLogsRejectInvalidOrBlockedPathWithoutChangingSettings 验证「Portable Logs Reject Invalid Or Blocked Path Without Changing Settings」回归场景。
func TestPortableLogsRejectInvalidOrBlockedPathWithoutChangingSettings(t *testing.T) {
	e, err := newNativeEngine(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer e.downloads.close()
	s := e.resourceSettings()
	if _, err = e.saveResourceSettings(s); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(e.directory, "resource-settings.json")
	before, _ := os.ReadFile(file)
	old := e.downloader.diagnostics.filePath()
	blocked := filepath.Join(t.TempDir(), "file")
	if err = os.WriteFile(blocked, []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"relative-directory", filepath.Join(blocked, "logs")} {
		s.LogDirectory = path
		if _, err = e.saveResourceSettings(s); err == nil {
			t.Fatal("invalid directory accepted", path)
		}
		after, _ := os.ReadFile(file)
		if string(after) != string(before) || e.downloader.diagnostics.filePath() != old {
			t.Fatal("failed save changed previous configuration")
		}
	}
}

// TestPortableDefaultDownloadDoesNotHideOldRecords 验证「Portable Default Download Does Not Hide Old Records」回归场景。
func TestPortableDefaultDownloadDoesNotHideOldRecords(t *testing.T) {
	data, old, current := t.TempDir(), t.TempDir(), t.TempDir()
	video := filepath.Join(old, "keep.mp4")
	if err := os.WriteFile(video, []byte("preserve"), 0600); err != nil {
		t.Fatal(err)
	}
	body, _ := json.Marshal(map[string]string{"directory": old, "mode": "default"})
	if err := os.WriteFile(filepath.Join(data, "download-location.json"), body, 0600); err != nil {
		t.Fatal(err)
	}
	if nativeDownloadLocation(data, current) != old {
		t.Fatal("existing videos hidden by automatic default rebase")
	}
	if err := os.WriteFile(filepath.Join(current, "index.json"), []byte(`{"jobs":[]}`), 0600); err != nil {
		t.Fatal(err)
	}
	if nativeDownloadLocation(data, current) != current {
		t.Fatal("copied software download index not followed")
	}
	stored, _ := os.ReadFile(video)
	if string(stored) != "preserve" {
		t.Fatal("old video changed")
	}
}

// TestPortableLogsUnwritableDefaultIsNotSilentlyRedirected 验证「Portable Logs Unwritable Default Is Not Silently Redirected」回归场景。
func TestPortableLogsUnwritableDefaultIsNotSilentlyRedirected(t *testing.T) {
	blocked := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(blocked, []byte("preserve"), 0600); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(blocked, "logs")
	e, err := newNativeEngine(t.TempDir(), "", dir)
	if err != nil {
		t.Fatal(err)
	}
	defer e.downloads.close()
	s := e.resourceSettings()
	if s.LogPath != filepath.Join(dir, "app.log") || !strings.Contains(s.Warning, "日志目录不可写") {
		t.Fatal(s)
	}
}
