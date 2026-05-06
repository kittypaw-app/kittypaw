package main

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jinto/kittypaw/core"
)

func TestRootCommandRegistersConnectGmail(t *testing.T) {
	root := newRootCmd()
	cmd, _, err := root.Find([]string{"connect", "gmail"})
	if err != nil || cmd == nil || cmd.Name() != "gmail" {
		t.Fatalf("Find(connect gmail) = (%v, %v), want gmail command", cmd, err)
	}
}

func TestRootCommandRegistersConnectX(t *testing.T) {
	root := newRootCmd()
	cmd, _, err := root.Find([]string{"connect", "x"})
	if err != nil || cmd == nil || cmd.Name() != "x" {
		t.Fatalf("Find(connect x) = (%v, %v), want x command", cmd, err)
	}
}

func TestConnectGmailUsesSelectedAccount(t *testing.T) {
	rootDir := t.TempDir()
	t.Setenv("KITTYPAW_CONFIG_DIR", rootDir)
	mustWriteTestConfig(t, filepath.Join(rootDir, "accounts", "alice", "config.toml"))
	mustWriteTestConfig(t, filepath.Join(rootDir, "accounts", "bob", "config.toml"))

	oldRunner := connectGmailRunner
	t.Cleanup(func() { connectGmailRunner = oldRunner })
	var gotAccount string
	connectGmailRunner = func(apiURL, accountID string, useCode bool) error {
		gotAccount = accountID
		return nil
	}

	root := newRootCmd()
	root.SetArgs([]string{"connect", "gmail", "--account", "bob", "--code", "--api-url", "https://portal.example"})
	if err := root.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if gotAccount != "bob" {
		t.Fatalf("connect gmail account = %q, want bob", gotAccount)
	}
}

func TestConnectXUsesSelectedAccount(t *testing.T) {
	rootDir := t.TempDir()
	t.Setenv("KITTYPAW_CONFIG_DIR", rootDir)
	mustWriteTestConfig(t, filepath.Join(rootDir, "accounts", "alice", "config.toml"))
	mustWriteTestConfig(t, filepath.Join(rootDir, "accounts", "bob", "config.toml"))

	oldRunner := connectXRunner
	t.Cleanup(func() { connectXRunner = oldRunner })
	var gotAccount string
	connectXRunner = func(apiURL, accountID string, useCode bool) error {
		gotAccount = accountID
		return nil
	}

	root := newRootCmd()
	root.SetArgs([]string{"connect", "x", "--account", "bob", "--code", "--api-url", "https://portal.example"})
	if err := root.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if gotAccount != "bob" {
		t.Fatalf("connect x account = %q, want bob", gotAccount)
	}
}

func TestConnectGmailLoginURLUsesDiscoveredConnectBaseURL(t *testing.T) {
	secrets := testSecretsStore(t)
	mgr := core.NewAPITokenManager("", secrets)
	apiURL := "https://portal.kittypaw.app"
	if err := mgr.SaveConnectBaseURL(apiURL, "https://connect.kittypaw.app"); err != nil {
		t.Fatal(err)
	}

	got := connectGmailLoginURL(apiURL, mgr, "http", 12345)
	want := "https://connect.kittypaw.app/connect/gmail/login?mode=http&port=12345"
	if got != want {
		t.Fatalf("connectGmailLoginURL = %q, want %q", got, want)
	}
}

func TestConnectXLoginURLUsesDiscoveredConnectBaseURL(t *testing.T) {
	secrets := testSecretsStore(t)
	mgr := core.NewAPITokenManager("", secrets)
	apiURL := "https://portal.kittypaw.app"
	if err := mgr.SaveConnectBaseURL(apiURL, "https://connect.kittypaw.app"); err != nil {
		t.Fatal(err)
	}

	got := connectXLoginURL(apiURL, mgr, "http", 12345)
	want := "https://connect.kittypaw.app/connect/x/login?mode=http&port=12345"
	if got != want {
		t.Fatalf("connectXLoginURL = %q, want %q", got, want)
	}
}

func TestConnectCallbackRejectsTokenQueryParams(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/callback?access_token=AT&refresh_token=RT", nil)
	_, err := connectCallbackCode(req)
	if err == nil {
		t.Fatal("connectCallbackCode accepted token query params")
	}
	if !strings.Contains(err.Error(), "one-time code") {
		t.Fatalf("error = %v", err)
	}
}

func TestConnectExchangeStoresXTokens(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/connect/cli/exchange" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"provider":"x","access_token":"x-access-1","refresh_token":"x-refresh-1","token_type":"bearer","expires_in":7200,"scope":"tweet.read users.read offline.access","username":"jaypark"}`)
	}))
	defer ts.Close()

	secrets := testSecretsStore(t)
	serviceMgr := core.NewServiceTokenManager(secrets)

	if err := exchangeConnectCode(ts.URL, "code-1", serviceMgr); err != nil {
		t.Fatalf("exchangeConnectCode: %v", err)
	}
	ns := core.ServiceTokenNamespace("x")
	for key, want := range map[string]string{
		"access_token":     "x-access-1",
		"refresh_token":    "x-refresh-1",
		"connect_base_url": ts.URL,
		"username":         "jaypark",
	} {
		if got, ok := secrets.Get(ns, key); !ok || got != want {
			t.Fatalf("%s = (%q, %v), want %q", key, got, ok, want)
		}
	}
}

func TestConnectExchangeStoresGmailTokens(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/connect/cli/exchange" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"provider":"gmail","access_token":"access-1","refresh_token":"refresh-1","token_type":"Bearer","expires_in":3600,"scope":"gmail.readonly","email":"alice@example.com"}`)
	}))
	defer ts.Close()

	secrets := testSecretsStore(t)
	serviceMgr := core.NewServiceTokenManager(secrets)

	if err := exchangeConnectCode(ts.URL, "code-1", serviceMgr); err != nil {
		t.Fatalf("exchangeConnectCode: %v", err)
	}
	ns := core.ServiceTokenNamespace("gmail")
	for key, want := range map[string]string{
		"access_token":     "access-1",
		"refresh_token":    "refresh-1",
		"connect_base_url": ts.URL,
		"email":            "alice@example.com",
	} {
		if got, ok := secrets.Get(ns, key); !ok || got != want {
			t.Fatalf("%s = (%q, %v), want %q", key, got, ok, want)
		}
	}
}
