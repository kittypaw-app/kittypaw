package main

import "testing"

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
