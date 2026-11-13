package core

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// TestFirstInstallDownloadDirectoryCreatedAndDefaultCanFollowProgram 验证「First Install Download Directory Created And Default Can Follow Program」回归场景。
func TestFirstInstallDownloadDirectoryCreatedAndDefaultCanFollowProgram(t *testing.T) {
	data, install := t.TempDir(), t.TempDir()
	target := filepath.Join(install, "download")
	engine, err := newNativeEngine(data, target)
	if err != nil {
		t.Fatal(err)
	}
	defer engine.downloads.close()
	if engine.downloads.loadErr != nil || engine.downloads.root != target {
		t.Fatalf("initial download location: %s %v", engine.downloads.root, engine.downloads.loadErr)
	}
	if info, err := os.Stat(target); err != nil || !info.IsDir() {
		t.Fatalf("download directory not created: %v", err)
	}
	moved := filepath.Join(t.TempDir(), "download")
	if nativeDownloadLocation(data) != target || nativeDownloadLocation(data, moved) != moved {
		t.Fatal("default download location did not follow a moved program directory")
	}
}

// TestSavedAndLegacyDownloadsTakePrecedence 验证「Saved And Legacy Downloads Take Precedence」回归场景。
func TestSavedAndLegacyDownloadsTakePrecedence(t *testing.T) {
	for _, saved := range []bool{false, true} {
		t.Run(map[bool]string{false: "legacy", true: "user-chosen"}[saved], func(t *testing.T) {
			data := t.TempDir()
			old := filepath.Join(data, "downloads")
			if saved {
				old = filepath.Join(t.TempDir(), "my-videos")
				body, _ := json.Marshal(map[string]string{"directory": old})
				if err := os.WriteFile(filepath.Join(data, "download-location.json"), body, 0600); err != nil {
					t.Fatal(err)
				}
			}
			if err := os.MkdirAll(old, 0700); err != nil {
				t.Fatal(err)
			}
			video := filepath.Join(old, "keep.mp4")
			if err := os.WriteFile(video, []byte("original-video"), 0600); err != nil {
				t.Fatal(err)
			}
			requested := filepath.Join(t.TempDir(), "download")
			engine, err := newNativeEngine(data, requested)
			if err != nil {
				t.Fatal(err)
			}
			defer engine.downloads.close()
			if engine.downloads.root != old || nativeDownloadLocation(data) != old {
				t.Fatal("existing download location changed")
			}
			if body, err := os.ReadFile(video); err != nil || string(body) != "original-video" {
				t.Fatal("original video changed")
			}
			if _, err := os.Stat(requested); !os.IsNotExist(err) {
				t.Fatal("unused program download directory created")
			}
		})
	}
}

// TestMalformedSavedLocationIsNotOverwritten 验证「Malformed Saved Location Is Not Overwritten」回归场景。
func TestMalformedSavedLocationIsNotOverwritten(t *testing.T) {
	data := t.TempDir()
	file := filepath.Join(data, "download-location.json")
	body := []byte("{broken saved location")
	if err := os.WriteFile(file, body, 0600); err != nil {
		t.Fatal(err)
	}
	engine, err := newNativeEngine(data, filepath.Join(t.TempDir(), "download"))
	if err != nil {
		t.Fatal(err)
	}
	defer engine.downloads.close()
	current, err := os.ReadFile(file)
	if err != nil || string(current) != string(body) {
		t.Fatal("broken original setting overwritten")
	}
	if engine.downloads.root != filepath.Join(data, "downloads") {
		t.Fatal("malformed setting silently switched directory")
	}
}

// TestMissingUnwritableFirstDefaultCanSelectExactDirectory 验证「Missing Unwritable First Default Can Select Exact Directory」回归场景。
func TestMissingUnwritableFirstDefaultCanSelectExactDirectory(t *testing.T) {
	data := t.TempDir()
	blocked := filepath.Join(t.TempDir(), "not-a-directory")
	if err := os.WriteFile(blocked, []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	requested := filepath.Join(blocked, "download")
	engine, err := newNativeEngine(data, requested)
	if err != nil {
		t.Fatal(err)
	}
	defer engine.downloads.close()
	if engine.downloads.loadErr == nil {
		t.Fatal("unwritable initial directory accepted")
	}
	target := t.TempDir()
	if err := engine.moveDownloads(context.Background(), target, true); err != nil {
		t.Fatal(err)
	}
	if engine.downloads.loadErr != nil || engine.downloads.root != target || nativeDownloadLocation(data) != target {
		t.Fatal("exact alternative was not persisted")
	}
	if body, err := os.ReadFile(blocked); err != nil || string(body) != "keep" {
		t.Fatal("unrelated file changed")
	}
}

// TestCorruptExistingIndexStillBlocksMigration 验证「Corrupt Existing Index Still Blocks Migration」回归场景。
func TestCorruptExistingIndexStillBlocksMigration(t *testing.T) {
	data := t.TempDir()
	root := filepath.Join(t.TempDir(), "download")
	if err := os.Mkdir(root, 0700); err != nil {
		t.Fatal(err)
	}
	index := filepath.Join(root, "index.json")
	body := []byte("broken-index")
	if err := os.WriteFile(index, body, 0600); err != nil {
		t.Fatal(err)
	}
	engine, err := newNativeEngine(data, root)
	if err != nil {
		t.Fatal(err)
	}
	defer engine.downloads.close()
	if engine.downloads.loadErr == nil {
		t.Fatal("corrupt index accepted")
	}
	if err := engine.moveDownloads(context.Background(), t.TempDir(), true); err == nil {
		t.Fatal("corrupt index migrated")
	}
	current, err := os.ReadFile(index)
	if err != nil || string(current) != string(body) {
		t.Fatal("corrupt original index changed")
	}
}
