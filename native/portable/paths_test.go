package portable

import (
	"os"
	"path/filepath"
	"testing"
)

// TestVersionDataDirectories 验证不同版本的数据路径隔离且拒绝相对路径。
func TestVersionDataDirectories(t *testing.T) {
	root := t.TempDir()
	first, err := DataDirectory(filepath.Join(root, "edition-167"))
	if err != nil {
		t.Fatal(err)
	}
	next, err := DataDirectory(filepath.Join(root, "edition-168"))
	if err != nil || first == next || next != filepath.Join(root, "edition-168", "app-data") {
		t.Fatal(next, err)
	}
	if err := EnsureDirectory(next); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(first); !os.IsNotExist(err) {
		t.Fatal("old data directory was touched", err)
	}
	if _, err := DataDirectory("relative"); err == nil {
		t.Fatal("relative root accepted")
	}
	for _, name := range []string{"app-data", "download", "logs", "APP-DATA"} {
		if !MutableDirectory(name) {
			t.Fatal(name)
		}
	}
	for _, name := range []string{"data", "app-data.exe", "app-data/other"} {
		if MutableDirectory(name) {
			t.Fatal(name)
		}
	}
}

// TestBlockedDataDirectory 验证目录不可用时不回退到系统目录。
func TestBlockedDataDirectory(t *testing.T) {
	root := t.TempDir()
	path, err := DataDirectory(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("unchanged"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := EnsureDirectory(path); err == nil {
		t.Fatal("blocked path accepted")
	}
	body, _ := os.ReadFile(path)
	if string(body) != "unchanged" {
		t.Fatal("blocker changed")
	}
}

// TestLinkedDataDirectory 验证链接不能把当前版本数据重定向到外部。
func TestLinkedDataDirectory(t *testing.T) {
	root, outside := t.TempDir(), t.TempDir()
	if err := os.Symlink(outside, filepath.Join(root, "app-data")); err != nil {
		t.Skip("system does not permit symlink creation", err)
	}
	if _, err := DataDirectory(root); err == nil {
		t.Fatal("linked data accepted")
	}
}
