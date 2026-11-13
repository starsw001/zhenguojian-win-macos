package core

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestNativePublishedRemovalCleansOnlyEmptyShowDirectory 验证「Native Published Removal Cleans Only Empty Show Directory」回归场景。
func TestNativePublishedRemovalCleansOnlyEmptyShowDirectory(t *testing.T) {
	for _, extra := range []string{"", "S01E002.mp4", "notes.txt", ".pending.part"} {
		t.Run("remaining="+extra, func(t *testing.T) {
			manager, input := publicationFixture(t)
			if err := manager.publish(context.Background(), input); err != nil {
				t.Fatal(err)
			}
			file := filepath.Join(manager.root, filepath.FromSlash(input.PublishedFile))
			directory := filepath.Dir(file)
			if extra != "" {
				if err := os.WriteFile(filepath.Join(directory, extra), []byte("keep"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			if err := manager.control(input.JobID, "remove"); err != nil {
				t.Fatal(err)
			}
			if _, err := os.Stat(file); !os.IsNotExist(err) {
				t.Fatal("selected video remains", err)
			}
			_, err := os.Stat(directory)
			if extra == "" && !os.IsNotExist(err) {
				t.Fatal("empty show directory remains", err)
			}
			if extra != "" {
				if body, err := os.ReadFile(filepath.Join(directory, extra)); err != nil || string(body) != "keep" {
					t.Fatal("unrelated or incomplete file removed", err)
				}
			}
			if _, err := os.Stat(filepath.Join(manager.root, "exports")); err != nil {
				t.Fatal("exports root removed", err)
			}
		})
	}
}

// TestNativePublishedRemovalCleansAfterManualFileDeletion 验证「Native Published Removal Cleans After Manual File Deletion」回归场景。
func TestNativePublishedRemovalCleansAfterManualFileDeletion(t *testing.T) {
	manager, input := publicationFixture(t)
	if err := manager.publish(context.Background(), input); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(manager.root, filepath.FromSlash(input.PublishedFile))
	if err := os.Remove(file); err != nil {
		t.Fatal(err)
	}
	if err := manager.control(input.JobID, "remove"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Dir(file)); !os.IsNotExist(err) {
		t.Fatal("manually emptied directory remains", err)
	}
}

// TestNativePublishedEmptyCleanupRejectsLinkedDirectory 验证「Native Published Empty Cleanup Rejects Linked Directory」回归场景。
func TestNativePublishedEmptyCleanupRejectsLinkedDirectory(t *testing.T) {
	manager, input := publicationFixture(t)
	if err := manager.publish(context.Background(), input); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(manager.root, filepath.FromSlash(input.PublishedFile))
	directory := filepath.Dir(file)
	if err := os.Remove(file); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(directory); err != nil {
		t.Fatal(err)
	}
	outside := t.TempDir()
	if err := os.Symlink(outside, directory); err != nil {
		t.Skipf("symlink capability unavailable: %v", err)
	}
	if err := manager.removeEmptyPublishedDirectory(*manager.jobs[input.JobID]); err == nil || !strings.Contains(err.Error(), "链接") {
		t.Fatal("linked show directory accepted", err)
	}
	if _, err := os.Stat(outside); err != nil {
		t.Fatal("outside directory removed", err)
	}
	if _, err := os.Lstat(directory); err != nil {
		t.Fatal("directory link removed", err)
	}
}

// publicationFixture 返回地址发布页Fixture。
func publicationFixture(t *testing.T) (*nativeDownloads, nativeInput) {
	t.Helper()
	manager := downloadTestManager(t)
	drama := nativeDrama{ID: "hongguo:1", Source: "hongguo", Title: "Publication"}
	job := nativeDownloadJob{ID: nativeDownloadID(drama.ID, 1), Drama: drama, Chapter: Chapter{ID: "hongguo:1:1", Source: "hongguo"}, Index: 1, Created: 1, Bytes: 3, State: "completed", Revision: 1}
	manager.jobs[job.ID] = &nativeDownloadRecord{nativeDownloadJob: job, File: "media.mp4", Key: "00112233445566778899aabbccddeeff"}
	directory := manager.jobDirectory(job)
	if err := os.MkdirAll(directory, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, "media.mp4"), []byte("raw"), 0600); err != nil {
		t.Fatal(err)
	}
	identity := sha256.Sum256([]byte(drama.ID))
	relative := "exports/Publication [hongguo-" + hex.EncodeToString(identity[:])[:12] + "]/S01E001.mp4"
	full := filepath.Join(manager.root, filepath.FromSlash(relative))
	if err := os.MkdirAll(filepath.Dir(full), 0700); err != nil {
		t.Fatal(err)
	}
	body := []byte("fixture-cleartext-mp4")
	if err := os.WriteFile(full, body, 0600); err != nil {
		t.Fatal(err)
	}
	hash := sha256.Sum256(body)
	return manager, nativeInput{JobID: job.ID, PublishedFile: relative, PublishedBytes: int64(len(body)), PublishedSHA256: hex.EncodeToString(hash[:]), ExpectedVersions: map[string]string{job.ID: nativeDownloadVersion(job)}}
}

// TestNativeDownloadPublishedMP4SurvivesCacheRemovalAndRestart 验证「Native Download Published MP 4 Survives Cache Removal And Restart」回归场景。
func TestNativeDownloadPublishedMP4SurvivesCacheRemovalAndRestart(t *testing.T) {
	manager, input := publicationFixture(t)
	job := manager.jobs[input.JobID].nativeDownloadJob
	if err := manager.publish(context.Background(), input); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(manager.jobDirectory(job)); !os.IsNotExist(err) {
		t.Fatal("raw cache remains", err)
	}
	plan, found, err := manager.localPlan(job.Drama.ID, 1)
	if err != nil || !found || !plan.Local || plan.Key != "" || plan.URL != filepath.Join(manager.root, filepath.FromSlash(input.PublishedFile)) {
		t.Fatalf("invalid published plan: %+v %v", plan, err)
	}
	added, err := manager.enqueue(nativeInput{Drama: job.Drama, Force: true, Entries: []nativeDownloadEpisode{{Chapter: job.Chapter, Index: 1}}})
	if err != nil || added != 0 {
		t.Fatalf("duplicate episode queued: %d %v", added, err)
	}
	reloaded := &nativeDownloads{root: manager.root, engine: manager.engine, jobs: map[string]*nativeDownloadRecord{}, active: map[string]context.CancelFunc{}}
	if err := reloaded.load(); err != nil {
		t.Fatal(err)
	}
	if !reloaded.localFilesValid(*reloaded.jobs[input.JobID]) {
		t.Fatal("restart lost final MP4")
	}
	if err := reloaded.publish(context.Background(), input); err != nil {
		t.Fatal("idempotent publication failed", err)
	}
}

// TestNativeDownloadPublishRejectsUnsafeOrChangedFiles 验证「Native Download Publish Rejects Unsafe Or Changed Files」回归场景。
func TestNativeDownloadPublishRejectsUnsafeOrChangedFiles(t *testing.T) {
	for _, scenario := range []string{"hash", "bytes", "path", "version"} {
		t.Run(scenario, func(t *testing.T) {
			manager, input := publicationFixture(t)
			switch scenario {
			case "hash":
				input.PublishedSHA256 = "bad"
			case "bytes":
				input.PublishedBytes++
			case "path":
				input.PublishedFile = "../other.mp4"
			case "version":
				input.ExpectedVersions[input.JobID] = "stale"
			}
			if err := manager.publish(context.Background(), input); err == nil {
				t.Fatal("unsafe publication accepted")
			}
			job := manager.jobs[input.JobID]
			if _, err := os.Stat(manager.jobDirectory(job.nativeDownloadJob)); err != nil || job.PublishedFile != "" {
				t.Fatal("raw video or record changed on rejection")
			}
		})
	}
}

// TestNativeDownloadPublishIndexFailureKeepsRawFiles 验证「Native Download Publish Index Failure Keeps Raw Files」回归场景。
func TestNativeDownloadPublishIndexFailureKeepsRawFiles(t *testing.T) {
	manager, input := publicationFixture(t)
	if err := os.Mkdir(filepath.Join(manager.root, "index.json"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := manager.publish(context.Background(), input); err == nil {
		t.Fatal("failed index save accepted")
	}
	if manager.jobs[input.JobID].PublishedFile != "" {
		t.Fatal("uncommitted record retained")
	}
	if _, err := os.Stat(manager.jobDirectory(manager.jobs[input.JobID].nativeDownloadJob)); err != nil {
		t.Fatal("cache removed before durable index", err)
	}
}

// TestNativeDownloadPublishedGroupedCache 验证「Native Download Published Grouped Cache」回归场景。
func TestNativeDownloadPublishedGroupedCache(t *testing.T) {
	manager, input := publicationFixture(t)
	record := manager.jobs[input.JobID]
	old := manager.jobDirectory(record.nativeDownloadJob)
	record.Folder = ".download-cache/hongguo/" + record.ID
	directory := manager.jobDirectory(record.nativeDownloadJob)
	if err := os.MkdirAll(filepath.Dir(directory), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(old, directory); err != nil {
		t.Fatal(err)
	}
	if err := manager.publish(context.Background(), input); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(directory); !os.IsNotExist(err) {
		t.Fatal("grouped cache remains")
	}
	reloaded := &nativeDownloads{root: manager.root, jobs: map[string]*nativeDownloadRecord{}}
	if err := reloaded.load(); err != nil {
		t.Fatal(err)
	}
	if !reloaded.localFilesValid(*reloaded.jobs[input.JobID]) {
		t.Fatal("grouped record did not survive reload")
	}
}

// TestNativeDownloadPublishOnlyClearsOwnedManagedBackups 验证「Native Download Publish Only Clears Owned Managed Backups」回归场景。
func TestNativeDownloadPublishOnlyClearsOwnedManagedBackups(t *testing.T) {
	manager, input := publicationFixture(t)
	backup := filepath.Join(manager.root, ".replaced-downloads", input.JobID+"-fixture", "original")
	if err := os.MkdirAll(backup, 0700); err != nil {
		t.Fatal(err)
	}
	marker := sha256.Sum256([]byte("previous transport fingerprint"))
	if err := os.WriteFile(filepath.Join(backup, ".bundle"), []byte(hex.EncodeToString(marker[:])), 0600); err != nil {
		t.Fatal(err)
	}
	unrelated := filepath.Join(manager.root, ".replaced-downloads", "other-job")
	if err := os.Mkdir(unrelated, 0700); err != nil {
		t.Fatal(err)
	}
	if err := manager.publish(context.Background(), input); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(backup); !os.IsNotExist(err) {
		t.Fatal("owned cache retained")
	}
	if _, err := os.Stat(unrelated); err != nil {
		t.Fatal("unrelated cache removed", err)
	}
}

// TestNativeDownloadPublishDoesNotAdoptChangedCompletedContent 验证「Native Download Publish Does Not Adopt Changed Completed Content」回归场景。
func TestNativeDownloadPublishDoesNotAdoptChangedCompletedContent(t *testing.T) {
	manager, input := publicationFixture(t)
	if err := manager.publish(context.Background(), input); err != nil {
		t.Fatal(err)
	}
	original := manager.jobs[input.JobID].PublishedSHA256
	file := filepath.Join(manager.root, filepath.FromSlash(input.PublishedFile))
	body := make([]byte, input.PublishedBytes)
	if err := os.WriteFile(file, body, 0600); err != nil {
		t.Fatal(err)
	}
	changed := sha256.Sum256(body)
	input.PublishedSHA256 = hex.EncodeToString(changed[:])
	if err := manager.publish(context.Background(), input); err == nil {
		t.Fatal("changed completed content accepted as identical")
	}
	if manager.jobs[input.JobID].PublishedSHA256 != original {
		t.Fatal("original fingerprint overwritten")
	}
}
