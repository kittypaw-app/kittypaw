package config

import "testing"

func TestLoadDefaultsToSpaceURLs(t *testing.T) {
	t.Setenv("KITTYSPACE_JWKS_URL", "https://portal.kittypaw.app/.well-known/jwks.json")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}
	if cfg.PublicBaseURL != "https://space.kittypaw.app" {
		t.Fatalf("PublicBaseURL = %q, want https://space.kittypaw.app", cfg.PublicBaseURL)
	}
	if cfg.APIAuthBaseURL != "https://portal.kittypaw.app/auth" {
		t.Fatalf("APIAuthBaseURL = %q, want portal auth base", cfg.APIAuthBaseURL)
	}
	if cfg.BindAddr != ":8080" {
		t.Fatalf("BindAddr = %q, want :8080", cfg.BindAddr)
	}
	if cfg.KittyPawStableFile != "/home/jinto/kittyspace/public/kittypaw/stable.json" {
		t.Fatalf("KittyPawStableFile = %q, want hosted stable metadata file", cfg.KittyPawStableFile)
	}
}

func TestLoadAcceptsStaticFallbackForLocalSmoke(t *testing.T) {
	t.Setenv("KITTYSPACE_API_TOKEN", "api-token")
	t.Setenv("KITTYSPACE_DEVICE_TOKEN", "device-token")
	t.Setenv("KITTYSPACE_USER_ID", "user_1")
	t.Setenv("KITTYSPACE_DEVICE_ID", "dev_1")
	t.Setenv("KITTYSPACE_LOCAL_ACCOUNT_ID", "alice")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}
	if cfg.APIToken != "api-token" || cfg.DeviceToken != "device-token" {
		t.Fatalf("static tokens not loaded: %#v", cfg)
	}
}

func TestLoadRequiresVerifierOrStaticTokens(t *testing.T) {
	_, err := Load()
	if err == nil {
		t.Fatal("Load returned nil error without verifier config")
	}
}

func TestLoadRejectsShortJWTSecret(t *testing.T) {
	t.Setenv("KITTYSPACE_JWT_SECRET", "short")

	_, err := Load()
	if err == nil {
		t.Fatal("Load returned nil error for short JWT secret")
	}
}
