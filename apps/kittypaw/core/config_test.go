package core

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/BurntSushi/toml"
)

func TestBindOrDefault(t *testing.T) {
	tests := []struct {
		bind string
		want string
	}{
		{"", ":3000"},
		{":8080", ":8080"},
		{"0.0.0.0:9000", "0.0.0.0:9000"},
	}
	for _, tt := range tests {
		cfg := ServerConfig{Bind: tt.bind}
		got := cfg.BindOrDefault()
		if got != tt.want {
			t.Errorf("BindOrDefault(%q) = %q, want %q", tt.bind, got, tt.want)
		}
	}
}

func TestConfigPathForAccount(t *testing.T) {
	t.Setenv("KITTYPAW_CONFIG_DIR", t.TempDir())
	got, err := ConfigPathForAccount("alice")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(got, filepath.Join("accounts", "alice", "config.toml")) {
		t.Fatalf("ConfigPathForAccount = %q", got)
	}
}

func TestConfigPathForAccountRejectsInvalidID(t *testing.T) {
	t.Setenv("KITTYPAW_CONFIG_DIR", t.TempDir())
	if _, err := ConfigPathForAccount("../bad"); err == nil {
		t.Fatal("expected invalid account id error")
	}
}

func TestConfigV2ShapeParsing(t *testing.T) {
	tomlContent := `
version = 2
is_shared = true
freeform_fallback = true
autonomy_level = "full"
default_staff = "default"

[llm]
default = "main"
fallback = "backup"

[[llm.models]]
id = "main"
provider = "openai"
model = "gpt-5.5"
credential = "openai"
max_tokens = 4096

[[llm.models]]
id = "backup"
provider = "anthropic"
model = "claude-sonnet-4-6"
credential = "anthropic"
max_tokens = 4096

[[channels]]
id = "telegram"
type = "telegram"
allowed_chat_ids = ["54076829"]
allowed_user_ids = ["111222333"]

[[channels]]
id = "kakao"
type = "kakao_talk"

[workspace]
default = "home"
live_index = true

[[workspace.roots]]
alias = "home"
path = "/Users/jinto/Documents/kittypaw/jinto"
access = "read_write"
`

	var cfg Config
	if _, err := toml.Decode(tomlContent, &cfg); err != nil {
		t.Fatalf("decode: %v", err)
	}

	if cfg.Version != 2 {
		t.Fatalf("Version = %d, want 2", cfg.Version)
	}
	if !cfg.IsSharedAccount() {
		t.Fatal("is_shared=true must mark account as shared")
	}
	if cfg.LLM.Default != "main" || cfg.LLM.Fallback != "backup" {
		t.Fatalf("LLM default/fallback = %q/%q", cfg.LLM.Default, cfg.LLM.Fallback)
	}
	if cfg.DefaultStaff != "default" {
		t.Fatalf("DefaultStaff = %q, want default", cfg.DefaultStaff)
	}
	if got := cfg.DefaultModel(); got == nil || got.ID != "main" || got.Credential != "openai" {
		t.Fatalf("DefaultModel = %#v, want main/openai", got)
	}
	if got := cfg.FallbackModel(); got == nil || got.ID != "backup" || got.Credential != "anthropic" {
		t.Fatalf("FallbackModel = %#v, want backup/anthropic", got)
	}
	if len(cfg.Channels) != 2 {
		t.Fatalf("Channels len = %d", len(cfg.Channels))
	}
	if cfg.Channels[0].ID != "telegram" || cfg.Channels[0].ChannelType != ChannelTelegram {
		t.Fatalf("telegram channel = %#v", cfg.Channels[0])
	}
	if got := cfg.Channels[0].AllowedChatIDs; len(got) != 1 || got[0] != "54076829" {
		t.Fatalf("AllowedChatIDs = %v", got)
	}
	if got := cfg.Channels[0].AllowedUserIDs; len(got) != 1 || got[0] != "111222333" {
		t.Fatalf("AllowedUserIDs = %v", got)
	}
	if cfg.Channels[1].ID != "kakao" || cfg.Channels[1].ChannelType != ChannelKakaoTalk {
		t.Fatalf("kakao channel = %#v", cfg.Channels[1])
	}
	if cfg.Workspace.Default != "home" {
		t.Fatalf("workspace default = %q", cfg.Workspace.Default)
	}
	if got := cfg.WorkspaceRoots(); len(got) != 1 || got[0].Alias != "home" || got[0].Path == "" {
		t.Fatalf("WorkspaceRoots = %#v", got)
	}
}

func TestLoadConfigMigratesLegacyProfilesToStaff(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "config.toml")
	tomlContent := `
default_profile = "finance"

[[profiles]]
id = "finance"
nick = "Finance"
channels = ["telegram"]

[[profiles]]
id = "ops"
nick = "Ops"
channels = ["kakao"]
`
	if err := os.WriteFile(path, []byte(tomlContent), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if cfg.DefaultStaff != "finance" {
		t.Fatalf("DefaultStaff = %q, want finance", cfg.DefaultStaff)
	}
	if len(cfg.Staff) != 2 {
		t.Fatalf("Staff len = %d, want 2: %+v", len(cfg.Staff), cfg.Staff)
	}
	if cfg.Staff[0].ID != "finance" || cfg.Staff[0].Nick != "Finance" || len(cfg.Staff[0].Channels) != 1 || cfg.Staff[0].Channels[0] != "telegram" {
		t.Fatalf("staff[0] = %+v", cfg.Staff[0])
	}
	if cfg.Staff[1].ID != "ops" || cfg.Staff[1].Nick != "Ops" || len(cfg.Staff[1].Channels) != 1 || cfg.Staff[1].Channels[0] != "kakao" {
		t.Fatalf("staff[1] = %+v", cfg.Staff[1])
	}
}

func TestLoadConfigPrefersStaffOverLegacyProfiles(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "config.toml")
	tomlContent := `
default_profile = "legacy"
default_staff = "modern"

[[profiles]]
id = "legacy"
nick = "Legacy"
channels = ["telegram"]

[[staff]]
id = "modern"
nick = "Modern"
channels = ["web"]
`
	if err := os.WriteFile(path, []byte(tomlContent), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if cfg.DefaultStaff != "modern" {
		t.Fatalf("DefaultStaff = %q, want modern", cfg.DefaultStaff)
	}
	if len(cfg.Staff) != 1 || cfg.Staff[0].ID != "modern" || cfg.Staff[0].Nick != "Modern" || len(cfg.Staff[0].Channels) != 1 || cfg.Staff[0].Channels[0] != "web" {
		t.Fatalf("Staff = %+v, want only modern staff", cfg.Staff)
	}
}

func TestFeatureFlagsUseRunnerVocabulary(t *testing.T) {
	tomlContent := `
[features]
background_runners = true
`
	var cfg Config
	if _, err := toml.Decode(tomlContent, &cfg); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !cfg.Features.BackgroundRunners {
		t.Fatal("background_runners should enable BackgroundRunners")
	}
}

func TestMCPServerConfigEnvFromParsing(t *testing.T) {
	tomlContent := `
[[mcp_servers]]
name = "gmail"
command = "gmail-mcp"
args = ["--stdio"]

[mcp_servers.env]
STATIC_VALUE = "present"

[mcp_servers.env_from]
GMAIL_ACCESS_TOKEN = "oauth-gmail/access_token"
`
	var cfg Config
	if _, err := toml.Decode(tomlContent, &cfg); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(cfg.MCPServers) != 1 {
		t.Fatalf("MCPServers len = %d", len(cfg.MCPServers))
	}
	server := cfg.MCPServers[0]
	if server.Env["STATIC_VALUE"] != "present" {
		t.Fatalf("static env did not parse: %#v", server.Env)
	}
	if server.EnvFrom["GMAIL_ACCESS_TOKEN"] != "oauth-gmail/access_token" {
		t.Fatalf("env_from did not parse: %#v", server.EnvFrom)
	}
}

func TestTeamSpaceConfigParsing(t *testing.T) {
	tomlContent := `
is_shared = true

[team_space]
members = ["alice", "bob"]
`
	var cfg Config
	if _, err := toml.Decode(tomlContent, &cfg); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !cfg.IsTeamSpaceAccount() {
		t.Fatal("is_shared=true must mark account as team space")
	}
	if got := cfg.TeamSpace.Members; len(got) != 2 || got[0] != "alice" || got[1] != "bob" {
		t.Fatalf("TeamSpace.Members = %#v, want alice,bob", got)
	}
	if !cfg.TeamSpaceHasMember("alice") {
		t.Fatal("alice must be recognized as a team-space member")
	}
}

func TestTeamSpaceConfigDefaultsDenyAll(t *testing.T) {
	var cfg Config
	if _, err := toml.Decode(`is_shared = true`, &cfg); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !cfg.IsTeamSpaceAccount() {
		t.Fatal("is_shared=true must mark account as team space")
	}
	if len(cfg.TeamSpace.Members) != 0 {
		t.Fatalf("missing [team_space].members must default empty, got %#v", cfg.TeamSpace.Members)
	}
	if cfg.TeamSpaceHasMember("alice") {
		t.Fatal("empty team-space members must deny all accounts")
	}
}

func TestLegacyShareParsingStillLoads(t *testing.T) {
	tomlContent := `
is_shared = true

[share.alice]
read = ["memory/weather.json"]
`
	var cfg Config
	if _, err := toml.Decode(tomlContent, &cfg); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(cfg.Share) != 1 || len(cfg.Share["alice"].Read) != 1 {
		t.Fatalf("legacy Share did not parse: %#v", cfg.Share)
	}
	if cfg.TeamSpaceHasMember("alice") {
		t.Fatal("[share.alice] must not imply team-space membership")
	}
}

func TestConfigV2SecretsHydration(t *testing.T) {
	t.Setenv("KITTYPAW_CONFIG_DIR", t.TempDir())
	secrets, err := LoadAccountSecrets("jinto")
	if err != nil {
		t.Fatal(err)
	}
	if err := secrets.Set("llm/openai", "api_key", "sk-openai"); err != nil {
		t.Fatal(err)
	}
	if err := secrets.Set("channel/telegram", "bot_token", "tg-token"); err != nil {
		t.Fatal(err)
	}
	if err := secrets.Set("channel/kakao", "ws_url", "wss://kakao.kittypaw.app/ws/token"); err != nil {
		t.Fatal(err)
	}

	cfg := Config{
		LLM: LLMConfig{
			Default: "main",
			Models: []ModelConfig{{
				ID:         "main",
				Provider:   "openai",
				Model:      "gpt-5.5",
				Credential: "openai",
			}},
		},
		Channels: []ChannelConfig{
			{ID: "telegram", ChannelType: ChannelTelegram},
			{ID: "kakao", ChannelType: ChannelKakaoTalk},
		},
	}

	model, ok := cfg.RuntimeDefaultModel(secrets)
	if !ok {
		t.Fatal("RuntimeDefaultModel not found")
	}
	if model.APIKey != "sk-openai" {
		t.Fatalf("hydrated model APIKey = %q", model.APIKey)
	}

	InjectChannelSecrets("jinto", cfg.Channels)
	if cfg.Channels[0].Token != "tg-token" {
		t.Fatalf("telegram token = %q", cfg.Channels[0].Token)
	}
	if cfg.Channels[1].KakaoWSURL != "wss://kakao.kittypaw.app/ws/token" {
		t.Fatalf("kakao ws url = %q", cfg.Channels[1].KakaoWSURL)
	}
}

func TestPermissionPolicyParsing(t *testing.T) {
	tomlContent := `
autonomy_level = "supervised"

[permissions]
require_approval = ["Shell.exec", "Git.push", "File.write"]
timeout_seconds = 60
`
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "config.toml")
	if err := os.WriteFile(path, []byte(tomlContent), 0o644); err != nil {
		t.Fatal(err)
	}

	var cfg Config
	if _, err := toml.DecodeFile(path, &cfg); err != nil {
		t.Fatalf("decode: %v", err)
	}

	if len(cfg.Permissions.RequireApproval) != 3 {
		t.Fatalf("expected 3 require_approval entries, got %d", len(cfg.Permissions.RequireApproval))
	}
	if cfg.Permissions.RequireApproval[0] != "Shell.exec" {
		t.Errorf("expected Shell.exec, got %s", cfg.Permissions.RequireApproval[0])
	}
	if cfg.Permissions.TimeoutSeconds != 60 {
		t.Errorf("expected timeout 60, got %d", cfg.Permissions.TimeoutSeconds)
	}
}

// TestFamilyShareParsing enforces legacy share TOML compatibility for team-space accounts.
// The shape ([share.<peer>] read=[...]) remains supported for existing installs,
// so this regression pins it against config refactors that would break migration.
func TestFamilyShareParsing(t *testing.T) {
	tomlContent := `
is_shared = true

[share.family]
read = ["memory/weather.json", "memory/household.json"]

[share.alice]
read = ["summary.md"]
`
	var cfg Config
	if _, err := toml.Decode(tomlContent, &cfg); err != nil {
		t.Fatalf("decode: %v", err)
	}

	if !cfg.IsSharedAccount() {
		t.Errorf("IsSharedAccount=true expected")
	}
	if len(cfg.Share) != 2 {
		t.Fatalf("expected 2 share peers, got %d: %#v", len(cfg.Share), cfg.Share)
	}
	family := cfg.Share["family"]
	if len(family.Read) != 2 || family.Read[0] != "memory/weather.json" {
		t.Errorf("share.family.read wrong: %v", family.Read)
	}
	alice := cfg.Share["alice"]
	if len(alice.Read) != 1 || alice.Read[0] != "summary.md" {
		t.Errorf("share.alice.read wrong: %v", alice.Read)
	}
}

// TestFamilyShareDefaults locks in the zero-state contract — a personal
// account config with no [share] blocks must decode to IsFamily=false and
// a nil Share map. If this drifts (e.g. share becomes a required field),
// every existing account breaks at server start.
func TestFamilyShareDefaults(t *testing.T) {
	var cfg Config
	if _, err := toml.Decode(`autonomy_level = "full"`, &cfg); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if cfg.IsFamily {
		t.Error("IsFamily should default to false")
	}
	if cfg.Share != nil {
		t.Errorf("Share should default to nil, got %#v", cfg.Share)
	}
}

func TestPermissionPolicyDefaults(t *testing.T) {
	// When [permissions] is omitted, RequireApproval should be nil.
	tomlContent := `autonomy_level = "supervised"`

	var cfg Config
	if _, err := toml.Decode(tomlContent, &cfg); err != nil {
		t.Fatalf("decode: %v", err)
	}

	if cfg.Permissions.RequireApproval != nil {
		t.Errorf("expected nil RequireApproval, got %v", cfg.Permissions.RequireApproval)
	}
	if cfg.Permissions.TimeoutSeconds != 0 {
		t.Errorf("expected 0 timeout, got %d", cfg.Permissions.TimeoutSeconds)
	}

	// DefaultRequireApproval should have sensible entries.
	if len(DefaultRequireApproval) < 4 {
		t.Errorf("DefaultRequireApproval too short: %v", DefaultRequireApproval)
	}
	for _, want := range []string{
		"Browser.open",
		"Browser.navigate",
		"Browser.click",
		"Browser.type",
		"Browser.evaluate",
		"Browser.close",
		"Skill.uninstall",
	} {
		if !slices.Contains(DefaultRequireApproval, want) {
			t.Fatalf("DefaultRequireApproval missing %s: %v", want, DefaultRequireApproval)
		}
	}
}

func TestBrowserConfigDefaults(t *testing.T) {
	cfg := DefaultConfig()
	if !cfg.Browser.Enabled {
		t.Fatal("browser should be enabled by default")
	}
	if cfg.Browser.Headless {
		t.Fatal("browser should default to visible managed Chrome")
	}
	if cfg.Browser.ChromePath != "" {
		t.Fatalf("ChromePath = %q, want empty auto-detect", cfg.Browser.ChromePath)
	}
	if cfg.Browser.TimeoutSeconds != 15 {
		t.Fatalf("TimeoutSeconds = %d, want 15", cfg.Browser.TimeoutSeconds)
	}
	if cfg.Browser.AllowedHosts != nil {
		t.Fatalf("AllowedHosts = %#v, want nil default", cfg.Browser.AllowedHosts)
	}
}

func TestRuntimeConfigDefaultsAndParsing(t *testing.T) {
	cfg := DefaultConfig()
	if cfg.Runtime.MaxConcurrentTurnsPerAccount != 1 {
		t.Fatalf("MaxConcurrentTurnsPerAccount = %d, want 1", cfg.Runtime.MaxConcurrentTurnsPerAccount)
	}
	if cfg.Runtime.MaxQueuedTurnsPerAccount != 32 {
		t.Fatalf("MaxQueuedTurnsPerAccount = %d, want 32", cfg.Runtime.MaxQueuedTurnsPerAccount)
	}
	if cfg.Runtime.MaxConcurrentTurnsPerConversation != 1 {
		t.Fatalf("MaxConcurrentTurnsPerConversation = %d, want 1", cfg.Runtime.MaxConcurrentTurnsPerConversation)
	}
	if cfg.Runtime.MaxConcurrentScheduledJobs != 2 {
		t.Fatalf("MaxConcurrentScheduledJobs = %d, want 2", cfg.Runtime.MaxConcurrentScheduledJobs)
	}

	tomlContent := `
[runtime]
max_concurrent_turns_per_account = 3
max_queued_turns_per_account = 7
max_concurrent_turns_per_conversation = 2
max_concurrent_scheduled_jobs = 4
`
	var parsed Config
	if _, err := toml.Decode(tomlContent, &parsed); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if parsed.Runtime.MaxConcurrentTurnsPerAccount != 3 ||
		parsed.Runtime.MaxQueuedTurnsPerAccount != 7 ||
		parsed.Runtime.MaxConcurrentTurnsPerConversation != 2 ||
		parsed.Runtime.MaxConcurrentScheduledJobs != 4 {
		t.Fatalf("parsed runtime config = %#v", parsed.Runtime)
	}
}

func TestWebConfigParsing(t *testing.T) {
	tomlContent := `
[web]
read_backend = "firecrawl"
search_backend = "duckduckgo"
firecrawl_api_url = "https://firecrawl.example.com"
`
	var cfg Config
	if _, err := toml.Decode(tomlContent, &cfg); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if cfg.Web.ReadBackend != "firecrawl" {
		t.Fatalf("ReadBackend = %q", cfg.Web.ReadBackend)
	}
	if cfg.Web.SearchBackend != "duckduckgo" {
		t.Fatalf("SearchBackend = %q", cfg.Web.SearchBackend)
	}
	if cfg.Web.FirecrawlURL != "https://firecrawl.example.com" {
		t.Fatalf("FirecrawlURL = %q", cfg.Web.FirecrawlURL)
	}
}

func TestBrowserConfigParsing(t *testing.T) {
	tomlContent := `
[browser]
enabled = false
headless = true
chrome_path = "/opt/chrome"
allowed_hosts = ["localhost", "127.0.0.1"]
timeout_seconds = 9
`
	var cfg Config
	if _, err := toml.Decode(tomlContent, &cfg); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if cfg.Browser.Enabled {
		t.Fatal("enabled should parse false")
	}
	if !cfg.Browser.Headless {
		t.Fatal("headless should parse true")
	}
	if cfg.Browser.ChromePath != "/opt/chrome" {
		t.Fatalf("ChromePath = %q", cfg.Browser.ChromePath)
	}
	if cfg.Browser.TimeoutSeconds != 9 {
		t.Fatalf("TimeoutSeconds = %d", cfg.Browser.TimeoutSeconds)
	}
	if got := cfg.Browser.AllowedHosts; len(got) != 2 || got[0] != "localhost" || got[1] != "127.0.0.1" {
		t.Fatalf("AllowedHosts = %#v", got)
	}
}
