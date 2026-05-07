package core

import (
	"os"
	"path/filepath"
	"testing"
)

// ---------------------------------------------------------------------------
// ValidatePackageID
// ---------------------------------------------------------------------------

func TestValidatePackageID_Valid(t *testing.T) {
	for _, id := range []string{"my-package", "hello_world", "pkg123", "a"} {
		if err := ValidatePackageID(id); err != nil {
			t.Errorf("ValidatePackageID(%q) should be valid: %v", id, err)
		}
	}
}

func TestValidatePackageID_Invalid(t *testing.T) {
	for _, id := range []string{"", "My-Package", "pkg/bad", "pkg\\bad", "pkg..bad", "../escape"} {
		if err := ValidatePackageID(id); err == nil {
			t.Errorf("ValidatePackageID(%q) should be invalid", id)
		}
	}
}

// ---------------------------------------------------------------------------
// LoadPackageToml
// ---------------------------------------------------------------------------

func TestLoadPackageToml_Valid(t *testing.T) {
	dir := t.TempDir()
	tomlContent := `
[meta]
id = "test-pkg"
name = "Test Package"
version = "1.0.0"
description = "A test package"
author = "tester"
cron = "every 10m"

[[config]]
key = "api_key"
label = "API Key"
required = true
secret = true

[[config]]
key = "region"
label = "Region"
default = "us-east-1"

[[chain]]
package_id = "formatter"

[permissions]
primitives = ["http_get"]
allowed_hosts = ["api.example.com"]
`
	path := filepath.Join(dir, "package.toml")
	if err := os.WriteFile(path, []byte(tomlContent), 0o644); err != nil {
		t.Fatal(err)
	}

	pkg, err := LoadPackageToml(path)
	if err != nil {
		t.Fatal(err)
	}

	if pkg.Meta.ID != "test-pkg" {
		t.Errorf("ID = %q, want %q", pkg.Meta.ID, "test-pkg")
	}
	if pkg.Meta.Name != "Test Package" {
		t.Errorf("Name = %q, want %q", pkg.Meta.Name, "Test Package")
	}
	if pkg.Meta.Cron != "every 10m" {
		t.Errorf("Cron = %q, want %q", pkg.Meta.Cron, "every 10m")
	}
	if len(pkg.Config) != 2 {
		t.Fatalf("Config len = %d, want 2", len(pkg.Config))
	}
	if !pkg.Config[0].Secret {
		t.Error("first config field should be secret")
	}
	if len(pkg.Chain) != 1 {
		t.Fatalf("Chain len = %d, want 1", len(pkg.Chain))
	}
	if pkg.Chain[0].PackageID != "formatter" {
		t.Errorf("Chain[0].PackageID = %q, want %q", pkg.Chain[0].PackageID, "formatter")
	}
	if len(pkg.Permissions.Primitives) != 1 {
		t.Errorf("Permissions.Primitives len = %d, want 1", len(pkg.Permissions.Primitives))
	}
}

func TestLoadPackageToml_MissingName(t *testing.T) {
	dir := t.TempDir()
	tomlContent := `
[meta]
id = "no-name"
version = "1.0.0"
`
	path := filepath.Join(dir, "package.toml")
	os.WriteFile(path, []byte(tomlContent), 0o644)

	_, err := LoadPackageToml(path)
	if err == nil {
		t.Error("expected error for missing name")
	}
}

func TestLoadPackageToml_InvalidID(t *testing.T) {
	dir := t.TempDir()
	tomlContent := `
[meta]
id = "Bad-ID"
name = "Test"
version = "1.0.0"
`
	path := filepath.Join(dir, "package.toml")
	os.WriteFile(path, []byte(tomlContent), 0o644)

	_, err := LoadPackageToml(path)
	if err == nil {
		t.Error("expected error for invalid ID (uppercase)")
	}
}

func TestLoadPackageToml_PathTraversalID(t *testing.T) {
	dir := t.TempDir()
	tomlContent := `
[meta]
id = "../escape"
name = "Evil"
version = "1.0.0"
`
	path := filepath.Join(dir, "package.toml")
	os.WriteFile(path, []byte(tomlContent), 0o644)

	_, err := LoadPackageToml(path)
	if err == nil {
		t.Error("expected error for path traversal in ID")
	}
}

func TestLoadPackageToml_NotFound(t *testing.T) {
	_, err := LoadPackageToml("/nonexistent/package.toml")
	if err == nil {
		t.Error("expected error for nonexistent file")
	}
}

func TestLoadPackageToml_ContextPermissions(t *testing.T) {
	dir := t.TempDir()
	tomlContent := `
[meta]
id = "ctx-pkg"
name = "Context Package"
version = "1.0.0"

[permissions]
primitives = ["Http", "Llm"]
context = ["locale", "location"]
`
	path := filepath.Join(dir, "package.toml")
	if err := os.WriteFile(path, []byte(tomlContent), 0o644); err != nil {
		t.Fatal(err)
	}

	pkg, err := LoadPackageToml(path)
	if err != nil {
		t.Fatal(err)
	}

	if got := len(pkg.Permissions.Context); got != 2 {
		t.Fatalf("Permissions.Context len = %d, want 2; got %v", got, pkg.Permissions.Context)
	}
	if pkg.Permissions.Context[0] != "locale" || pkg.Permissions.Context[1] != "location" {
		t.Errorf("Permissions.Context = %v, want [locale location]", pkg.Permissions.Context)
	}
}

func TestLoadPackageToml_InvocationContract(t *testing.T) {
	dir := t.TempDir()
	tomlContent := `
[package]
id = "weather-now"
name = "현재 날씨"
version = "1.2.1"
description = "현재 날씨"

[discovery]
time_scope = "now"
trigger_examples = ["강남역에 비오나? 지금?"]
anti_examples = ["내일 날씨"]

[discovery.delegates_to]
future = "weather-briefing"

[capabilities.location]
accepts = ["coord", "natural_poi", "city"]
resolution = "engine"

[invocation]
execution_model = "deterministic"
caller_responsibilities = [
  "Extract explicit location mentions before calling this package.",
]
missing_slot_policy = "Use configured location only when the user did not explicitly name a place."
postprocess = "Rephrase package output only; do not invent weather facts."

[[invocation.inputs]]
key = "location"
path = "ctx.params.location"
type = "object"
required = false
resolver = "engine"
fields = ["label", "lat", "lon"]
description = "Resolved place for the weather question."

[attribution]
policy = "required-only"

[[attribution.providers]]
id = "kma"
name = "KMA"
label = "Weather data by KMA"
url = "https://apihub.kma.go.kr"
required = false

[permissions]
primitives = ["Http"]
context = ["location"]
`
	path := filepath.Join(dir, "package.toml")
	if err := os.WriteFile(path, []byte(tomlContent), 0o644); err != nil {
		t.Fatal(err)
	}

	pkg, err := LoadPackageToml(path)
	if err != nil {
		t.Fatal(err)
	}
	if pkg.Discovery.TimeScope != "now" {
		t.Fatalf("Discovery.TimeScope = %q, want now", pkg.Discovery.TimeScope)
	}
	if pkg.Discovery.DelegatesTo["future"] != "weather-briefing" {
		t.Fatalf("Discovery.DelegatesTo = %+v", pkg.Discovery.DelegatesTo)
	}
	if pkg.Capabilities.Location.Resolution != "engine" {
		t.Fatalf("Location resolution = %q, want engine", pkg.Capabilities.Location.Resolution)
	}
	if pkg.Invocation.ExecutionModel != "deterministic" {
		t.Fatalf("Invocation.ExecutionModel = %q", pkg.Invocation.ExecutionModel)
	}
	if len(pkg.Invocation.Inputs) != 1 {
		t.Fatalf("Invocation.Inputs len = %d, want 1", len(pkg.Invocation.Inputs))
	}
	input := pkg.Invocation.Inputs[0]
	if input.Key != "location" || input.Path != "ctx.params.location" || input.Resolver != "engine" {
		t.Fatalf("Invocation input = %+v", input)
	}
	if len(input.Fields) != 3 || input.Fields[0] != "label" || input.Fields[1] != "lat" || input.Fields[2] != "lon" {
		t.Fatalf("Invocation input fields = %+v", input.Fields)
	}
	if pkg.Attribution.Policy != "required-only" {
		t.Fatalf("Attribution.Policy = %q, want required-only", pkg.Attribution.Policy)
	}
	if len(pkg.Attribution.Providers) != 1 {
		t.Fatalf("Attribution.Providers len = %d, want 1", len(pkg.Attribution.Providers))
	}
	provider := pkg.Attribution.Providers[0]
	if provider.ID != "kma" || provider.Label != "Weather data by KMA" || provider.Required {
		t.Fatalf("Attribution provider = %+v", provider)
	}
}

// ---------------------------------------------------------------------------
// Install / Uninstall / List (PackageManager)
// ---------------------------------------------------------------------------

func TestPackageManager_InstallAndList(t *testing.T) {
	// Create a fake package source directory.
	src := t.TempDir()
	writeTestPackage(t, src, "test-pkg", "1.0.0")

	// Point PackagesDir to a temp location.
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)

	secrets, err := LoadSecretsFrom(filepath.Join(tmpHome, "secrets.json"))
	if err != nil {
		t.Fatal(err)
	}
	pm := NewPackageManager(secrets)

	pkg, err := pm.Install(src)
	if err != nil {
		t.Fatal(err)
	}
	if pkg.Meta.ID != "test-pkg" {
		t.Errorf("installed ID = %q", pkg.Meta.ID)
	}

	// List should find it.
	list, err := pm.ListInstalled()
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 {
		t.Fatalf("list len = %d, want 1", len(list))
	}

	// Load should work.
	loaded, code, err := pm.LoadPackage("test-pkg")
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Meta.ID != "test-pkg" {
		t.Error("loaded package ID mismatch")
	}
	if code == "" {
		t.Error("code should not be empty")
	}

	// Uninstall.
	if err := pm.Uninstall("test-pkg"); err != nil {
		t.Fatal(err)
	}
	list, _ = pm.ListInstalled()
	if len(list) != 0 {
		t.Error("package should be uninstalled")
	}
}

func TestPackageManager_InstallMissingMainJS(t *testing.T) {
	src := t.TempDir()
	// Write package.toml but no main.js.
	toml := `[meta]
id = "no-js"
name = "No JS"
version = "1.0.0"
`
	os.WriteFile(filepath.Join(src, "package.toml"), []byte(toml), 0o644)

	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)

	secrets, _ := LoadSecretsFrom(filepath.Join(tmpHome, "secrets.json"))
	pm := NewPackageManager(secrets)

	_, err := pm.Install(src)
	if err == nil {
		t.Error("expected error for missing main.js")
	}
}

func TestPackageManager_UninstallNotFound(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)
	secrets, _ := LoadSecretsFrom(filepath.Join(tmpHome, "secrets.json"))
	pm := NewPackageManager(secrets)

	err := pm.Uninstall("nonexistent")
	if err == nil {
		t.Error("expected error for nonexistent package")
	}
}

// ---------------------------------------------------------------------------
// Config (GetConfig / SetConfig)
// ---------------------------------------------------------------------------

func TestPackageManager_Config(t *testing.T) {
	src := t.TempDir()
	writeTestPackage(t, src, "cfg-pkg", "1.0.0")

	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)

	secrets, _ := LoadSecretsFrom(filepath.Join(tmpHome, "secrets.json"))
	pm := NewPackageManager(secrets)

	if _, err := pm.Install(src); err != nil {
		t.Fatal(err)
	}

	// Get default config.
	cfg, err := pm.GetConfig("cfg-pkg")
	if err != nil {
		t.Fatal(err)
	}
	if cfg["region"] != "us-east-1" {
		t.Errorf("default region = %q, want %q", cfg["region"], "us-east-1")
	}

	// Set non-secret config.
	if err := pm.SetConfig("cfg-pkg", "region", "eu-west-1"); err != nil {
		t.Fatal(err)
	}
	cfg, _ = pm.GetConfig("cfg-pkg")
	if cfg["region"] != "eu-west-1" {
		t.Errorf("updated region = %q, want %q", cfg["region"], "eu-west-1")
	}

	// Set secret config.
	if err := pm.SetConfig("cfg-pkg", "api_key", "sk-secret-123"); err != nil {
		t.Fatal(err)
	}
	cfg, _ = pm.GetConfig("cfg-pkg")
	if cfg["api_key"] != "sk-secret-123" {
		t.Errorf("secret api_key = %q, want %q", cfg["api_key"], "sk-secret-123")
	}

	// Verify secret is in secrets store, not config.toml.
	val, ok := secrets.Get("cfg-pkg", "api_key")
	if !ok || val != "sk-secret-123" {
		t.Error("secret should be stored in secrets.json")
	}
}

func TestPackageManager_SetConfigUnknownField(t *testing.T) {
	src := t.TempDir()
	writeTestPackage(t, src, "cfg-unk", "1.0.0")

	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)
	secrets, _ := LoadSecretsFrom(filepath.Join(tmpHome, "secrets.json"))
	pm := NewPackageManager(secrets)
	pm.Install(src)

	err := pm.SetConfig("cfg-unk", "nonexistent", "value")
	if err == nil {
		t.Error("expected error for unknown config field")
	}
}

// ---------------------------------------------------------------------------
// LoadChain
// ---------------------------------------------------------------------------

func TestPackageManager_LoadChain_NotInstalled(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)
	secrets, _ := LoadSecretsFrom(filepath.Join(tmpHome, "secrets.json"))
	pm := NewPackageManager(secrets)

	pkg := &SkillPackage{
		Chain: []ChainStep{{PackageID: "missing-dep"}},
	}
	_, err := pm.LoadChain(pkg)
	if err == nil {
		t.Error("expected error for missing chain dependency")
	}
}

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

func writeTestPackage(t *testing.T, dir, id, version string) {
	t.Helper()
	toml := `[meta]
id = "` + id + `"
name = "Test Package"
version = "` + version + `"
description = "test"

[[config]]
key = "api_key"
label = "API Key"
secret = true

[[config]]
key = "region"
label = "Region"
default = "us-east-1"

[permissions]
primitives = ["http_get"]
`
	os.WriteFile(filepath.Join(dir, "package.toml"), []byte(toml), 0o644)
	os.WriteFile(filepath.Join(dir, "main.js"), []byte(`return "hello"`), 0o644)
}

func writeTestPackageWithSource(t *testing.T, dir, id string) {
	t.Helper()
	toml := `[meta]
id = "` + id + `"
name = "Source Test"
version = "1.0.0"
description = "test"

[[config]]
key = "bot_token"
label = "Bot Token"
secret = true
required = true
source = "telegram/bot_token"

[[config]]
key = "region"
label = "Region"
default = "us-east-1"

[permissions]
primitives = ["http_get"]
`
	os.WriteFile(filepath.Join(dir, "package.toml"), []byte(toml), 0o644)
	os.WriteFile(filepath.Join(dir, "main.js"), []byte(`return "hello"`), 0o644)
}

// ---------------------------------------------------------------------------
// Source Binding
// ---------------------------------------------------------------------------

func TestGetConfig_SourceBinding(t *testing.T) {
	src := t.TempDir()
	writeTestPackageWithSource(t, src, "src-pkg")

	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)

	secrets, _ := LoadSecretsFrom(filepath.Join(tmpHome, "secrets.json"))
	// Pre-populate shared secret.
	secrets.Set("telegram", "bot_token", "tk-shared-123")

	pm := NewPackageManager(secrets)
	if _, err := pm.Install(src); err != nil {
		t.Fatal(err)
	}

	// Source binding should resolve.
	cfg, err := pm.GetConfig("src-pkg")
	if err != nil {
		t.Fatal(err)
	}
	if cfg["bot_token"] != "tk-shared-123" {
		t.Errorf("bot_token = %q, want %q (from source binding)", cfg["bot_token"], "tk-shared-123")
	}

	// Package-scoped override should win.
	if err := pm.SetConfig("src-pkg", "bot_token", "tk-override"); err != nil {
		t.Fatal(err)
	}
	cfg, _ = pm.GetConfig("src-pkg")
	if cfg["bot_token"] != "tk-override" {
		t.Errorf("bot_token = %q, want %q (package-scoped override)", cfg["bot_token"], "tk-override")
	}
}

func TestGetConfig_SourceMissing(t *testing.T) {
	src := t.TempDir()
	writeTestPackageWithSource(t, src, "src-miss")

	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)

	secrets, _ := LoadSecretsFrom(filepath.Join(tmpHome, "secrets.json"))
	// Do NOT set telegram/bot_token — source is missing.

	pm := NewPackageManager(secrets)
	if _, err := pm.Install(src); err != nil {
		t.Fatal(err)
	}

	cfg, err := pm.GetConfig("src-miss")
	if err != nil {
		t.Fatal(err)
	}
	// Should fall through to default (empty for bot_token).
	if cfg["bot_token"] != "" {
		t.Errorf("bot_token = %q, want empty (source missing, no default)", cfg["bot_token"])
	}
}

func TestLoadPackageToml_InvalidSource(t *testing.T) {
	dir := t.TempDir()
	toml := `[meta]
id = "bad-source"
name = "Bad Source"
version = "1.0.0"
description = "test"

[[config]]
key = "token"
label = "Token"
source = "no-slash"

[permissions]
primitives = []
`
	os.WriteFile(filepath.Join(dir, "package.toml"), []byte(toml), 0o644)

	pkg, err := LoadPackageToml(filepath.Join(dir, "package.toml"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Invalid source should be cleared.
	if pkg.Config[0].Source != "" {
		t.Errorf("source = %q, want empty (invalid format should be cleared)", pkg.Config[0].Source)
	}
}
