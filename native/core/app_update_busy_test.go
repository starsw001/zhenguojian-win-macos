package core

import (
	"context"
	"testing"
)

// TestUpdateBusyReservation 验证「Update Busy Reservation」回归场景。
func TestUpdateBusyReservation(t *testing.T) {
	engine := &nativeEngine{downloads: &nativeDownloads{jobs: map[string]*nativeDownloadRecord{}, active: map[string]context.CancelFunc{}}}
	m := engine.downloads
	for _, job := range []string{"queued", "downloading"} {
		m.jobs["job"] = &nativeDownloadRecord{nativeDownloadJob: nativeDownloadJob{State: job}}
		if engine.activity(true) == nil || m.updateHandoff {
			t.Fatal("did not defer", job)
		}
	}
	delete(m.jobs, "job")
	m.mediaBusy = true
	if engine.activity(true) == nil {
		t.Fatal("media")
	}
	m.mediaBusy = false
	engine.playbackCancel = func() {}
	if engine.activity(true) == nil {
		t.Fatal("playback")
	}
	engine.playbackCancel = nil
	if e := engine.activity(true); e != nil || !m.updateHandoff {
		t.Fatal("idle handoff", e)
	}
	if _, _, e := engine.nativeBeginPlayback(context.Background(), 1); e == nil {
		t.Fatal("play started during handoff")
	}
}
