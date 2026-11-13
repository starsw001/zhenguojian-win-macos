package core

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// TestAIIdentityCacheEpochOnlyRemovesDerivedSourceFields 验证「AI Identity Cache Epoch Only Removes Derived Source Fields」回归场景。
func TestAIIdentityCacheEpochOnlyRemovesDerivedSourceFields(t *testing.T) {
	oldBuild := buildAllSources
	buildAllSources = "true"
	defer func() { buildAllSources = oldBuild }()
	engine := sourceFixtureEngine(t, nil)
	disk := `{"version":3,"catalogs":{"huangguoai":[{"id":"huangguoai:89","episodes":25}],"huangguoai|tag:qihuan":[{"id":"huangguoai:89"}],"hongguo":[{"id":"hongguo:keep"}]},"states":{"huangguoai":{"page":2},"hongguo":{"page":1}},"categories":{"huangguoai":[{"id":"old"}],"hongguo":[]},"catalogEpochs":{"other":"preserve"},"opaque":{"preserve":true},"hongguoApp":{"deviceId":"preserve"}}`
	path := filepath.Join(engine.directory, "catalogs.json")
	if err := os.WriteFile(path, []byte(disk), 0600); err != nil {
		t.Fatal(err)
	}
	private := filepath.Join(engine.directory, "sources.json")
	before := []byte(`{"opaque-health-history":"preserve"}`)
	if err := os.WriteFile(private, before, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := engine.rebuildHuangguoIdentityCache("package-first"); err != nil {
		t.Fatal(err)
	}
	var after map[string]json.RawMessage
	body, _ := os.ReadFile(path)
	if json.Unmarshal(body, &after) != nil {
		t.Fatal("cache not parseable")
	}
	var catalogs map[string]json.RawMessage
	_ = json.Unmarshal(after["catalogs"], &catalogs)
	if len(catalogs) != 1 || catalogs["hongguo"] == nil || string(after["opaque"]) != `{"preserve":true}` || engine.catalogEpochs["other"] != "preserve" {
		t.Fatal("unrelated caches or extension changed", string(body))
	}
	actual, _ := os.ReadFile(private)
	if string(actual) != string(before) {
		t.Fatal("health history changed")
	}
	engine.catalogs[sourceHuangguoAI] = []nativeDrama{{ID: "huangguoai:current"}}
	if removed, err := engine.rebuildHuangguoIdentityCache("package-first"); err != nil || removed != 0 || len(engine.catalogs[sourceHuangguoAI]) != 1 {
		t.Fatal("same package repeated cache clearing")
	}
}

// TestAIIdentityCacheFailureAndRedEditionDoNotClear 验证「AI Identity Cache Failure And Red Edition Do Not Clear」回归场景。
func TestAIIdentityCacheFailureAndRedEditionDoNotClear(t *testing.T) {
	oldBuild := buildAllSources
	defer func() { buildAllSources = oldBuild }()
	engine := sourceFixtureEngine(t, nil)
	path := filepath.Join(engine.directory, "catalogs.json")
	broken := []byte(`{"catalogs":`)
	if err := os.WriteFile(path, broken, 0600); err != nil {
		t.Fatal(err)
	}
	buildAllSources = "true"
	if _, err := engine.rebuildHuangguoIdentityCache("package-new"); err == nil {
		t.Fatal("broken cache discarded")
	}
	body, _ := os.ReadFile(path)
	if string(body) != string(broken) || engine.catalogEpochs[huangguoIdentityCacheEpoch] != "" {
		t.Fatal("failed clear committed marker or changed bytes")
	}
	buildAllSources = "false"
	if _, err := engine.rebuildHuangguoIdentityCache("package-red"); err != nil {
		t.Fatal("red edition inspected uncompiled source")
	}
}
