package config_test

import (
	"reflect"
	"strings"
	"testing"

	"github.com/kittypaw-app/kittyapi/internal/config"
)

func TestLoadRequiresDatabaseURL(t *testing.T) {
	t.Setenv("DATABASE_URL", "")

	_, err := config.Load()
	if err == nil {
		t.Fatal("expected DATABASE_URL error")
	}
	if !strings.Contains(err.Error(), "DATABASE_URL") {
		t.Fatalf("error = %v, want DATABASE_URL mention", err)
	}
}

func TestLoadDoesNotRequirePortalAuthSecrets(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://localhost/x")

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.DatabaseURL != "postgres://localhost/x" {
		t.Fatalf("DatabaseURL = %q", cfg.DatabaseURL)
	}
}

func TestLoadUsesUnixSocketEnv(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://localhost/x")
	t.Setenv("UNIX_SOCKET", "/tmp/kittyapi.sock")

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.UnixSocket != "/tmp/kittyapi.sock" {
		t.Fatalf("UnixSocket = %q, want /tmp/kittyapi.sock", cfg.UnixSocket)
	}
}

func TestLoadCORSOriginsCSV(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://localhost/x")
	t.Setenv("CORS_ORIGINS", " https://portal.kittypaw.app,https://chat.kittypaw.app ")

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	want := []string{"https://portal.kittypaw.app", "https://chat.kittypaw.app"}
	if !reflect.DeepEqual(cfg.AllowedOrigins, want) {
		t.Fatalf("AllowedOrigins = %#v, want %#v", cfg.AllowedOrigins, want)
	}
}
