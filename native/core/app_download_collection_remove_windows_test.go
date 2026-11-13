package core

import (
	"context"
	"os"
	"syscall"
	"testing"
)

// TestNativeCollectionRemovalLockedSpecialKeepsRecordForRetry 验证「Native Collection Removal Locked Special Keeps Record For Retry」回归场景。
func TestNativeCollectionRemovalLockedSpecialKeepsRecordForRetry(t *testing.T) {
	manager, drama, id, files := collectionRemovalFixture(t)
	before, _ := os.ReadFile(files.index)
	handle, err := syscall.CreateFile(syscall.StringToUTF16Ptr(files.special), syscall.GENERIC_READ, syscall.FILE_SHARE_READ, nil, syscall.OPEN_EXISTING, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manager.engine.removeDownloadCollection(context.Background(), drama); err == nil {
		_ = syscall.CloseHandle(handle)
		t.Fatal("locked file reported deleted")
	}
	after, _ := os.ReadFile(files.index)
	if manager.jobs[id] == nil || !collectionExists(files.special) || string(before) != string(after) {
		_ = syscall.CloseHandle(handle)
		t.Fatal("retry records were lost")
	}
	_ = syscall.CloseHandle(handle)
	if _, err := manager.engine.removeDownloadCollection(context.Background(), drama); err != nil {
		t.Fatal("unlocked retry failed", err)
	}
	if collectionExists(files.directory) || manager.jobs[id] != nil {
		t.Fatal("retry left files or task")
	}
}
