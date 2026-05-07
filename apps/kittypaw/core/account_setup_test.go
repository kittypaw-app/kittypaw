package core

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestInitAccount_HappyPath_Personal(t *testing.T) {
	accountsDir := t.TempDir()

	tt, err := InitAccount(accountsDir, "alice", AccountOpts{
		TelegramToken: "12345:alice-token",
		AdminChatID:   "111",
	})
	if err != nil {
		t.Fatalf("InitAccount: %v", err)
	}
	if tt == nil {
		t.Fatal("InitAccount returned nil account")
	}
	if tt.ID != "alice" {
		t.Errorf("ID = %q, want alice", tt.ID)
	}

	dir := filepath.Join(accountsDir, "alice")
	for _, sub := range []string{"data", "skills", "staff", "packages"} {
		if info, err := os.Stat(filepath.Join(dir, sub)); err != nil || !info.IsDir() {
			t.Errorf("expected subdir %q, err=%v", sub, err)
		}
	}

	cfgPath := filepath.Join(dir, "config.toml")
	cfg, err := LoadConfig(cfgPath)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if cfg.IsFamily {
		t.Error("personal account should not have IsFamily=true")
	}
	if len(cfg.Channels) != 1 || cfg.Channels[0].ChannelType != ChannelTelegram {
		t.Errorf("expected one telegram channel, got %+v", cfg.Channels)
	}
	if cfg.Channels[0].Token != "" {
		t.Errorf("config token = %q, want empty", cfg.Channels[0].Token)
	}
	if got := cfg.Channels[0].AllowedChatIDs; len(got) != 1 || got[0] != "111" {
		t.Errorf("allowed_chat_ids = %v, want [111]", got)
	}
	secrets, err := LoadSecretsFrom(filepath.Join(dir, "secrets.json"))
	if err != nil {
		t.Fatalf("LoadSecretsFrom: %v", err)
	}
	if token, ok := secrets.Get("channel/telegram", "bot_token"); !ok || token != "12345:alice-token" {
		t.Errorf("telegram secret = (%q, %v), want token true", token, ok)
	}

	// Account config stays private, even though channel secrets live in secrets.json.
	if runtime.GOOS != "windows" {
		info, err := os.Stat(cfgPath)
		if err != nil {
			t.Fatalf("stat config: %v", err)
		}
		if info.Mode().Perm() != 0o600 {
			t.Errorf("config.toml perm = %o, want 0600", info.Mode().Perm())
		}
	}

	if _, err := os.Stat(filepath.Join(accountsDir, ".alice.staging")); !os.IsNotExist(err) {
		t.Errorf("staging dir should be gone after commit, stat err=%v", err)
	}
}

func TestInitAccount_HappyPath_Family(t *testing.T) {
	accountsDir := t.TempDir()

	tt, err := InitAccount(accountsDir, "family", AccountOpts{IsFamily: true})
	if err != nil {
		t.Fatalf("InitAccount family: %v", err)
	}

	cfg, err := LoadConfig(filepath.Join(tt.BaseDir, "config.toml"))
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if !cfg.IsSharedAccount() {
		t.Error("expected IsSharedAccount=true")
	}
	if len(cfg.Channels) != 0 {
		t.Errorf("team-space account must declare no channels, got %+v", cfg.Channels)
	}
}

func TestInitAccount_HappyPath_Kakao(t *testing.T) {
	accountsDir := t.TempDir()
	wsURL := "wss://relay.example.com/ws/kakao-token"

	tt, err := InitAccount(accountsDir, "alice", AccountOpts{
		KakaoEnabled:    true,
		KakaoRelayWSURL: wsURL,
		APIServerURL:    DefaultAPIServerURL,
	})
	if err != nil {
		t.Fatalf("InitAccount kakao: %v", err)
	}

	cfg, err := LoadConfig(filepath.Join(tt.BaseDir, "config.toml"))
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if len(cfg.Channels) != 1 || cfg.Channels[0].ChannelType != ChannelKakaoTalk {
		t.Fatalf("expected one kakao_talk channel, got %+v", cfg.Channels)
	}

	secrets, err := LoadSecretsFrom(filepath.Join(tt.BaseDir, "secrets.json"))
	if err != nil {
		t.Fatalf("LoadSecretsFrom: %v", err)
	}
	if got, ok := secrets.Get("channel/kakao", "ws_url"); !ok || got != wsURL {
		t.Fatalf("channel/kakao ws_url = (%q, %v), want %q true", got, ok, wsURL)
	}
	if got, ok := NewAPITokenManager("", secrets).LoadKakaoRelayWSURL(DefaultAPIServerURL); !ok || got != wsURL {
		t.Fatalf("host kakao ws_url = (%q, %v), want %q true", got, ok, wsURL)
	}
	if got, ok := secrets.Get("kittypaw-api", "api_url"); !ok || got != DefaultAPIServerURL {
		t.Fatalf("kittypaw-api api_url = (%q, %v), want %q true", got, ok, DefaultAPIServerURL)
	}
}

func TestInitAccountCreatesLocalAuthUser(t *testing.T) {
	root := t.TempDir()
	t.Setenv("KITTYPAW_CONFIG_DIR", root)
	accountsDir := filepath.Join(root, "accounts")

	if _, err := InitAccount(accountsDir, "alice", AccountOpts{
		IsFamily:      true,
		LocalPassword: "pw123",
	}); err != nil {
		t.Fatalf("InitAccount: %v", err)
	}

	auth := NewLocalAuthStore(filepath.Join(root, "accounts"))
	if ok, err := auth.VerifyPassword("alice", "pw123"); err != nil || !ok {
		t.Fatalf("VerifyPassword = (%v, %v), want true nil", ok, err)
	}
}

func TestInitAccountAuthDuplicateRollsBackStaging(t *testing.T) {
	root := t.TempDir()
	t.Setenv("KITTYPAW_CONFIG_DIR", root)
	accountsDir := filepath.Join(root, "accounts")
	auth := NewLocalAuthStore(filepath.Join(root, "accounts"))
	if err := auth.CreateUser("alice", "existing"); err != nil {
		t.Fatalf("CreateUser: %v", err)
	}

	_, err := InitAccount(accountsDir, "alice", AccountOpts{
		IsFamily:      true,
		LocalPassword: "new",
	})
	if !errors.Is(err, ErrAccountExists) {
		t.Fatalf("InitAccount err = %v, want ErrAccountExists", err)
	}
	if _, err := os.Stat(filepath.Join(accountsDir, "alice", "account.toml")); err != nil {
		t.Fatalf("existing account auth file should remain after duplicate, stat err=%v", err)
	}
	if _, err := os.Stat(filepath.Join(accountsDir, ".alice.staging")); !os.IsNotExist(err) {
		t.Fatalf("staging dir should be removed after auth duplicate, stat err=%v", err)
	}
}

// Re-adding must not clobber existing DB/secrets/skills.
func TestInitAccount_DuplicateID(t *testing.T) {
	accountsDir := t.TempDir()
	if _, err := InitAccount(accountsDir, "alice", AccountOpts{TelegramToken: "12345:a"}); err != nil {
		t.Fatalf("first InitAccount: %v", err)
	}

	marker := filepath.Join(accountsDir, "alice", "data", "do-not-delete")
	if err := os.WriteFile(marker, []byte("x"), 0o600); err != nil {
		t.Fatalf("write marker: %v", err)
	}

	_, err := InitAccount(accountsDir, "alice", AccountOpts{TelegramToken: "99999:b"})
	if !errors.Is(err, ErrAccountExists) {
		t.Fatalf("expected ErrAccountExists, got %v", err)
	}
	if _, err := os.Stat(marker); err != nil {
		t.Errorf("marker file missing — existing account was clobbered: %v", err)
	}
}

// Collision must surface before any filesystem write — otherwise the error only appears at server startup.
func TestInitAccount_DuplicateTelegramToken(t *testing.T) {
	accountsDir := t.TempDir()
	if _, err := InitAccount(accountsDir, "alice", AccountOpts{TelegramToken: "shared"}); err != nil {
		t.Fatalf("alice: %v", err)
	}

	_, err := InitAccount(accountsDir, "bob", AccountOpts{TelegramToken: "shared"})
	if err == nil {
		t.Fatal("expected duplicate-token error, got nil")
	}
	if !strings.Contains(err.Error(), "telegram bot_token") {
		t.Errorf("error should cite telegram bot_token: %q", err.Error())
	}

	if _, err := os.Stat(filepath.Join(accountsDir, "bob")); !os.IsNotExist(err) {
		t.Errorf("bob dir should not exist after collision, err=%v", err)
	}
	if _, err := os.Stat(filepath.Join(accountsDir, ".bob.staging")); !os.IsNotExist(err) {
		t.Errorf("bob staging should be cleaned up, err=%v", err)
	}
}

func TestInitAccount_DuplicateKakaoRelayURL(t *testing.T) {
	accountsDir := t.TempDir()
	wsURL := "wss://relay.example.com/ws/shared"
	if _, err := InitAccount(accountsDir, "alice", AccountOpts{
		KakaoEnabled:    true,
		KakaoRelayWSURL: wsURL,
		APIServerURL:    DefaultAPIServerURL,
	}); err != nil {
		t.Fatalf("alice: %v", err)
	}

	_, err := InitAccount(accountsDir, "bob", AccountOpts{
		KakaoEnabled:    true,
		KakaoRelayWSURL: wsURL,
		APIServerURL:    DefaultAPIServerURL,
	})
	if err == nil {
		t.Fatal("expected duplicate kakao relay URL error, got nil")
	}
	if !strings.Contains(err.Error(), "kakao relay URL") {
		t.Errorf("error should cite kakao relay URL: %q", err.Error())
	}
	if _, err := os.Stat(filepath.Join(accountsDir, "bob")); !os.IsNotExist(err) {
		t.Errorf("bob dir should not exist after collision, err=%v", err)
	}
}

// Team-space-no-channels invariant must reject before any file is written.
func TestInitAccount_FamilyWithToken(t *testing.T) {
	accountsDir := t.TempDir()

	_, err := InitAccount(accountsDir, "family", AccountOpts{
		IsFamily:      true,
		TelegramToken: "12345:family",
	})
	if err == nil {
		t.Fatal("expected error for team-space + telegram token, got nil")
	}
	if !strings.Contains(err.Error(), "family") {
		t.Errorf("error should cite family: %q", err.Error())
	}
	if _, err := os.Stat(filepath.Join(accountsDir, "family")); !os.IsNotExist(err) {
		t.Errorf("family dir should not exist after rejection")
	}
}

func TestInitAccount_FamilyWithKakao(t *testing.T) {
	accountsDir := t.TempDir()

	_, err := InitAccount(accountsDir, "family", AccountOpts{
		IsFamily:        true,
		KakaoEnabled:    true,
		KakaoRelayWSURL: "wss://relay.example.com/ws/family",
	})
	if err == nil {
		t.Fatal("expected error for family + kakao, got nil")
	}
	if !strings.Contains(err.Error(), "team-space account") {
		t.Errorf("error should cite team-space account: %q", err.Error())
	}
	if _, err := os.Stat(filepath.Join(accountsDir, "family")); !os.IsNotExist(err) {
		t.Errorf("family dir should not exist after rejection")
	}
}

// Accepting "../escape" would be a traversal vulnerability.
func TestInitAccount_InvalidID(t *testing.T) {
	accountsDir := t.TempDir()

	_, err := InitAccount(accountsDir, "../escape", AccountOpts{TelegramToken: "x"})
	if err == nil {
		t.Fatal("expected error for invalid account id")
	}
}

// Without this, SIGKILL/disk-full during provisioning would one-shot break the feature.
func TestInitAccount_StagingRecovery(t *testing.T) {
	accountsDir := t.TempDir()

	stale := filepath.Join(accountsDir, ".alice.staging")
	if err := os.MkdirAll(filepath.Join(stale, "data"), 0o755); err != nil {
		t.Fatalf("seed stale staging: %v", err)
	}
	if err := os.WriteFile(filepath.Join(stale, "config.toml"), []byte("garbage"), 0o600); err != nil {
		t.Fatalf("seed stale config: %v", err)
	}

	if _, err := InitAccount(accountsDir, "alice", AccountOpts{TelegramToken: "12345:a"}); err != nil {
		t.Fatalf("InitAccount after staging crash: %v", err)
	}
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Errorf("stale staging should be cleaned up, stat err=%v", err)
	}
	cfg, err := LoadConfig(filepath.Join(accountsDir, "alice", "config.toml"))
	if err != nil {
		t.Fatalf("LoadConfig after recovery: %v", err)
	}
	if len(cfg.Channels) != 1 {
		t.Errorf("expected 1 channel after recovery, got %d", len(cfg.Channels))
	}
}
