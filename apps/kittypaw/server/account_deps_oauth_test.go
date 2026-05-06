package server

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jinto/kittypaw/core"
)

func TestResolveMCPEnvSourceLoadsGmailAccessToken(t *testing.T) {
	secrets, err := core.LoadSecretsFrom(filepath.Join(t.TempDir(), "secrets.json"))
	if err != nil {
		t.Fatal(err)
	}
	tokens := core.NewServiceTokenManager(secrets)
	if err := tokens.Save("gmail", core.ServiceTokenSet{
		AccessToken: "gmail-token",
		ExpiresAt:   time.Now().Add(time.Hour),
	}); err != nil {
		t.Fatal(err)
	}

	got, err := resolveMCPEnvSource(tokens, "oauth-gmail/access_token")
	if err != nil {
		t.Fatalf("resolveMCPEnvSource: %v", err)
	}
	if got != "gmail-token" {
		t.Fatalf("token = %q", got)
	}
}

func TestResolveMCPEnvSourceLoadsXAccessToken(t *testing.T) {
	secrets, err := core.LoadSecretsFrom(filepath.Join(t.TempDir(), "secrets.json"))
	if err != nil {
		t.Fatal(err)
	}
	tokens := core.NewServiceTokenManager(secrets)
	if err := tokens.Save("x", core.ServiceTokenSet{
		AccessToken: "x-token",
		ExpiresAt:   time.Now().Add(time.Hour),
	}); err != nil {
		t.Fatal(err)
	}

	got, err := resolveMCPEnvSource(tokens, "oauth-x/access_token")
	if err != nil {
		t.Fatalf("resolveMCPEnvSource: %v", err)
	}
	if got != "x-token" {
		t.Fatalf("token = %q", got)
	}
}

func TestResolveMCPEnvSourceMissingGmailTokenGuidesReconnect(t *testing.T) {
	secrets, err := core.LoadSecretsFrom(filepath.Join(t.TempDir(), "secrets.json"))
	if err != nil {
		t.Fatal(err)
	}
	tokens := core.NewServiceTokenManager(secrets)

	_, err = resolveMCPEnvSource(tokens, "oauth-gmail/access_token")
	if err == nil {
		t.Fatal("expected missing token error")
	}
	if !strings.Contains(err.Error(), "kittypaw connect gmail") {
		t.Fatalf("expected reconnect guidance, got: %v", err)
	}
}

func TestResolveMCPEnvSourceMissingXTokenGuidesReconnect(t *testing.T) {
	secrets, err := core.LoadSecretsFrom(filepath.Join(t.TempDir(), "secrets.json"))
	if err != nil {
		t.Fatal(err)
	}
	tokens := core.NewServiceTokenManager(secrets)

	_, err = resolveMCPEnvSource(tokens, "oauth-x/access_token")
	if err == nil {
		t.Fatal("expected missing token error")
	}
	if !strings.Contains(err.Error(), "kittypaw connect x") {
		t.Fatalf("expected reconnect guidance, got: %v", err)
	}
}

func TestResolveMCPEnvSourceRejectsUnknownSource(t *testing.T) {
	secrets, err := core.LoadSecretsFrom(filepath.Join(t.TempDir(), "secrets.json"))
	if err != nil {
		t.Fatal(err)
	}
	tokens := core.NewServiceTokenManager(secrets)

	_, err = resolveMCPEnvSource(tokens, "oauth-slack/access_token")
	if err == nil {
		t.Fatal("expected unsupported source error")
	}
	if !strings.Contains(err.Error(), "unsupported MCP env source") {
		t.Fatalf("unexpected error: %v", err)
	}
}
