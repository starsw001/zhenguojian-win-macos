package core

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

type collectionRemovalFiles struct{ special, merged, episode, directory, index string }

// collectionRemovalFixture 返回合集RemovalFixture。
func collectionRemovalFixture(t *testing.T) (*nativeDownloads, nativeDrama, string, collectionRemovalFiles) {
	t.Helper()
	manager, input := publicationFixture(t)
	job := manager.jobs[input.JobID].nativeDownloadJob
	if err := manager.publish(context.Background(), input); err != nil {
		t.Fatal(err)
	}
	episode := filepath.Join(manager.root, filepath.FromSlash(input.PublishedFile))
	special := filepath.Join(filepath.Dir(episode), "S00E001.mkv")
	merged := filepath.Join(manager.root, "library", "merged-fixture", "full.mkv")
	if err := os.WriteFile(special, []byte("special"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(merged), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(merged, []byte("merged"), 0600); err != nil {
		t.Fatal(err)
	}
	data := map[string]any{"items": []any{map[string]any{"id": "export-" + job.ID, "drama": job.Drama, "file": input.PublishedFile, "kind": "export", "bytes": input.PublishedBytes}, map[string]any{"id": "special-merged-fixture", "drama": job.Drama, "file": filepath.ToSlash(special[len(manager.root)+1:]), "kind": "special", "bytes": 7}, map[string]any{"id": "merged-fixture", "drama": job.Drama, "file": "library/merged-fixture/full.mkv", "kind": "merged", "bytes": 6}}, "showFolders": map[string]string{job.Drama.ID: filepath.ToSlash(filepath.Dir(input.PublishedFile))}, "specialNumbers": map[string]int{job.Drama.ID + "\x00merged-fixture": 1, "other\x00merged-keep": 2}, "skipped": []string{"old-keep"}, "extension": map[string]string{"keep": "unchanged"}}
	body, _ := json.Marshal(data)
	index := filepath.Join(manager.root, "media-library.json")
	if err := os.WriteFile(index, body, 0600); err != nil {
		t.Fatal(err)
	}
	return manager, job.Drama, job.ID, collectionRemovalFiles{special, merged, episode, filepath.Dir(episode), index}
}

// collectionExists 判断合集Exists是否满足当前规则。
func collectionExists(p string) bool { _, err := os.Stat(p); return err == nil }

// TestNativeCollectionRemovalClearsEpisodesMergedSpecialAndFolders 验证「Native Collection Removal Clears Episodes Merged Special And Folders」回归场景。
func TestNativeCollectionRemovalClearsEpisodesMergedSpecialAndFolders(t *testing.T) {
	manager, drama, id, files := collectionRemovalFixture(t)
	result, err := manager.engine.removeDownloadCollection(context.Background(), drama)
	if err != nil || len(result.Completed) != 1 || result.Completed[0] != id {
		t.Fatal(result, err)
	}
	for _, file := range []string{files.special, files.merged, files.episode, files.directory, filepath.Dir(files.merged)} {
		if collectionExists(file) {
			t.Fatal("collection output remains", file)
		}
	}
	if !collectionExists(filepath.Join(manager.root, "exports")) || !collectionExists(filepath.Join(manager.root, "library")) {
		t.Fatal("media root removed")
	}
	var data map[string]json.RawMessage
	body, _ := os.ReadFile(files.index)
	_ = json.Unmarshal(body, &data)
	if string(data["items"]) != "[]" || string(data["extension"]) != `{"keep":"unchanged"}` || string(data["skipped"]) != `["old-keep"]` {
		t.Fatal(string(body))
	}
	var numbers map[string]int
	_ = json.Unmarshal(data["specialNumbers"], &numbers)
	if len(numbers) != 1 || numbers["other\x00merged-keep"] != 2 {
		t.Fatal(numbers)
	}
	if len(manager.jobs) != 0 {
		t.Fatal("download records remain")
	}
}

// TestNativeCollectionRemovalHandlesAlreadyDeletedTasksAndMissingOriginals 验证「Native Collection Removal Handles Already Deleted Tasks And Missing Originals」回归场景。
func TestNativeCollectionRemovalHandlesAlreadyDeletedTasksAndMissingOriginals(t *testing.T) {
	manager, drama, _, files := collectionRemovalFixture(t)
	manager.jobs = map[string]*nativeDownloadRecord{}
	_ = os.Remove(files.episode)
	_ = os.Remove(files.merged)
	if _, err := manager.engine.removeDownloadCollection(context.Background(), drama); err != nil {
		t.Fatal(err)
	}
	if collectionExists(files.special) || collectionExists(files.directory) {
		t.Fatal("orphan special remains")
	}
	if _, err := manager.engine.removeDownloadCollection(context.Background(), drama); err != nil {
		t.Fatal("retry not idempotent", err)
	}
}

// TestNativeCollectionRemovalPreservesUnknownFilesAndAllRetryRecords 验证「Native Collection Removal Preserves Unknown Files And All Retry Records」回归场景。
func TestNativeCollectionRemovalPreservesUnknownFilesAndAllRetryRecords(t *testing.T) {
	for _, name := range []string{"notes.txt", ".unfinished.part", "S01E099.mp4"} {
		t.Run(name, func(t *testing.T) {
			manager, drama, id, files := collectionRemovalFixture(t)
			before, _ := os.ReadFile(files.index)
			keep := filepath.Join(files.directory, name)
			_ = os.WriteFile(keep, []byte("keep"), 0600)
			if _, err := manager.engine.removeDownloadCollection(context.Background(), drama); err == nil {
				t.Fatal("unknown file not rejected")
			}
			after, _ := os.ReadFile(files.index)
			if string(before) != string(after) || manager.jobs[id] == nil || !collectionExists(files.special) || !collectionExists(files.episode) || !collectionExists(keep) {
				t.Fatal("preflight changed protected data")
			}
		})
	}
}

// TestNativeCollectionRemovalPreservesUnrelatedMediaAndDownloadTasks 验证「Native Collection Removal Preserves Unrelated Media And Download Tasks」回归场景。
func TestNativeCollectionRemovalPreservesUnrelatedMediaAndDownloadTasks(t *testing.T) {
	manager, drama, _, files := collectionRemovalFixture(t)
	other := nativeDownloadJob{ID: nativeDownloadID("hongguo:other", 1), Drama: nativeDrama{ID: "hongguo:other", Source: "hongguo", Title: "Other"}, Index: 1, State: "paused"}
	manager.jobs[other.ID] = &nativeDownloadRecord{nativeDownloadJob: other}
	body, _ := os.ReadFile(files.index)
	var data map[string]any
	_ = json.Unmarshal(body, &data)
	rows := data["items"].([]any)
	item := map[string]any{"id": "merged-other", "drama": other.Drama, "file": "library/merged-other/full.mkv", "kind": "merged", "bytes": 4}
	data["items"] = append(rows, item)
	body, _ = json.Marshal(data)
	_ = os.WriteFile(files.index, body, 0600)
	keep := filepath.Join(manager.root, "library", "merged-other", "full.mkv")
	_ = os.MkdirAll(filepath.Dir(keep), 0700)
	_ = os.WriteFile(keep, []byte("keep"), 0600)
	if _, err := manager.engine.removeDownloadCollection(context.Background(), drama); err != nil {
		t.Fatal(err)
	}
	if manager.jobs[other.ID] == nil || !collectionExists(keep) {
		t.Fatal("unrelated files or task removed")
	}
	body, _ = os.ReadFile(files.index)
	_ = json.Unmarshal(body, &data)
	rows = data["items"].([]any)
	if len(rows) != 1 || rows[0].(map[string]any)["id"] != "merged-other" {
		t.Fatal(string(body))
	}
}

// TestNativeCollectionRemovalIncludesArchivedAndPausedTasks 验证「Native Collection Removal Includes Archived And Paused Tasks」回归场景。
func TestNativeCollectionRemovalIncludesArchivedAndPausedTasks(t *testing.T) {
	manager, drama, _, _ := collectionRemovalFixture(t)
	other := nativeDownloadJob{ID: nativeDownloadID(drama.ID, 2), Drama: drama, Index: 2, State: "paused", Archived: true}
	manager.jobs[other.ID] = &nativeDownloadRecord{nativeDownloadJob: other}
	folder := manager.jobDirectory(other)
	_ = os.MkdirAll(folder, 0700)
	_ = os.WriteFile(filepath.Join(folder, "media.mp4"), []byte("raw"), 0600)
	result, err := manager.engine.removeDownloadCollection(context.Background(), drama)
	if err != nil || len(result.Completed) != 2 || len(manager.jobs) != 0 || collectionExists(folder) {
		t.Fatal(result, err)
	}
}

// TestNativeCollectionRemovalRejectsTraversalSharedFoldersAndInvalidIndex 验证「Native Collection Removal Rejects Traversal Shared Folders And Invalid Index」回归场景。
func TestNativeCollectionRemovalRejectsTraversalSharedFoldersAndInvalidIndex(t *testing.T) {
	for _, mode := range []string{"traversal", "shared", "invalid-specials", "invalid-json"} {
		t.Run(mode, func(t *testing.T) {
			manager, drama, id, files := collectionRemovalFixture(t)
			body, _ := os.ReadFile(files.index)
			var data map[string]any
			_ = json.Unmarshal(body, &data)
			switch mode {
			case "traversal":
				data["items"].([]any)[0].(map[string]any)["file"] = "../outside.mp4"
			case "shared":
				data["showFolders"].(map[string]any)["other"] = filepath.ToSlash(files.directory[len(manager.root)+1:])
			case "invalid-specials":
				data["specialNumbers"] = "invalid"
			case "invalid-json":
				data["items"] = "invalid"
			}
			body, _ = json.Marshal(data)
			_ = os.WriteFile(files.index, body, 0600)
			if _, err := manager.engine.removeDownloadCollection(context.Background(), drama); err == nil {
				t.Fatal("unsafe index accepted")
			}
			after, _ := os.ReadFile(files.index)
			if string(after) != string(body) || manager.jobs[id] == nil || !collectionExists(files.special) || !collectionExists(files.episode) {
				t.Fatal("invalid data changed protected files")
			}
		})
	}
}

// TestNativeCollectionRemovalWaitsForMediaTaskAndPartialDeletionKeepsSpecial 验证「Native Collection Removal Waits For Media Task And Partial Deletion Keeps Special」回归场景。
func TestNativeCollectionRemovalWaitsForMediaTaskAndPartialDeletionKeepsSpecial(t *testing.T) {
	manager, drama, id, files := collectionRemovalFixture(t)
	if _, err := manager.engine.workLease("media", "start"); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.engine.removeDownloadCollection(context.Background(), drama); err == nil {
		t.Fatal("active media task not protected")
	}
	_, _ = manager.engine.workLease("media", "end")
	if manager.jobs[id] == nil || !collectionExists(files.special) {
		t.Fatal("media job protection failed")
	}
	if _, err := manager.controlBatch([]string{id}, "remove"); err != nil {
		t.Fatal(err)
	}
	if !collectionExists(files.special) || !collectionExists(files.merged) {
		t.Fatal("per-episode removal deleted independent outputs")
	}
}

// TestNativeCollectionRemovalRejectsLinkedExport 验证「Native Collection Removal Rejects Linked Export」回归场景。
func TestNativeCollectionRemovalRejectsLinkedExport(t *testing.T) {
	manager, drama, id, files := collectionRemovalFixture(t)
	outside := filepath.Join(t.TempDir(), "outside.mkv")
	_ = os.WriteFile(outside, []byte("outside"), 0600)
	_ = os.Remove(files.special)
	if err := os.Symlink(outside, files.special); err != nil {
		t.Skip("symlink creation unavailable")
	}
	if _, err := manager.engine.removeDownloadCollection(context.Background(), drama); err == nil {
		t.Fatal("linked file accepted")
	}
	body, _ := os.ReadFile(outside)
	if string(body) != "outside" || manager.jobs[id] == nil {
		t.Fatal("linked target changed")
	}
}
