package licensing

import (
	"os"
	"path/filepath"
	"testing"
)

// TestPortableLicenseStateAndEditableData 验证授权缓存随版本隔离且修改数据不触发运行文件锁定。
func TestPortableLicenseStateAndEditableData(t *testing.T) {
	f := newFixture(t)
	f.config.Storage = filepath.Join(f.config.Root, "app-data", "licenses", f.config.Edition)
	m := Open(f.config)
	m.Login("fixture1@example.invalid", "Abc123@#")
	if s := m.Activate("TEST-VALID"); !s.Ready {
		t.Fatal(s)
	}
	file := filepath.Join(f.config.Root, "app-data", "sources.json")
	if err := os.WriteFile(file, []byte(`{"manual":"current-version"}`), 0600); err != nil {
		t.Fatal(err)
	}
	if s := Open(f.config).Poll(true); !s.Ready {
		t.Fatal("manual data change locked runtime", s)
	}
	next := f.config
	next.Storage = filepath.Join(t.TempDir(), "app-data", "licenses", next.Edition)
	if s := Open(next).Poll(true); s.Ready || s.Phase != "login" {
		t.Fatal("fresh version imported previous login state", s)
	}
	if s := Open(f.config).Poll(true); !s.Ready {
		t.Fatal("previous state altered", s)
	}
	if body, err := os.ReadFile(file); err != nil || string(body) != `{"manual":"current-version"}` {
		t.Fatal("manual data changed", err)
	}
}
