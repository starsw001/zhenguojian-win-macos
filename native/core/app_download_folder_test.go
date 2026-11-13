package core

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// TestNativeDownloadFolderFindsPublishedOrClearMP4WithoutChangingRecords 验证「Native Download Folder Finds Published Or Clear MP 4 Without Changing Records」回归场景。
func TestNativeDownloadFolderFindsPublishedOrClearMP4WithoutChangingRecords(t *testing.T) {
	m, published := publicationFixture(t)
	record := m.jobs[published.JobID]
	record.PublishedFile = published.PublishedFile
	record.PublishedBytes = published.PublishedBytes
	record.PublishedSHA256 = published.PublishedSHA256
	drama := record.Drama
	id := nativeDownloadID(drama.ID, 2)
	plain := &nativeDownloadRecord{nativeDownloadJob: nativeDownloadJob{ID: id, Drama: drama, Index: 2, State: "completed", Bytes: 5}, File: "media.mp4"}
	m.jobs[id] = plain
	dir := m.jobDirectory(plain.nativeDownloadJob)
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, plain.File), []byte("clear"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := m.saveLocked(); err != nil {
		t.Fatal(err)
	}
	index := filepath.Join(m.root, "index.json")
	before, err := os.ReadFile(index)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, _ := json.Marshal(m.jobs)
	ctx := context.Background()
	expected := filepath.Dir(filepath.Join(m.root, filepath.FromSlash(record.PublishedFile)))
	if got, err := m.downloadFolder(ctx, []string{"missing-id", id, record.ID}); err != nil || got != expected {
		t.Fatalf("published folder = %q, %v", got, err)
	}
	if err := os.Remove(filepath.Join(m.root, filepath.FromSlash(record.PublishedFile))); err != nil {
		t.Fatal(err)
	}
	if got, err := m.downloadFolder(ctx, []string{record.ID, id}); err != nil || got != dir {
		t.Fatalf("clear fallback = %q, %v", got, err)
	}
	if err := os.WriteFile(filepath.Join(dir, plain.File), []byte("partial"), 0600); err != nil {
		t.Fatal(err)
	}
	if got, err := m.downloadFolder(ctx, []string{record.ID, id}); err != nil || got != "" {
		t.Fatalf("partial or encrypted download opened: %q, %v", got, err)
	}
	after, err := os.ReadFile(index)
	now, _ := json.Marshal(m.jobs)
	if err != nil || string(before) != string(after) || string(snapshot) != string(now) {
		t.Fatal("folder lookup changed the task index")
	}
}

// TestNativeDownloadFolderRejectsUnrelatedUnavailableAndPlaylistJobs 验证「Native Download Folder Rejects Unrelated Unavailable And Playlist Jobs」回归场景。
func TestNativeDownloadFolderRejectsUnrelatedUnavailableAndPlaylistJobs(t *testing.T) {
	m, published := publicationFixture(t)
	record := m.jobs[published.JobID]
	record.File = "index.m3u8"
	dir := m.jobDirectory(record.nativeDownloadJob)
	if err := os.WriteFile(filepath.Join(dir, "index.m3u8"), []byte("#EXTM3U\nmissing.ts\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if got, err := m.downloadFolder(context.Background(), []string{record.ID}); err != nil || got != "" || record.State != "completed" {
		t.Fatalf("playlist inspected as playable media: %q, %v, state %s", got, err, record.State)
	}
	record.PublishedFile, record.PublishedBytes, record.PublishedSHA256 = published.PublishedFile, published.PublishedBytes, published.PublishedSHA256
	record.State = "queued"
	if got, err := m.downloadFolder(context.Background(), []string{record.ID, "unknown"}); err != nil || got != "" {
		t.Fatalf("uncompleted task opened: %q, %v", got, err)
	}
	m.moving = true
	if _, err := m.downloadFolder(context.Background(), []string{record.ID}); err == nil {
		t.Fatal("moving directory should not open")
	}
}
