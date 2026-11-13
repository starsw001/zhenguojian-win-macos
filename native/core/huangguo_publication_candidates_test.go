package core

import (
	"encoding/json"
	"testing"
)

// TestBuildPublicationCandidatesArePendingAndNeverOverrideVerifiedRoutes 验证「Build Publication Candidates Are Pending And Never Override Verified Routes」回归场景。
func TestBuildPublicationCandidatesArePendingAndNeverOverrideVerifiedRoutes(t *testing.T) {
	var candidates []huangguoMirrorRecord
	if err := json.Unmarshal([]byte(huangguoPublicationCandidateJSON), &candidates); err != nil {
		t.Fatal(err)
	}
	registry := &huangguoMirrorRegistry{disk: huangguoMirrorDisk{Version: 1}}
	origin := "https://fixture.example.org"
	registry.appendLocked(origin, "2026-10-23", "fixture")
	registry.disk.Mirrors[0].Kind, registry.disk.Mirrors[0].State = sourceHuangguoAI, "ready"
	registry.disk.Mirrors[0].LastError = "retained reason"
	registry.appendLocked(origin, "2026-10-26", "publication")
	if len(registry.disk.Mirrors) != 1 || registry.disk.Mirrors[0].State != "ready" || registry.disk.Mirrors[0].LastError != "retained reason" {
		t.Fatal("existing route replaced")
	}
	for _, candidate := range candidates {
		index := registry.appendLocked(candidate.Origin, candidate.AddedOn, candidate.AddedFrom)
		if index < 1 || candidate.AddedOn == "" || candidate.AddedFrom == "" {
			t.Fatal("invalid publication candidate")
		}
		entry := registry.disk.Mirrors[index]
		if entry.State != "pending" || usableHuangguoMirror(entry, sourceHuangguoAI) || usableHuangguoMirror(entry, sourceHuangguoVideo) {
			t.Fatal("unverified address enabled")
		}
	}
}
