package secureupdate

import (
	"archive/zip"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestPortableUpdateCache 验证更新缓存保留在各自版本目录内。
func TestPortableUpdateCache(t *testing.T) {
	for _, edition := range []string{"hongguoshang", "zhenguoshang"} {
		root := filepath.Join(t.TempDir(), edition+"-1.1.37+168-windows-x64")
		i, err := Locate(root, edition, "fixture-id", "fixture-key", "1.1.37+168")
		if err != nil || i.Cache != filepath.Join(root, "app-data", "updates") {
			t.Fatal(i.Cache, err)
		}
		if err := i.Init(); err != nil {
			t.Fatal(err)
		}
	}
}

// TestPortableMutableDataAndRuntimeIntegrity 验证可编辑数据不触发完整性锁定且不进入更新包。
func TestPortableMutableDataAndRuntimeIntegrity(t *testing.T) {
	f := newFixture(t, "zhenguoshang", nil)
	for _, name := range []string{"app-data", "download", "logs"} {
		dir := filepath.Join(f.i.Root, name)
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "private.json"), []byte(`{"keep":"private"}`), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if err := VerifyTree(f.i.Root, f.i.Key, f.i.Edition, f.i.ID, f.i.Base, true); err != nil {
		t.Fatal("editable data rejected", err)
	}
	r, err := Publish(f.i.Root, f.releaseDir, f.key, f.i.Key, f.i.Edition, f.i.ID, f.i.Base)
	if err != nil {
		t.Fatal(err)
	}
	z, err := zip.OpenReader(filepath.Join(f.releaseDir, r.Archive))
	if err != nil {
		t.Fatal(err)
	}
	defer z.Close()
	for _, entry := range z.File {
		name, _, valid := archiveEntryName(entry.Name, r)
		if !valid || strings.HasPrefix(name, "app-data/") || strings.HasPrefix(name, "download/") || strings.HasPrefix(name, "logs/") {
			t.Fatal("private runtime data included", entry.Name)
		}
	}
	if err := os.WriteFile(filepath.Join(f.i.Root, "duanju_core.dll"), []byte("tampered"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := VerifyTree(f.i.Root, f.i.Key, f.i.Edition, f.i.ID, f.i.Base, true); err == nil {
		t.Fatal("tampered runtime accepted")
	}
}
