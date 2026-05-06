package main

import (
	"testing"

	"github.com/kittypaw-app/kittyhome/internal/config"
)

func TestBuildValuePrefersRealBuildValue(t *testing.T) {
	if got := buildValue("configured", "v1.2.3"); got != "v1.2.3" {
		t.Fatalf("buildValue = %q, want v1.2.3", got)
	}
}

func TestBuildValueFallsBackToConfiguredForDevBuild(t *testing.T) {
	if got := buildValue("configured", "dev"); got != "configured" {
		t.Fatalf("buildValue = %q, want configured", got)
	}
}

func TestUnixSocketPath(t *testing.T) {
	tests := []struct {
		in   string
		path string
		ok   bool
	}{
		{in: "unix:/run/kittyhome.sock", path: "/run/kittyhome.sock", ok: true},
		{in: "/tmp/kittyhome.sock", path: "/tmp/kittyhome.sock", ok: true},
		{in: ":8080", path: "", ok: false},
	}
	for _, tt := range tests {
		path, ok := unixSocketPath(tt.in)
		if path != tt.path || ok != tt.ok {
			t.Fatalf("unixSocketPath(%q) = (%q, %v), want (%q, %v)", tt.in, path, ok, tt.path, tt.ok)
		}
	}
}

func TestNewRouterRequiresVerifierConfig(t *testing.T) {
	_, err := newRouter(config.Config{BindAddr: ":0"})
	if err == nil {
		t.Fatal("newRouter returned nil error without verifier config")
	}
}

func TestNewRouterAcceptsStaticSmokeConfig(t *testing.T) {
	_, err := newRouter(config.Config{
		BindAddr:       ":0",
		APIToken:       "api-token",
		DeviceToken:    "device-token",
		UserID:         "user_1",
		DeviceID:       "dev_1",
		LocalAccountID: "alice",
		PublicBaseURL:  "http://localhost:8080",
		APIAuthBaseURL: "http://localhost:9714/auth",
	})
	if err != nil {
		t.Fatalf("newRouter returned error: %v", err)
	}
}
