package smoke

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestRemoteConfigRequiresHomeBaseURL(t *testing.T) {
	t.Setenv("HOME_USER_TOKEN", "user-token")
	t.Setenv("HOME_DEVICE_TOKEN", "device-token")
	t.Setenv("HOME_DEVICE_ID", "dev_1")
	t.Setenv("HOME_LOCAL_ACCOUNT_ID", "alice")

	_, err := LoadRemoteConfig()
	if err == nil {
		t.Fatal("LoadRemoteConfig() error = nil, want missing HOME_BASE_URL")
	}
	if !strings.Contains(err.Error(), "HOME_BASE_URL") {
		t.Fatalf("LoadRemoteConfig() error = %v, want HOME_BASE_URL", err)
	}
}

func TestRemoteConfigLoadsRequiredAndOptionalEnv(t *testing.T) {
	t.Setenv("HOME_BASE_URL", "https://home.kittypaw.app/")
	t.Setenv("HOME_USER_TOKEN", "user-token")
	t.Setenv("HOME_DEVICE_TOKEN", "device-token")
	t.Setenv("HOME_DEVICE_ID", "dev_1")
	t.Setenv("HOME_LOCAL_ACCOUNT_ID", "alice")
	t.Setenv("HOME_SMOKE_USER_ID", "user_1")
	t.Setenv("HOME_SMOKE_TIMEOUT", "7s")

	cfg, err := LoadRemoteConfig()
	if err != nil {
		t.Fatalf("LoadRemoteConfig() error = %v", err)
	}
	if cfg.BaseURL != "https://home.kittypaw.app" {
		t.Fatalf("BaseURL = %q, want trimmed Home URL", cfg.BaseURL)
	}
	if cfg.UserToken != "user-token" || cfg.DeviceToken != "device-token" {
		t.Fatalf("tokens not loaded: %#v", cfg)
	}
	if cfg.DeviceID != "dev_1" || cfg.LocalAccountID != "alice" || cfg.UserID != "user_1" {
		t.Fatalf("identity fields not loaded: %#v", cfg)
	}
	if cfg.Timeout != 7*time.Second {
		t.Fatalf("Timeout = %v, want 7s", cfg.Timeout)
	}
}

func TestRemoteConfigRejectsInvalidTimeout(t *testing.T) {
	t.Setenv("HOME_BASE_URL", "https://home.kittypaw.app")
	t.Setenv("HOME_USER_TOKEN", "user-token")
	t.Setenv("HOME_DEVICE_TOKEN", "device-token")
	t.Setenv("HOME_DEVICE_ID", "dev_1")
	t.Setenv("HOME_LOCAL_ACCOUNT_ID", "alice")
	t.Setenv("HOME_SMOKE_TIMEOUT", "soon")

	_, err := LoadRemoteConfig()
	if err == nil {
		t.Fatal("LoadRemoteConfig() error = nil, want invalid timeout")
	}
	if !strings.Contains(err.Error(), "HOME_SMOKE_TIMEOUT") {
		t.Fatalf("LoadRemoteConfig() error = %v, want HOME_SMOKE_TIMEOUT", err)
	}
}

func TestRemoteWebSocketURLDerivation(t *testing.T) {
	tests := []struct {
		base string
		want string
	}{
		{base: "https://home.kittypaw.app", want: "wss://home.kittypaw.app/daemon/connect"},
		{base: "http://127.0.0.1:8080", want: "ws://127.0.0.1:8080/daemon/connect"},
		{base: "https://home.kittypaw.app/base/", want: "wss://home.kittypaw.app/base/daemon/connect"},
	}
	for _, tt := range tests {
		got, err := remoteWebSocketURL(tt.base)
		if err != nil {
			t.Fatalf("remoteWebSocketURL(%q) error = %v", tt.base, err)
		}
		if got != tt.want {
			t.Fatalf("remoteWebSocketURL(%q) = %q, want %q", tt.base, got, tt.want)
		}
	}
}

func TestRemoteWebSocketURLRejectsUnsupportedScheme(t *testing.T) {
	_, err := remoteWebSocketURL("ftp://home.kittypaw.app")
	if err == nil {
		t.Fatal("remoteWebSocketURL() error = nil, want unsupported scheme")
	}
}

func TestRunRemoteCompletesCredentialedRoundTrip(t *testing.T) {
	t.Skip("implemented after remote runner exists")
	_ = context.Background()
}
