package engine

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jinto/kittypaw/core"
	"github.com/jinto/kittypaw/sandbox"
)

func TestRunPackageInjectsGmailOAuthAccessToken(t *testing.T) {
	baseDir := t.TempDir()
	secrets := mustTestSecrets(t)
	pm := installOAuthPackage(t, baseDir, secrets)
	if err := core.NewServiceTokenManager(secrets).Save("gmail", core.ServiceTokenSet{
		Provider:    "gmail",
		AccessToken: "gmail-access-1",
	}); err != nil {
		t.Fatalf("Save service token: %v", err)
	}
	cfg := core.DefaultConfig()
	sess := &AccountRuntime{
		Sandbox:         sandbox.New(cfg.Sandbox),
		Config:          &cfg,
		BaseDir:         baseDir,
		PackageManager:  pm,
		ServiceTokenMgr: core.NewServiceTokenManager(secrets),
	}

	raw, err := runSkillOrPackageWithParams(context.Background(), "gmail-oauth-test", sess, nil)
	if err != nil {
		t.Fatalf("runSkillOrPackageWithParams: %v", err)
	}
	var wrapper struct {
		Output string `json:"output"`
	}
	if err := json.Unmarshal([]byte(raw), &wrapper); err != nil {
		t.Fatalf("decode wrapper: %v", err)
	}
	if wrapper.Output != "gmail-access-1" {
		t.Fatalf("output = %q, want gmail access token", wrapper.Output)
	}
}

func TestRunPackageInjectsXOAuthAccessToken(t *testing.T) {
	baseDir := t.TempDir()
	secrets := mustTestSecrets(t)
	pm := installOAuthPackageWithSource(t, baseDir, secrets, "x-oauth-test", "oauth-x/access_token")
	if err := core.NewServiceTokenManager(secrets).Save("x", core.ServiceTokenSet{
		Provider:    "x",
		AccessToken: "x-access-1",
	}); err != nil {
		t.Fatalf("Save service token: %v", err)
	}
	cfg := core.DefaultConfig()
	sess := &AccountRuntime{
		Sandbox:         sandbox.New(cfg.Sandbox),
		Config:          &cfg,
		BaseDir:         baseDir,
		PackageManager:  pm,
		ServiceTokenMgr: core.NewServiceTokenManager(secrets),
	}

	raw, err := runSkillOrPackageWithParams(context.Background(), "x-oauth-test", sess, nil)
	if err != nil {
		t.Fatalf("runSkillOrPackageWithParams: %v", err)
	}
	var wrapper struct {
		Output string `json:"output"`
	}
	if err := json.Unmarshal([]byte(raw), &wrapper); err != nil {
		t.Fatalf("decode wrapper: %v", err)
	}
	if wrapper.Output != "x-access-1" {
		t.Fatalf("output = %q, want X access token", wrapper.Output)
	}
}

func TestRunPackageMissingGmailOAuthReturnsActionableError(t *testing.T) {
	baseDir := t.TempDir()
	secrets := mustTestSecrets(t)
	pm := installOAuthPackage(t, baseDir, secrets)
	cfg := core.DefaultConfig()
	sess := &AccountRuntime{
		Sandbox:         sandbox.New(cfg.Sandbox),
		Config:          &cfg,
		BaseDir:         baseDir,
		PackageManager:  pm,
		ServiceTokenMgr: core.NewServiceTokenManager(secrets),
	}

	raw, err := runSkillOrPackageWithParams(context.Background(), "gmail-oauth-test", sess, nil)
	if err != nil {
		t.Fatalf("runSkillOrPackageWithParams: %v", err)
	}
	if !strings.Contains(raw, "kittypaw connect gmail") {
		t.Fatalf("raw = %s, want reconnect guidance", raw)
	}
}

func installOAuthPackage(t *testing.T, baseDir string, secrets *core.SecretsStore) *core.PackageManager {
	return installOAuthPackageWithSource(t, baseDir, secrets, "gmail-oauth-test", "oauth-gmail/access_token")
}

func installOAuthPackageWithSource(t *testing.T, baseDir string, secrets *core.SecretsStore, id, source string) *core.PackageManager {
	t.Helper()
	srcDir := t.TempDir()
	tomlContent := `
[meta]
id = "` + id + `"
name = "OAuth Test"
version = "0.1.0"
description = "test"

[[config]]
key = "access_token"
label = "Access Token"
required = true
secret = true
source = "` + source + `"
`
	jsContent := `
const ctx = JSON.parse(__context__);
return ctx.config.access_token || "";
`
	if err := os.WriteFile(filepath.Join(srcDir, "package.toml"), []byte(tomlContent), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(srcDir, "main.js"), []byte(jsContent), 0o644); err != nil {
		t.Fatal(err)
	}
	pm := core.NewPackageManagerFrom(baseDir, secrets)
	if _, err := pm.Install(srcDir); err != nil {
		t.Fatalf("install package: %v", err)
	}
	return pm
}

func mustTestSecrets(t *testing.T) *core.SecretsStore {
	t.Helper()
	secrets, err := core.LoadSecretsFrom(filepath.Join(t.TempDir(), "secrets.json"))
	if err != nil {
		t.Fatal(err)
	}
	return secrets
}
