package core

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// storageFixture 返回存储Fixture。
func storageFixture(t *testing.T) (*nativeEngine, string) {
	t.Helper()
	engine, err := newNativeEngine(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(engine.downloads.close)
	id := nativeDownloadID("hongguo:70001", 1)
	manager := engine.downloads
	if err := os.MkdirAll(filepath.Join(manager.root, id), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(manager.root, id, "media.mp4"), []byte("synthetic-video"), 0600); err != nil {
		t.Fatal(err)
	}
	manager.jobs[id] = &nativeDownloadRecord{nativeDownloadJob: nativeDownloadJob{
		ID: id, Index: 1, Drama: nativeDrama{ID: "hongguo:70001", Source: "hongguo"},
		State: "completed", Bytes: 15}, File: "media.mp4"}
	if err := manager.saveLocked(); err != nil {
		t.Fatal(err)
	}
	return engine, id
}

// TestDownloadMigrationPreservesMediaAndRestartLocation 验证「Download Migration Preserves Media And Restart Location」回归场景。
func TestDownloadMigrationPreservesMediaAndRestartLocation(t *testing.T) {
	engine, id := storageFixture(t)
	old := engine.downloads.root
	target := t.TempDir()
	if err := engine.moveDownloads(context.Background(), target); err != nil {
		t.Fatal(err)
	}
	location := nativeDownloadLocation(engine.directory)
	if location != engine.downloads.root || location == old {
		t.Fatal("new location was not persisted")
	}
	if _, err := os.Stat(old); !os.IsNotExist(err) {
		t.Fatal("old media not cleaned after successful migration")
	}
	if body, err := os.ReadFile(filepath.Join(location, id, "media.mp4")); err != nil || string(body) != "synthetic-video" {
		t.Fatalf("media not copied: %v", err)
	}
	info, err := engine.storage()
	if err != nil || info.Files < 2 || info.Bytes < 15 || info.Free < 1 {
		t.Fatalf("storage stats: %+v %v", info, err)
	}
	restarted := newNativeDownloads(engine)
	defer restarted.close()
	if len(restarted.jobs) != 1 || restarted.jobs[id].State != "completed" {
		t.Fatal("restart lost jobs")
	}
}

// TestMigrationFailureLeavesSourceAndExistingTargetIntact 验证「Migration Failure Leaves Source And Existing Target Intact」回归场景。
func TestMigrationFailureLeavesSourceAndExistingTargetIntact(t *testing.T) {
	engine, _ := storageFixture(t)
	old := engine.downloads.root
	parent := t.TempDir()
	existing := filepath.Join(parent, "zhenguoshang-downloads")
	os.Mkdir(existing, 0700)
	os.WriteFile(filepath.Join(existing, "user-file"), []byte("keep"), 0600)
	if err := engine.moveDownloads(context.Background(), parent); err == nil {
		t.Fatal("existing directory overwritten")
	}
	if _, err := os.Stat(filepath.Join(existing, "user-file")); err != nil {
		t.Fatal("existing content removed")
	}
	if engine.downloads.root != old {
		t.Fatal("failed migration changed root")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	empty := t.TempDir()
	if err := engine.moveDownloads(ctx, empty); err == nil {
		t.Fatal("cancelled migration succeeded")
	}
	if _, err := os.Stat(old); err != nil {
		t.Fatal("source removed after cancellation")
	}
	if _, err := os.Stat(filepath.Join(empty, "zhenguoshang-downloads")); !os.IsNotExist(err) {
		t.Fatal("partial target not cleaned")
	}
}

// TestExactDownloadMigrationPreservesIndependentEpisodes 验证「Exact Download Migration Preserves Independent Episodes」回归场景。
func TestExactDownloadMigrationPreservesIndependentEpisodes(t *testing.T) {
	for _, existingEmpty := range []bool{false, true} {
		t.Run(map[bool]string{false: "new-directory", true: "empty-directory"}[existingEmpty], func(t *testing.T) {
			engine, id := storageFixture(t)
			target := filepath.Join(t.TempDir(), "download")
			if existingEmpty {
				if err := os.Mkdir(target, 0700); err != nil {
					t.Fatal(err)
				}
			}
			if err := engine.moveDownloads(context.Background(), target, true); err != nil {
				t.Fatal(err)
			}
			if engine.downloads.root != target || nativeDownloadLocation(engine.directory) != target {
				t.Fatal("exact target was not persisted")
			}
			body, err := os.ReadFile(filepath.Join(target, id, "media.mp4"))
			if err != nil || string(body) != "synthetic-video" {
				t.Fatalf("independent episode changed: %v", err)
			}
			if err := engine.moveDownloads(context.Background(), target, true); err != nil {
				t.Fatal("reusing exact directory failed", err)
			}
		})
	}
}

// TestExactDownloadMigrationRejectsExistingUserFiles 验证「Exact Download Migration Rejects Existing User Files」回归场景。
func TestExactDownloadMigrationRejectsExistingUserFiles(t *testing.T) {
	engine, _ := storageFixture(t)
	old := engine.downloads.root
	target := t.TempDir()
	file := filepath.Join(target, "keep.mp4")
	if err := os.WriteFile(file, []byte("user-video"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := engine.moveDownloads(context.Background(), target, true); err == nil {
		t.Fatal("nonempty exact target accepted")
	}
	body, err := os.ReadFile(file)
	if err != nil || string(body) != "user-video" || engine.downloads.root != old {
		t.Fatal("existing user files or download root changed")
	}
}

// TestMediaLeasePreventsMigrationAndDeletingSourceFiles 验证「Media Lease Prevents Migration And Deleting Source Files」回归场景。
func TestMediaLeasePreventsMigrationAndDeletingSourceFiles(t *testing.T) {
	engine, id := storageFixture(t)
	if _, err := engine.workLease("media", "start"); err != nil {
		t.Fatal(err)
	}
	if _, err := engine.workLease("media", "start"); err == nil {
		t.Fatal("parallel media operations accepted")
	}
	if err := engine.moveDownloads(context.Background(), t.TempDir()); err == nil {
		t.Fatal("moving busy media")
	}
	if err := engine.downloads.control(id, "remove"); err == nil {
		t.Fatal("deleting active input")
	}
	if _, err := engine.workLease("media", "end"); err != nil {
		t.Fatal(err)
	}
	if err := engine.downloads.control(id, "remove"); err != nil {
		t.Fatal(err)
	}
}

// TestMigrationRejectsContainmentAndSymbolicLinks 验证「Migration Rejects Containment And Symbolic Links」回归场景。
func TestMigrationRejectsContainmentAndSymbolicLinks(t *testing.T) {
	engine, _ := storageFixture(t)
	if err := engine.moveDownloads(context.Background(), engine.downloads.root); err == nil {
		t.Fatal("nested destination accepted")
	}
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(engine.downloads.root, "link")); err != nil {
		t.Skip(err)
	}
	if err := engine.moveDownloads(context.Background(), t.TempDir()); err == nil {
		t.Fatal("followed symlink")
	}
	if _, err := os.Stat(outside); err != nil {
		t.Fatal("removed linked directory")
	}
}
