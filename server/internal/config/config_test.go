package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestConfigFailsClosed(t *testing.T) {
	c := Config{Mode: "offline", Listen: "127.0.0.1:8080", PairingSecret: "01234567890123456789012345678901"}
	if err := c.Validate(); err != nil {
		t.Fatal(err)
	}
	c.Mode = "live"
	if err := c.Validate(); err == nil {
		t.Fatal("live started without credentials")
	}
	c.Mode = "offline"
	c.Listen = "0.0.0.0:8080"
	if err := c.Validate(); err == nil {
		t.Fatal("public plaintext listener")
	}
}
func TestEnvFileIsLiteralAndEnvironmentWins(t *testing.T) {
	t.Setenv("RELAY_TEST_EXISTING", "keep")
	f := filepath.Join(t.TempDir(), "env")
	if err := os.WriteFile(f, []byte("# comment\nRELAY_TEST_EXISTING=replace\nRELAY_TEST_NEW=$(never-execute)\n"), 0600); err != nil {
		t.Fatal(err)
	}
	defer os.Unsetenv("RELAY_TEST_NEW")
	if err := LoadEnv(f); err != nil {
		t.Fatal(err)
	}
	if os.Getenv("RELAY_TEST_EXISTING") != "keep" || os.Getenv("RELAY_TEST_NEW") != "$(never-execute)" {
		t.Fatal("env parsing")
	}
}

func TestAPNsEnvironmentKeySelection(t *testing.T) {
	c := Config{APNsEnvironment: "sandbox", APNsKeyID: "legacy", APNsKeyFile: "legacy.p8"}
	keys, err := c.APNsCredentials()
	if err != nil || len(keys) != 1 || keys["development"].KeyID != "legacy" {
		t.Fatal(keys, err)
	}
	c.APNsEnvironment = "both"
	c.APNsSandboxKeyID = "sandbox-key"
	c.APNsSandboxKeyFile = "sandbox.p8"
	c.APNsProductionKeyID = "production-key"
	c.APNsProductionKeyFile = "production.p8"
	keys, err = c.APNsCredentials()
	if err != nil || len(keys) != 2 || keys["production"].KeyID != "production-key" || keys["development"].KeyID != "sandbox-key" {
		t.Fatal(keys, err)
	}
	c.APNsProductionKeyFile = ""
	if _, err := c.APNsCredentials(); err == nil {
		t.Fatal("partial scoped key fell back to common key")
	}
	c.APNsEnvironment = "typo"
	if _, err := c.APNsCredentials(); err == nil {
		t.Fatal("invalid environment accepted")
	}
}
func TestKeyPathAlias(t *testing.T) {
	t.Setenv("APNS_KEY_PATH", "preferred.p8")
	t.Setenv("APNS_KEY_FILE", "legacy.p8")
	if Load().APNsKeyFile != "preferred.p8" {
		t.Fatal("key path alias")
	}
}

func TestDMDATAAPIKeyAliasAndClassificationSelection(t *testing.T) {
	t.Setenv("DMDATA_API_KEY", "new-key")
	t.Setenv("DMDATA_TOKEN", "legacy-key")
	t.Setenv("DMDATA_CLASSIFICATIONS", "eew.warning,telegram.earthquake")
	c := Load()
	if c.DMDATAToken != "new-key" || len(c.DMDATAClassifications) != 2 || c.DMDATAClassifications[0] != "eew.warning" {
		t.Fatal("DMDATA configuration")
	}
}
