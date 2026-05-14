package core

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/BurntSushi/toml"
)

// AutonomyLevel controls how much freedom the runner has.
type AutonomyLevel string

const (
	AutonomyReadonly   AutonomyLevel = "readonly"
	AutonomySupervised AutonomyLevel = "supervised"
	AutonomyFull       AutonomyLevel = "full"
)

// ChannelType identifies a messaging channel backend.
type ChannelType string

const (
	ChannelTelegram  ChannelType = "telegram"
	ChannelSlack     ChannelType = "slack"
	ChannelDiscord   ChannelType = "discord"
	ChannelWeb       ChannelType = "web"
	ChannelDesktop   ChannelType = "desktop"
	ChannelKakaoTalk ChannelType = "kakao_talk"
)

// TopLevelServerConfig holds server-wide settings loaded from server.toml.
// This is separate from per-account Config — it controls the server itself.
type TopLevelServerConfig struct {
	Bind           string `toml:"bind"`
	MasterAPIKey   string `toml:"master_api_key"`
	AccountsDir    string `toml:"accounts_dir"`
	DefaultAccount string `toml:"default_account"`
}

// LoadServerConfig reads server.toml from the given path.
func LoadServerConfig(path string) (*TopLevelServerConfig, error) {
	sc := &TopLevelServerConfig{}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return sc, nil // defaults
		}
		return nil, fmt.Errorf("read server config: %w", err)
	}
	if err := toml.Unmarshal(data, sc); err != nil {
		return nil, fmt.Errorf("parse server config: %w", err)
	}
	return sc, nil
}

// ServerConfigPath returns the path to server.toml in the kittypaw dir.
func ServerConfigPath() (string, error) {
	dir, err := ConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "server.toml"), nil
}

// Config is the top-level application configuration, loaded from TOML.
type Config struct {
	Version          int                 `toml:"version"`
	LLM              LLMConfig           `toml:"llm"`
	Sandbox          SandboxConfig       `toml:"sandbox"`
	Runners          []RunnerConfig      `toml:"runners"`
	Channels         []ChannelConfig     `toml:"channels"`
	AllowedChatIDs   []string            `toml:"-"`
	AllowedUserIDs   []string            `toml:"-"`
	FreeformFallback bool                `toml:"freeform_fallback"`
	Models           []ModelConfig       `toml:"-"`
	STT              STTConfig           `toml:"stt"`
	Features         FeatureFlags        `toml:"features"`
	MCPServers       []MCPServerConfig   `toml:"mcp_servers"`
	AutonomyLevel    AutonomyLevel       `toml:"autonomy_level"`
	PairedChatIDs    []string            `toml:"paired_chat_ids"`
	Server           ServerConfig        `toml:"server"`
	Staff            []StaffConfig       `toml:"staff"`
	DefaultStaff     string              `toml:"default_staff"`
	Reflection       ReflectionConfig    `toml:"reflection"`
	Evolution        EvolutionConfig     `toml:"evolution"`
	Orchestration    OrchestrationConfig `toml:"orchestration"`
	Runtime          RuntimeConfig       `toml:"runtime"`
	Registry         RegistryConfig      `toml:"registry"`
	SkillInstall     SkillInstallConfig  `toml:"skill_install"`
	Permissions      PermissionPolicy    `toml:"permissions"`
	Web              WebConfig           `toml:"web"`
	Browser          BrowserConfig       `toml:"browser"`
	Workspace        WorkspaceConfig     `toml:"workspace"`
	User             UserConfig          `toml:"user"`

	// IsShared marks an account as a team-space/coordinator account. Team
	// spaces run scheduled skills for explicit members and fanout to member
	// accounts, but MUST NOT own chat channels.
	IsShared bool `toml:"is_shared"`
	// IsFamily is a legacy in-memory alias for IsShared retained for
	// compatibility with older callsites and tests.
	IsFamily  bool            `toml:"-"`
	TeamSpace TeamSpaceConfig `toml:"team_space"`

	// Share is the legacy per-path cross-account read allowlist. It remains
	// active for existing read checks and will be superseded by team-space
	// membership in the new read path.
	Share map[string]ShareConfig `toml:"share"`
}

// ShareConfig is the per-peer read allowlist for cross-account filesystem
// access. Read paths are account-relative (e.g. "memory/weather.json") and
// must match exactly after traversal/symlink validation in ValidateSharedReadPath.
// Write sharing is intentionally absent — the S3-lite scope forbids cross-account
// writes so a team-space skill bug can't corrupt alice's store.
type ShareConfig struct {
	Read []string `toml:"read"`
}

// TeamSpaceConfig controls which personal accounts can use a team-space
// account. An empty Members list is deny-all.
type TeamSpaceConfig struct {
	Members []string `toml:"members"`
}

// UserConfig holds user-level settings surfaced to packages via __context__.user.
type UserConfig struct {
	Locale    string  `toml:"locale"`    // e.g. "ko", "en", "ja"
	Timezone  string  `toml:"timezone"`  // e.g. "Asia/Seoul"
	City      string  `toml:"city"`      // e.g. "Seoul"
	Latitude  float64 `toml:"latitude"`  // e.g. 37.57
	Longitude float64 `toml:"longitude"` // e.g. 126.98
}

// WorkspaceConfig controls workspace indexing behavior.
//
// LiveIndex toggles the fsnotify-backed live indexer. When true (default),
// workspace file changes are reflected in the FTS index within one debounce
// window without requiring explicit File.reindex calls. When false, the
// server falls back to v1 behavior: index at startup and on explicit
// Reindex only.
type WorkspaceConfig struct {
	Default   string          `toml:"default"`
	Roots     []WorkspaceRoot `toml:"roots"`
	LiveIndex bool            `toml:"live_index"`
}

type WorkspaceRoot struct {
	Alias  string `toml:"alias"`
	Path   string `toml:"path"`
	Access string `toml:"access"`
}

// WebConfig controls web tool behavior (search backend, etc.).
type WebConfig struct {
	ReadBackend   string `toml:"read_backend"`   // "auto" | "static" | "firecrawl" | "browser"; empty = auto
	SearchBackend string `toml:"search_backend"` // "firecrawl" | "tavily" | "duckduckgo"; empty = auto-detect
	FirecrawlKey  string `toml:"-"`
	FirecrawlURL  string `toml:"firecrawl_api_url"` // self-hosted; default https://api.firecrawl.dev
	TavilyAPIKey  string `toml:"-"`
}

// BrowserConfig controls the managed Chrome/CDP browser tool.
type BrowserConfig struct {
	Enabled        bool     `toml:"enabled"`
	Headless       bool     `toml:"headless"`
	ChromePath     string   `toml:"chrome_path"`
	AllowedHosts   []string `toml:"allowed_hosts"`
	TimeoutSeconds int      `toml:"timeout_seconds"`
}

// SkillInstallConfig controls skill installation behavior.
type SkillInstallConfig struct {
	MdExecutionMode string `toml:"md_execution_mode"` // "prompt" or "native", empty = ask user
}

// PermissionPolicy configures which operations require explicit user approval.
type PermissionPolicy struct {
	RequireApproval []string `toml:"require_approval"`
	TimeoutSeconds  int      `toml:"timeout_seconds"`
}

// DefaultRequireApproval is used when RequireApproval is nil (not configured).
// Skill.installFromRegistry was historically gated here, but channels without
// a Confirmer implementation (CLI chat, web_chat) silently fall through to
// "requires permission approval" and the install fails even when the user
// explicitly agreed in chat. The LLM-level confirm flow now owns first-touch
// consent (see CapabilityBlock + auto-discovery in engine/prompt.go), and
// the registry layer enforces SourceHash / provenance for supply-chain
// integrity. Operators wanting a hard system gate can re-add the entry in
// their config.toml [permissions] require_approval list.
var DefaultRequireApproval = []string{
	"Shell.exec", "Git.add", "Git.commit", "Git.push", "Git.pull",
	"File.write", "File.append", "File.edit", "File.mkdir", "File.delete",
	"Skill.uninstall",
	"Browser.open", "Browser.navigate", "Browser.click", "Browser.type", "Browser.evaluate", "Browser.close",
}

// LLMConfig holds the primary LLM provider settings.
type LLMConfig struct {
	Default   string        `toml:"default"`
	Fallback  string        `toml:"fallback"`
	Models    []ModelConfig `toml:"models"`
	Provider  string        `toml:"-"`
	APIKey    string        `toml:"-"`
	Model     string        `toml:"-"`
	MaxTokens uint32        `toml:"-"`
	BaseURL   string        `toml:"-"`
}

// ModelConfig defines an additional named model.
type ModelConfig struct {
	ID            string               `toml:"id"`
	Name          string               `toml:"-"`
	Provider      string               `toml:"provider"`
	Model         string               `toml:"model"`
	Credential    string               `toml:"credential"`
	APIKey        string               `toml:"-"`
	MaxTokens     uint32               `toml:"max_tokens"`
	Default       bool                 `toml:"-"`
	BaseURL       string               `toml:"base_url"`
	ContextWindow uint32               `toml:"context_window"`
	Tier          *ModelTier           `toml:"tier"`
	RateLimit     ModelRateLimitConfig `toml:"rate_limit"`
}

// ModelRateLimitConfig defines account-local admission limits for one
// configured LLM model. Zero values disable each axis.
type ModelRateLimitConfig struct {
	Pool                  string `toml:"pool"`
	RequestsPerMinute     uint32 `toml:"requests_per_minute"`
	InputTokensPerMinute  uint64 `toml:"input_tokens_per_minute"`
	OutputTokensPerMinute uint64 `toml:"output_tokens_per_minute"`
	TokensPerMinute       uint64 `toml:"tokens_per_minute"`
	RequestsPerDay        uint32 `toml:"requests_per_day"`
	TokensPerDay          uint64 `toml:"tokens_per_day"`
	MaxConcurrent         uint32 `toml:"max_concurrent"`
}

func (r ModelRateLimitConfig) Enabled() bool {
	return r.RequestsPerMinute > 0 ||
		r.InputTokensPerMinute > 0 ||
		r.OutputTokensPerMinute > 0 ||
		r.TokensPerMinute > 0 ||
		r.RequestsPerDay > 0 ||
		r.TokensPerDay > 0 ||
		r.MaxConcurrent > 0
}

// SandboxConfig controls the JavaScript execution sandbox.
type SandboxConfig struct {
	TimeoutSecs   uint64   `toml:"timeout_secs"`
	MemoryLimitMB uint64   `toml:"memory_limit_mb"`
	AllowedPaths  []string `toml:"-"`
	AllowedHosts  []string `toml:"allowed_hosts"`
}

// STTConfig holds speech-to-text settings.
type STTConfig struct {
	Provider string `toml:"provider"`
	APIKey   string `toml:"-"`
	Language string `toml:"language"`
}

// FeatureFlags toggles experimental features.
type FeatureFlags struct {
	ProgressiveRetry  bool   `toml:"progressive_retry"`
	ContextCompaction bool   `toml:"context_compaction"`
	ModelRouting      bool   `toml:"model_routing"`
	BackgroundRunners bool   `toml:"background_runners"`
	DailyTokenLimit   uint64 `toml:"daily_token_limit"`
	MaxObserveRounds  int    `toml:"max_observe_rounds"` // default 5
}

// ReflectionConfig controls daily pattern analysis.
type ReflectionConfig struct {
	Enabled         bool   `toml:"enabled"`
	Cron            string `toml:"cron"`
	MaxInputChars   uint32 `toml:"max_input_chars"`
	IntentThreshold uint32 `toml:"intent_threshold"`
	TTLDays         uint32 `toml:"ttl_days"`
	WeeklyReportDay uint32 `toml:"weekly_report_day"`
}

// EvolutionConfig controls autonomous skill suggestion.
type EvolutionConfig struct {
	Enabled              bool   `toml:"enabled"`
	ObservationThreshold uint32 `toml:"observation_threshold"`
}

// OrchestrationConfig controls multi-runner PM pattern.
type OrchestrationConfig struct {
	Enabled      bool   `toml:"enabled"`
	MaxDepth     uint32 `toml:"max_depth"`
	MaxDelegates uint32 `toml:"max_delegates"`
}

// RuntimeConfig controls account runtime admission and scheduler concurrency.
type RuntimeConfig struct {
	MaxConcurrentTurnsPerAccount      uint32 `toml:"max_concurrent_turns_per_account"`
	MaxQueuedTurnsPerAccount          uint32 `toml:"max_queued_turns_per_account"`
	MaxConcurrentTurnsPerConversation uint32 `toml:"max_concurrent_turns_per_conversation"`
	MaxConcurrentScheduledJobs        uint32 `toml:"max_concurrent_scheduled_jobs"`
}

// RegistryConfig controls the remote package registry.
type RegistryConfig struct {
	URL string `toml:"url"`
}

// ServerConfig holds HTTP server settings.
type ServerConfig struct {
	Bind           string   `toml:"bind"`
	APIKey         string   `toml:"-"`
	AllowedOrigins []string `toml:"allowed_origins"`
}

func NewServerAPIKey() (string, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("generate server api key: %w", err)
	}
	return "kp_" + base64.RawURLEncoding.EncodeToString(raw), nil
}

func EnsureServerAPIKey(cfg *Config) (bool, error) {
	if cfg == nil || cfg.Server.APIKey != "" {
		return false, nil
	}
	key, err := NewServerAPIKey()
	if err != nil {
		return false, err
	}
	cfg.Server.APIKey = key
	return true, nil
}

// BindOrDefault returns the configured bind address, defaulting to ":3000".
func (s ServerConfig) BindOrDefault() string {
	if s.Bind != "" {
		return s.Bind
	}
	return ":3000"
}

// MCPServerConfig defines an external MCP tool server.
type MCPServerConfig struct {
	Name    string            `toml:"name"`
	Command string            `toml:"command"`
	Args    []string          `toml:"args"`
	Env     map[string]string `toml:"env"`
	EnvFrom map[string]string `toml:"env_from"`
}

// ChannelConfig defines a messaging channel.
type ChannelConfig struct {
	ID             string      `toml:"id"`
	ChannelType    ChannelType `toml:"type"`
	AllowedChatIDs []string    `toml:"allowed_chat_ids"`
	AllowedUserIDs []string    `toml:"allowed_user_ids"`
	Credential     string      `toml:"credential"`
	Token          string      `toml:"-"`
	BindAddr       string      `toml:"bind_addr"`
	KakaoWSURL     string      `toml:"-"` // runtime-injected from secrets (not in config.toml)
}

// InjectKakaoWSURL populates KakaoWSURL on kakao_talk channel configs from
// the named account's secrets. Called by ChannelSpawner.Reconcile so
// hot-reload and initial spawn share the same path. No-op if the account's
// secrets, api_url, or relay URL are missing.
//
// api_url is written by the setup paths under the bare "kittypaw-api"
// namespace of the account's per-account secrets store. When absent — e.g.
// the user only completed the KakaoTalk step and skipped API server
// login — fall back to DefaultAPIServerURL so the host-scoped secret
// saved by wizardKakao still resolves.
func InjectKakaoWSURL(accountID string, channels []ChannelConfig) {
	InjectChannelSecrets(accountID, channels)
}

func InjectChannelSecrets(accountID string, channels []ChannelConfig) {
	secrets, err := LoadAccountSecrets(accountID)
	if err != nil {
		return
	}
	mgr := NewAPITokenManager("", secrets)

	apiURL, ok := secrets.Get("kittypaw-api", "api_url")
	if !ok || apiURL == "" {
		apiURL = DefaultAPIServerURL
	}

	wsURL, _ := mgr.LoadKakaoRelayWSURL(apiURL)

	for i := range channels {
		id := channels[i].SecretID()
		switch channels[i].ChannelType {
		case ChannelTelegram:
			if channels[i].Token == "" {
				if token, ok := secrets.Get("channel/"+id, "bot_token"); ok {
					channels[i].Token = token
				}
			}
		case ChannelKakaoTalk:
			if channels[i].KakaoWSURL == "" {
				if url, ok := secrets.Get("channel/"+id, "ws_url"); ok {
					channels[i].KakaoWSURL = url
				} else {
					channels[i].KakaoWSURL = wsURL
				}
			}
		}
	}
}

// RunnerConfig defines one runner's execution behavior.
type RunnerConfig struct {
	ID            string            `toml:"id"`
	Name          string            `toml:"name"`
	SystemPrompt  string            `toml:"system_prompt"`
	Channels      []string          `toml:"channels"`
	AllowedSkills []SkillPermission `toml:"allowed_skills"`
}

// SkillPermission controls per-skill access for a runner.
type SkillPermission struct {
	Skill              string   `toml:"skill"`
	Methods            []string `toml:"methods"`
	RateLimitPerMinute uint32   `toml:"rate_limit_per_minute"`
}

// StaffConfig defines a switchable staff identity.
type StaffConfig struct {
	ID       string   `toml:"id"`
	Nick     string   `toml:"nick"`
	Channels []string `toml:"channels"`
}

type legacyProfileConfig struct {
	DefaultProfile string        `toml:"default_profile"`
	Profiles       []StaffConfig `toml:"profiles"`
}

// DefaultConfig returns a Config with sensible defaults.
func DefaultConfig() Config {
	return Config{
		Version: 2,
		LLM: LLMConfig{
			Default: "main",
			Models: []ModelConfig{{
				ID:         "main",
				Provider:   "anthropic",
				Model:      ClaudeDefaultModel,
				Credential: "anthropic",
				MaxTokens:  4096,
			}},
			Model:     ClaudeDefaultModel,
			MaxTokens: 4096,
		},
		Sandbox: SandboxConfig{
			TimeoutSecs:   30,
			MemoryLimitMB: 64,
		},
		AutonomyLevel: AutonomyFull,
		DefaultStaff:  "default",
		STT: STTConfig{
			Language: "ko",
		},
		Features: FeatureFlags{
			ProgressiveRetry:  true,
			ContextCompaction: true,
		},
		Reflection: ReflectionConfig{
			Enabled:         true,
			Cron:            "0 0 3 * * *",
			MaxInputChars:   4000,
			IntentThreshold: 3,
			TTLDays:         7,
		},
		Evolution: EvolutionConfig{
			ObservationThreshold: 20,
		},
		Orchestration: OrchestrationConfig{
			MaxDepth:     3,
			MaxDelegates: 5,
		},
		Runtime: RuntimeConfig{
			MaxConcurrentTurnsPerAccount:      1,
			MaxQueuedTurnsPerAccount:          32,
			MaxConcurrentTurnsPerConversation: 1,
			MaxConcurrentScheduledJobs:        2,
		},
		Registry: RegistryConfig{
			URL: DefaultRegistryURL,
		},
		Browser: BrowserConfig{
			Enabled:        true,
			TimeoutSeconds: 15,
		},
		Workspace: WorkspaceConfig{
			Default:   "home",
			LiveIndex: true,
		},
	}
}

// LoadConfig reads and parses a TOML config file.
func LoadConfig(path string) (*Config, error) {
	cfg := DefaultConfig()
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}
	md, err := toml.Decode(string(data), &cfg)
	if err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}
	if err := applyLegacyProfileConfig(&cfg, data, md); err != nil {
		return nil, fmt.Errorf("parse legacy profile config: %w", err)
	}
	cfg.NormalizeRuntimeFields()
	return &cfg, nil
}

func applyLegacyProfileConfig(cfg *Config, data []byte, md toml.MetaData) error {
	var legacy legacyProfileConfig
	if err := toml.Unmarshal(data, &legacy); err != nil {
		return err
	}

	if !md.IsDefined("default_staff") {
		if defaultProfile := strings.TrimSpace(legacy.DefaultProfile); defaultProfile != "" {
			cfg.DefaultStaff = defaultProfile
		}
	}
	if !md.IsDefined("staff") && len(legacy.Profiles) > 0 {
		cfg.Staff = append([]StaffConfig(nil), legacy.Profiles...)
	}
	return nil
}

// ConfigDir returns the user's .kittypaw config directory, creating it if needed.
// On first run after rename, migrates ~/.gopaw → ~/.kittypaw automatically.
// The directory is owned-by-user only (mode 0700) so other OS users on the same
// host cannot read account data, skill sources, or secrets. KITTYPAW_CONFIG_DIR
// overrides the default location — set by init-system units (systemd/launchd).
func ConfigDir() (string, error) {
	if dir := os.Getenv("KITTYPAW_CONFIG_DIR"); dir != "" {
		if err := ensureConfigDirMode(dir); err != nil {
			return "", err
		}
		return dir, nil
	}

	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(home, ".kittypaw")

	// Migrate legacy ~/.gopaw if .kittypaw doesn't exist yet.
	if _, err := os.Stat(dir); os.IsNotExist(err) {
		oldDir := filepath.Join(home, ".gopaw")
		if _, err := os.Stat(oldDir); err == nil {
			if renameErr := os.Rename(oldDir, dir); renameErr != nil {
				slog.Warn("failed to migrate config dir, using legacy path",
					"from", oldDir, "to", dir, "error", renameErr)
				dir = oldDir
			} else {
				slog.Info("migrated config directory", "from", oldDir, "to", dir)
			}
		}
	}

	if err := ensureConfigDirMode(dir); err != nil {
		return "", err
	}
	return dir, nil
}

// ensureConfigDirMode creates dir if missing and enforces mode 0700 even on
// pre-existing directories left over from earlier versions that used 0755.
func ensureConfigDirMode(dir string) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	return os.Chmod(dir, 0o700)
}

// ResolveBaseDir returns baseDir if non-empty, otherwise falls back to ConfigDir.
func ResolveBaseDir(baseDir string) (string, error) {
	if baseDir != "" {
		return baseDir, nil
	}
	return ConfigDir()
}

// ConfigPathForAccount returns the config file path for accountID.
func ConfigPathForAccount(accountID string) (string, error) {
	if err := ValidateAccountID(accountID); err != nil {
		return "", err
	}
	dir, err := ConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "accounts", accountID, "config.toml"), nil
}

// ConfigPath returns the default account's config file path.
func ConfigPath() (string, error) {
	return ConfigPathForAccount(DefaultAccountID)
}

// FindRunner returns the runner config matching the given ID, or nil.
func (c *Config) FindRunner(id string) *RunnerConfig {
	for i := range c.Runners {
		if c.Runners[i].ID == id {
			return &c.Runners[i]
		}
	}
	return nil
}

// DefaultRunner returns the first runner, or nil if none configured.
func (c *Config) DefaultRunner() *RunnerConfig {
	if len(c.Runners) == 0 {
		return nil
	}
	return &c.Runners[0]
}

func (c *Config) NormalizeRuntimeFields() {
	if c == nil {
		return
	}
	if c.Version == 0 {
		c.Version = 2
	}
	if c.IsShared {
		c.IsFamily = true
	}
	if len(c.AllowedChatIDs) == 0 {
		c.AllowedChatIDs = cfgAllowedChatIDs(c)
	}
	if len(c.AllowedUserIDs) == 0 {
		c.AllowedUserIDs = cfgAllowedUserIDs(c)
	}
	if m := c.DefaultModel(); m != nil {
		c.LLM.Provider = m.Provider
		c.LLM.Model = m.Model
		c.LLM.MaxTokens = m.MaxTokens
		c.LLM.BaseURL = m.BaseURL
	}
	if c.LLM.MaxTokens == 0 {
		c.LLM.MaxTokens = 4096
	}
}

// FindModel returns the model config matching the given name, or nil.
func (c *Config) FindModel(name string) *ModelConfig {
	for i := range c.LLM.Models {
		if c.LLM.Models[i].ModelID() == name {
			return &c.LLM.Models[i]
		}
	}
	for i := range c.Models {
		if c.Models[i].ModelID() == name {
			return &c.Models[i]
		}
	}
	return nil
}

// DefaultModel returns the model marked as default, or the first model.
func (c *Config) DefaultModel() *ModelConfig {
	if c == nil {
		return nil
	}
	if c.LLM.Default != "" {
		if m := c.FindModel(c.LLM.Default); m != nil {
			return m
		}
	}
	for i := range c.LLM.Models {
		if c.LLM.Models[i].Default {
			return &c.LLM.Models[i]
		}
	}
	if len(c.LLM.Models) > 0 {
		return &c.LLM.Models[0]
	}
	for i := range c.Models {
		if c.Models[i].Default {
			return &c.Models[i]
		}
	}
	if len(c.Models) > 0 {
		return &c.Models[0]
	}
	return nil
}

func (c *Config) FallbackModel() *ModelConfig {
	if c == nil || c.LLM.Fallback == "" {
		return nil
	}
	return c.FindModel(c.LLM.Fallback)
}

func (c *Config) IsTeamSpaceAccount() bool {
	return c != nil && (c.IsShared || c.IsFamily)
}

func (c *Config) IsSharedAccount() bool {
	return c.IsTeamSpaceAccount()
}

func (c *Config) TeamSpaceHasMember(accountID string) bool {
	if c == nil || !c.IsTeamSpaceAccount() {
		return false
	}
	for _, member := range c.TeamSpace.Members {
		if member == accountID {
			return true
		}
	}
	return false
}

func (c *Config) WorkspaceRoots() []WorkspaceRoot {
	if c == nil {
		return nil
	}
	if len(c.Workspace.Roots) > 0 {
		return c.Workspace.Roots
	}
	roots := make([]WorkspaceRoot, 0, len(c.Sandbox.AllowedPaths))
	for i, p := range c.Sandbox.AllowedPaths {
		alias := c.Workspace.Default
		if alias == "" {
			alias = "home"
		}
		if i > 0 {
			alias = fmt.Sprintf("workspace-%d", i+1)
		}
		roots = append(roots, WorkspaceRoot{Alias: alias, Path: p, Access: "read_write"})
	}
	return roots
}

func (m ModelConfig) ModelID() string {
	if m.ID != "" {
		return m.ID
	}
	return m.Name
}

func (m ModelConfig) SecretID() string {
	if m.Credential != "" {
		return m.Credential
	}
	if m.ID != "" {
		return m.ID
	}
	if m.Provider != "" {
		return m.Provider
	}
	return m.Name
}

func (c ChannelConfig) SecretID() string {
	if c.Credential != "" {
		return c.Credential
	}
	if c.ID != "" {
		return c.ID
	}
	return string(c.ChannelType)
}

func HydrateModelSecrets(model ModelConfig, secrets *SecretsStore) ModelConfig {
	if secrets == nil || model.APIKey != "" {
		return model
	}
	if id := model.SecretID(); id != "" {
		if key, ok := secrets.Get("llm/"+id, "api_key"); ok {
			model.APIKey = key
		}
	}
	return model
}

func (c *Config) RuntimeDefaultModel(secrets *SecretsStore) (ModelConfig, bool) {
	m := c.DefaultModel()
	if m == nil {
		return ModelConfig{}, false
	}
	return HydrateModelSecrets(*m, secrets), true
}

func (c *Config) RuntimeFallbackModel(secrets *SecretsStore) (ModelConfig, bool) {
	m := c.FallbackModel()
	if m == nil {
		return ModelConfig{}, false
	}
	return HydrateModelSecrets(*m, secrets), true
}
