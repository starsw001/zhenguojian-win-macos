package secureupdate

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func stageFixtureVersion(t *testing.T, fixture fixture, version string, executable []byte) string {
	t.Helper()
	tree := t.TempDir()
	makeTree(t, tree, fixture.i.Edition, fixture.i.ID, version, fixture.priv, executable)
	out := t.TempDir()
	release, err := Publish(tree, out, fixture.key, fixture.i.Key, fixture.i.Edition, fixture.i.ID, version)
	if err != nil {
		t.Fatal(err)
	}
	metadata, err := os.ReadFile(filepath.Join(out, fixture.i.Edition+"-windows-x64.update.json"))
	if err != nil {
		t.Fatal(err)
	}
	slot, err := fixture.i.Stage(filepath.Join(out, release.Archive), metadata, fixture.i.Base)
	if err != nil {
		t.Fatal(err)
	}
	return slot
}

func writeFixtureFile(t *testing.T, root, name, body string) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
}

func readFixtureFile(t *testing.T, root, name string) string {
	t.Helper()
	body, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(name)))
	if err != nil {
		t.Fatal(err)
	}
	return string(body)
}

func TestApplyStagedOverlaysRootAndPreservesMutableData(t *testing.T) {
	fixture := newFixture(t, "hongguoshang", []byte("base-launcher"))
	previous := stageFixtureVersion(t, fixture, "1.1.28+159", []byte("previous-launcher"))
	previousDir, _, err := fixture.i.Slot(previous)
	if err != nil {
		t.Fatal(err)
	}
	writeFixtureFile(t, previousDir, "app-data/profiles/watch.json", "latest-watch")
	writeFixtureFile(t, previousDir, "app-data/updates/not-user-data", "skip")
	writeFixtureFile(t, previousDir, "download/剧集/第12集.mp4", "latest-video")
	writeFixtureFile(t, previousDir, "logs/client.log", "latest-log")
	writeFixtureFile(t, fixture.i.Root, "app-data/profiles/watch.json", "base-watch")
	writeFixtureFile(t, fixture.i.Root, "download/剧集/第12集.mp4", "base-video")
	writeFixtureFile(t, fixture.i.Root, "user-notes/keep.txt", "never-touch")
	target := stageFixtureVersion(t, fixture, "1.1.29+160", []byte("target-launcher"))
	if err = fixture.i.Commit(Selection{Active: target, Previous: previous}); err != nil {
		t.Fatal(err)
	}
	validated := false
	version, err := fixture.i.ApplyStaged(target, func(root string) error {
		validated = true
		if err := VerifyTree(root, fixture.i.Key, fixture.i.Edition, fixture.i.ID, "1.1.29+160", false); err != nil {
			return err
		}
		if readFixtureFile(t, root, "download/剧集/第12集.mp4") != "latest-video" {
			return errors.New("download was not consolidated")
		}
		return nil
	})
	if err != nil || version != "1.1.29+160" || !validated {
		t.Fatal(version, validated, err)
	}
	if fixture.i.Selection() != (Selection{}) {
		t.Fatal("staged selection remained active")
	}
	for name, expected := range map[string]string{
		"app-data/profiles/watch.json": "latest-watch",
		"download/剧集/第12集.mp4":         "latest-video",
		"logs/client.log":              "latest-log",
		"user-notes/keep.txt":          "never-touch",
		fixture.i.Edition + ".exe":     "target-launcher",
	} {
		if actual := readFixtureFile(t, fixture.i.Root, name); actual != expected {
			t.Fatalf("%s = %q, want %q", name, actual, expected)
		}
	}
	if _, err = os.Stat(filepath.Join(fixture.i.Root, "app-data", "updates", "not-user-data")); !os.IsNotExist(err) {
		t.Fatal("nested update cache was copied as user data")
	}
	if err = fixture.i.PruneStaged(""); err != nil {
		t.Fatal(err)
	}
	for _, slot := range []string{previous, target} {
		if _, err = os.Stat(filepath.Join(fixture.i.Cache, "slot-"+slot)); !os.IsNotExist(err) {
			t.Fatal("staged slot was not removed", slot, err)
		}
	}
}

func TestApplyStagedKeepsVerifiedRootLicenseState(t *testing.T) {
	fixture := newFixture(t, "hongguoshang", []byte("base-launcher"))
	previous := stageFixtureVersion(t, fixture, "1.1.28+159", []byte("previous-launcher"))
	previousDir, _, err := fixture.i.Slot(previous)
	if err != nil {
		t.Fatal(err)
	}
	licenseName := filepath.ToSlash(filepath.Join("app-data", "licenses", fixture.i.Edition, fixture.i.ID, "license-state.dat"))
	writeFixtureFile(t, previousDir, licenseName, "stale-slot-license")
	writeFixtureFile(t, fixture.i.Root, licenseName, "verified-root-license")
	target := stageFixtureVersion(t, fixture, "1.1.29+160", []byte("target-launcher"))
	if err = fixture.i.Commit(Selection{Active: target, Previous: previous}); err != nil {
		t.Fatal(err)
	}
	if _, err = fixture.i.ApplyStaged(target, nil); err != nil {
		t.Fatal(err)
	}
	if actual := readFixtureFile(t, fixture.i.Root, licenseName); actual != "verified-root-license" {
		t.Fatal("verified account and license state changed", actual)
	}
}

func TestApplyStagedInheritsPreviousLicenseStateWhenRootMissing(t *testing.T) {
	fixture := newFixture(t, "zhenguoshang", []byte("base-launcher"))
	previous := stageFixtureVersion(t, fixture, "1.1.28+159", []byte("previous-launcher"))
	previousDir, _, err := fixture.i.Slot(previous)
	if err != nil {
		t.Fatal(err)
	}
	licenseName := filepath.ToSlash(filepath.Join("app-data", "licenses", fixture.i.Edition, fixture.i.ID, "license-state.dat"))
	writeFixtureFile(t, previousDir, licenseName, "verified-previous-license")
	target := stageFixtureVersion(t, fixture, "1.1.29+160", []byte("target-launcher"))
	if err = fixture.i.Commit(Selection{Active: target, Previous: previous}); err != nil {
		t.Fatal(err)
	}
	if _, err = fixture.i.ApplyStaged(target, nil); err != nil {
		t.Fatal(err)
	}
	if actual := readFixtureFile(t, fixture.i.Root, licenseName); actual != "verified-previous-license" {
		t.Fatal("previous account and license state was not inherited", actual)
	}
}

func TestApplyStagedRestoresRootWhenValidationFails(t *testing.T) {
	fixture := newFixture(t, "zhenguoshang", []byte("base-launcher"))
	target := stageFixtureVersion(t, fixture, "1.1.28+159", []byte("target-launcher"))
	writeFixtureFile(t, fixture.i.Root, "download/keep/episode.mp4", "user-video")
	if err := fixture.i.Commit(Selection{Active: target}); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.i.ApplyStaged(target, func(string) error {
		return errors.New("synthetic first start failure")
	}); err == nil {
		t.Fatal("failed validation was accepted")
	}
	if err := VerifyTree(fixture.i.Root, fixture.i.Key, fixture.i.Edition, fixture.i.ID, fixture.i.Base, false); err != nil {
		t.Fatal("base runtime was not restored", err)
	}
	if actual := readFixtureFile(t, fixture.i.Root, fixture.i.Edition+".exe"); actual != "base-launcher" {
		t.Fatal("base executable was not restored", actual)
	}
	if actual := readFixtureFile(t, fixture.i.Root, "download/keep/episode.mp4"); actual != "user-video" {
		t.Fatal("user download changed", actual)
	}
	if fixture.i.Selection() != (Selection{}) {
		t.Fatal("failed update remained selected")
	}
}

func TestCurrentStagedSlotRejectsOriginalRoot(t *testing.T) {
	fixture := newFixture(t, "hongguoshang", nil)
	slot := stageFixtureVersion(t, fixture, "1.1.28+159", nil)
	directory, _, err := fixture.i.Slot(slot)
	if err != nil {
		t.Fatal(err)
	}
	actual, err := fixture.i.CurrentStagedSlot(filepath.Join(directory, fixture.i.Edition+".exe"))
	if err != nil || actual != slot {
		t.Fatal(actual, err)
	}
	if _, err = fixture.i.CurrentStagedSlot(filepath.Join(fixture.i.Root, fixture.i.Edition+".exe")); err == nil {
		t.Fatal("original root was classified as a staged slot")
	}
}
