package core

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// TestNativePublishedReaderAllowsManualDelete 验证「Native Published Reader Allows Manual Delete」回归场景。
func TestNativePublishedReaderAllowsManualDelete(t *testing.T) {
	directory := t.TempDir()
	file := filepath.Join(directory, "独立分集.mp4")
	if err := os.WriteFile(file, []byte("synthetic-mp4"), 0600); err != nil {
		t.Fatal(err)
	}
	reader, err := nativeOpenPublishedFile(file)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	if err = os.Remove(file); err != nil {
		t.Fatal("read-only verification blocks manual deletion", err)
	}
	if err = reader.Close(); err != nil {
		t.Fatal(err)
	}
	if err = os.Remove(directory); err != nil {
		t.Fatal("completed reader left a directory handle", err)
	}
}

// TestNativePublishedReaderDoesNotChangeAttributes 验证「Native Published Reader Does Not Change Attributes」回归场景。
func TestNativePublishedReaderDoesNotChangeAttributes(t *testing.T) {
	file := filepath.Join(t.TempDir(), "ordinary.mp4")
	if err := os.WriteFile(file, []byte("content"), 0600); err != nil {
		t.Fatal(err)
	}
	before, _ := os.Stat(file)
	reader, err := nativeOpenPublishedFile(file)
	if err != nil {
		t.Fatal(err)
	}
	if err = reader.Close(); err != nil {
		t.Fatal(err)
	}
	after, _ := os.Stat(file)
	if before.Mode() != after.Mode() || before.ModTime() != after.ModTime() || !os.SameFile(before, after) {
		t.Fatal("read-only verification changed attributes or identity")
	}
}

type deletingPublicationContext struct {
	context.Context
	path string
	done bool
	err  error
}

// Err 返回Err。
func (ctx *deletingPublicationContext) Err() error {
	if !ctx.done {
		ctx.done = true
		ctx.err = os.Remove(ctx.path)
	}
	return nil
}

// TestNativePublishedDeletionDuringHashKeepsRawCacheAndRecord 验证「Native Published Deletion During Hash Keeps Raw Cache And Record」回归场景。
func TestNativePublishedDeletionDuringHashKeepsRawCacheAndRecord(t *testing.T) {
	manager, input := publicationFixture(t)
	record := manager.jobs[input.JobID]
	ctx := &deletingPublicationContext{Context: context.Background(), path: filepath.Join(manager.root, filepath.FromSlash(input.PublishedFile))}
	if err := manager.publish(ctx, input); err == nil {
		t.Fatal("deleted product was committed")
	}
	if ctx.err != nil {
		t.Fatal("verification prevents manual deletion", ctx.err)
	}
	if record.PublishedFile != "" {
		t.Fatal("record changed after manual deletion")
	}
	if _, err := os.Stat(manager.jobDirectory(record.nativeDownloadJob)); err != nil {
		t.Fatal("raw cache lost after product deletion", err)
	}
}

// TestNativePublishedReaderReportsMissingFile 验证「Native Published Reader Reports Missing File」回归场景。
func TestNativePublishedReaderReportsMissingFile(t *testing.T) {
	_, err := nativeOpenPublishedFile(filepath.Join(t.TempDir(), "missing.mp4"))
	if !errors.Is(err, os.ErrNotExist) {
		t.Fatal(err)
	}
}
