package licensing

import (
	"crypto/ed25519"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// TestDownloadCategoryRejectsCreationAndMissingSignedLease 验证「Download Category Rejects Creation And Missing Signed Lease」回归场景。
func TestDownloadCategoryRejectsCreationAndMissingSignedLease(t *testing.T) {
	f := newFixture(t)
	m := Open(f.config)
	m.Login("fixture1@example.invalid", "Abc123@#")
	if s := m.Activate("WYPS-QRF4-RAZB-4RVQ"); !s.Ready {
		t.Fatalf("download fixture: %s", s.Phase)
	}
	lease, err := m.readLease(m.state.Lease, "")
	if err != nil {
		t.Fatal(err)
	}
	for _, category := range []int{0, 2, 3} {
		altered := lease
		altered.Category = category
		if _, err = m.readLease(signed(t, f.server, altered), ""); err == nil {
			t.Fatalf("wrong category accepted: %d", category)
		}
	}
}

// TestDownloadManifestRequiresSignedDownloadCategory 验证「Download Manifest Requires Signed Download Category」回归场景。
func TestDownloadManifestRequiresSignedDownloadCategory(t *testing.T) {
	for _, category := range []int{0, 2} {
		f := newFixture(t)
		raw, err := os.ReadFile(filepath.Join(f.config.Root, "distribution.json"))
		if err != nil {
			t.Fatal(err)
		}
		var envelope Envelope
		_ = json.Unmarshal(raw, &envelope)
		var d Distribution
		if err = verifyEnvelope(envelope, f.distributor.Public().(ed25519.PublicKey), &d); err != nil {
			t.Fatal(err)
		}
		d.Category = category
		d.Permit = signed(t, f.distributor, ActivationPermit{Category: category, Protocol: Protocol, Kind: "activation-permit", DistributionID: d.DistributionID, Edition: d.Edition, Months: d.Months, Username: d.Username})
		raw, _ = json.Marshal(signed(t, f.distributor, d))
		if err = os.WriteFile(filepath.Join(f.config.Root, "distribution.json"), raw, 0600); err != nil {
			t.Fatal(err)
		}
		if s := Open(f.config).Poll(false); s.Phase != "configuration" || s.Ready {
			t.Fatal("non-download manifest accepted")
		}
	}
}

// TestLeaseRequiresItsOwnSoftwareEdition 验证「Lease Requires Its Own Software Edition」回归场景。
func TestLeaseRequiresItsOwnSoftwareEdition(t *testing.T) {
	f := newFixture(t)
	m := Open(f.config)
	m.Login("fixture1@example.invalid", "Abc123@#")
	if s := m.Activate("WYPS-QRF4-RAZB-4RVQ"); !s.Ready {
		t.Fatal(s.Phase)
	}
	lease, err := m.readLease(m.state.Lease, "")
	if err != nil {
		t.Fatal(err)
	}
	for _, edition := range []string{"", "zhenguoshang", "creator"} {
		changed := lease
		changed.LicenseEdition = edition
		if _, err = m.readLease(signed(t, f.server, changed), ""); err == nil {
			t.Fatal("cross-edition lease accepted", edition)
		}
	}
}
