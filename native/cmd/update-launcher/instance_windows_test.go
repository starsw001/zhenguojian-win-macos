package main

import (
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"duanjuapp/native/secureupdate"
	"golang.org/x/sys/windows"
)

func TestProductLaunchMutexSeparatesEditions(t *testing.T) {
	key := "hongguoshang|" + secureupdate.Random()
	first, exists, err := acquireProductLaunchMutex(key)
	if err != nil || exists || first == 0 {
		t.Fatal(err, exists)
	}
	defer windows.CloseHandle(first)
	second, exists, err := acquireProductLaunchMutex(key)
	if second != 0 {
		windows.CloseHandle(second)
	}
	if err != nil || !exists {
		t.Fatal(err, exists)
	}
	other, exists, err := acquireProductLaunchMutex("zhenguoshang|" + key)
	if other != 0 {
		windows.CloseHandle(other)
	}
	if err != nil || exists {
		t.Fatal(err, exists)
	}
}

func TestProcessProductReadsVersionAndIdentity(t *testing.T) {
	oldEdition, oldDistribution := edition, distributionID
	edition, distributionID = "hongguoshang", "fixture-distribution"
	t.Cleanup(func() { edition, distributionID = oldEdition, oldDistribution })
	directory := t.TempDir()
	payload, _ := json.Marshal(secureupdate.Manifest{Edition: edition, DistributionID: distributionID, Version: "1.1.71+202"})
	body, _ := json.Marshal(secureupdate.Envelope{Payload: base64.RawURLEncoding.EncodeToString(payload), Signature: "fixture"})
	if err := os.WriteFile(filepath.Join(directory, "distribution.json"), body, 0600); err != nil {
		t.Fatal(err)
	}
	actual, valid := processProduct(filepath.Join(directory, edition+".exe"))
	if !valid || actual != "1.1.71+202" {
		t.Fatal(actual, valid)
	}
}

func TestProcessInsideRootAllowsOnlyOwnRuntimeAndUpdateHelper(t *testing.T) {
	oldEdition := edition
	edition = "hongguoshang"
	t.Cleanup(func() { edition = oldEdition })
	root := filepath.Join(t.TempDir(), "install")
	inside := []string{
		filepath.Join(root, "hongguoshang.exe"),
		filepath.Join(root, "app-data", "updates", "slot-a", "bundle", "hongguoshang.exe"),
	}
	for _, path := range inside {
		if !processInsideRoot(root, path) {
			t.Fatal(path)
		}
	}
	if processInsideRoot(root, filepath.Join(filepath.Dir(root), "other", "hongguoshang.exe")) {
		t.Fatal("other install accepted")
	}
}

func TestRunningProductMessageIncludesVersions(t *testing.T) {
	oldEdition, oldVersion := edition, version
	edition, version = "hongguoshang", "1.1.71+202"
	t.Cleanup(func() { edition, version = oldEdition, oldVersion })
	for _, running := range []string{"1.1.70+201", "1.1.71+202", "1.1.72+203"} {
		message := runningProductMessage(running)
		if !strings.Contains(message, "红果赏") || !strings.Contains(message, running) {
			t.Fatal(running)
		}
	}
}
