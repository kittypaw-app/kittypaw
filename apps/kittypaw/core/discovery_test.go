package core

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestFetchDiscovery_HappyPath(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/discovery" {
			t.Errorf("expected /discovery path, got %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{
  "api_base_url": "https://api.kittypaw.app",
  "auth_base_url": "https://portal.kittypaw.app/auth",
  "connect_base_url": "https://connect.kittypaw.app",
  "home_base_url": "https://home.kittypaw.app",
  "chat_relay_url": "https://chat.kittypaw.app",
  "kakao_relay_url": "https://kakao.kittypaw.app",
  "skills_registry_url": "https://github.com/kittypaw-app/skills"
}`)
	}))
	defer ts.Close()

	got, err := FetchDiscovery(ts.URL)
	if err != nil {
		t.Fatalf("FetchDiscovery: %v", err)
	}
	if got.APIBaseURL != "https://api.kittypaw.app" {
		t.Errorf("APIBaseURL = %q", got.APIBaseURL)
	}
	if got.AuthBaseURL != "https://portal.kittypaw.app/auth" {
		t.Errorf("AuthBaseURL = %q", got.AuthBaseURL)
	}
	if got.ConnectBaseURL != "https://connect.kittypaw.app" {
		t.Errorf("ConnectBaseURL = %q", got.ConnectBaseURL)
	}
	if got.HomeBaseURL != "https://home.kittypaw.app" {
		t.Errorf("HomeBaseURL = %q", got.HomeBaseURL)
	}
	if got.ChatRelayURL != "https://chat.kittypaw.app" {
		t.Errorf("ChatRelayURL = %q", got.ChatRelayURL)
	}
	if got.KakaoRelayURL != "https://kakao.kittypaw.app" {
		t.Errorf("KakaoRelayURL = %q", got.KakaoRelayURL)
	}
	if got.SkillsRegistryURL != "https://github.com/kittypaw-app/skills" {
		t.Errorf("SkillsRegistryURL = %q", got.SkillsRegistryURL)
	}
}

func TestFetchDiscovery_TrailingSlashTrimmed(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{
  "api_base_url": "https://api.kittypaw.app/",
  "auth_base_url": "https://portal.kittypaw.app/auth///",
  "connect_base_url": "https://connect.kittypaw.app///",
  "home_base_url": "https://home.kittypaw.app///",
  "chat_relay_url": "https://chat.kittypaw.app///",
  "kakao_relay_url": "https://kakao.kittypaw.app///",
  "skills_registry_url": "https://github.com/kittypaw-app/skills/"
}`)
	}))
	defer ts.Close()

	got, err := FetchDiscovery(ts.URL)
	if err != nil {
		t.Fatalf("FetchDiscovery: %v", err)
	}
	if got.APIBaseURL != "https://api.kittypaw.app" {
		t.Errorf("APIBaseURL trailing slash not trimmed: %q", got.APIBaseURL)
	}
	if got.AuthBaseURL != "https://portal.kittypaw.app/auth" {
		t.Errorf("AuthBaseURL trailing slash not trimmed: %q", got.AuthBaseURL)
	}
	if got.ConnectBaseURL != "https://connect.kittypaw.app" {
		t.Errorf("ConnectBaseURL trailing slashes not trimmed: %q", got.ConnectBaseURL)
	}
	if got.HomeBaseURL != "https://home.kittypaw.app" {
		t.Errorf("HomeBaseURL trailing slashes not trimmed: %q", got.HomeBaseURL)
	}
	if got.ChatRelayURL != "https://chat.kittypaw.app" {
		t.Errorf("ChatRelayURL trailing slashes not trimmed: %q", got.ChatRelayURL)
	}
	if got.KakaoRelayURL != "https://kakao.kittypaw.app" {
		t.Errorf("KakaoRelayURL trailing slashes not trimmed: %q", got.KakaoRelayURL)
	}
	if got.SkillsRegistryURL != "https://github.com/kittypaw-app/skills" {
		t.Errorf("SkillsRegistryURL trailing slash not trimmed: %q", got.SkillsRegistryURL)
	}
}

func TestFetchDiscovery_TrimsTrailingSlashOnBase(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/discovery" {
			t.Errorf("expected /discovery, got %s (double-slash concat?)", r.URL.Path)
		}
		fmt.Fprint(w, `{"api_base_url":"https://a.x"}`)
	}))
	defer ts.Close()

	// Pass base with trailing slash — implementation must trim before appending /discovery.
	_, err := FetchDiscovery(ts.URL + "/")
	if err != nil {
		t.Fatalf("FetchDiscovery with trailing slash base: %v", err)
	}
}

func TestFetchDiscovery_EmptyRelayOK(t *testing.T) {
	// Empty chat_relay_url/kakao_relay_url/skills_registry_url are valid; only api_base_url is required.
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"api_base_url":"https://api.x","chat_relay_url":"","kakao_relay_url":"","skills_registry_url":""}`)
	}))
	defer ts.Close()

	got, err := FetchDiscovery(ts.URL)
	if err != nil {
		t.Fatalf("FetchDiscovery: %v", err)
	}
	if got.ChatRelayURL != "" || got.KakaoRelayURL != "" || got.SkillsRegistryURL != "" {
		t.Errorf("expected empty chat/relay/skills, got %+v", got)
	}
}

func TestFetchDiscovery_MissingConnectBaseURLOK(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"api_base_url":"https://api.x"}`)
	}))
	defer ts.Close()

	got, err := FetchDiscovery(ts.URL)
	if err != nil {
		t.Fatalf("FetchDiscovery: %v", err)
	}
	if got.ConnectBaseURL != "" {
		t.Fatalf("ConnectBaseURL = %q, want empty for old portals", got.ConnectBaseURL)
	}
}

func TestFetchDiscovery_MissingAPIBaseURL(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"kakao_relay_url":"https://kakao.x"}`)
	}))
	defer ts.Close()

	_, err := FetchDiscovery(ts.URL)
	if err == nil {
		t.Fatal("expected error for missing api_base_url, got nil")
	}
	if !strings.Contains(err.Error(), "api_base_url") {
		t.Errorf("error should mention api_base_url, got %v", err)
	}
}

func TestFetchDiscovery_HTTP500(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	defer ts.Close()

	_, err := FetchDiscovery(ts.URL)
	if err == nil {
		t.Fatal("expected error for 500, got nil")
	}
}

func TestFetchDiscovery_NonJSONBody(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `<html>not json</html>`)
	}))
	defer ts.Close()

	_, err := FetchDiscovery(ts.URL)
	if err == nil {
		t.Fatal("expected error for non-JSON body, got nil")
	}
}

func TestFetchDiscovery_HostDown(t *testing.T) {
	// Point at a closed port — connection refused.
	_, err := FetchDiscovery("http://127.0.0.1:1") // port 1 is never a real service
	if err == nil {
		t.Fatal("expected error for unreachable host, got nil")
	}
}
