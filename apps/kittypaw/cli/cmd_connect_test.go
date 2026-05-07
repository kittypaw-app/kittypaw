package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
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

func TestConnectXCreatesPreauthSessionWithPortalToken(t *testing.T) {
	secrets := testSecretsStore(t)
	apiMgr := core.NewAPITokenManager("", secrets)
	serviceMgr := core.NewServiceTokenManager(secrets)

	var gotAuth string
	var gotMode string
	var gotPort string
	var ts *httptest.Server
	ts = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/discovery":
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprintf(w, `{"api_base_url":%q,"connect_base_url":%q}`, ts.URL, ts.URL)
		case "/connect/x/sessions":
			gotAuth = r.Header.Get("Authorization")
			var payload struct {
				Mode string `json:"mode"`
				Port string `json:"port"`
			}
			if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
				t.Errorf("decode session payload: %v", err)
			}
			gotMode = payload.Mode
			gotPort = payload.Port
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprintf(w, `{"login_url":%q}`, ts.URL+"/connect/x/login?session=s-1")
		case "/connect/cli/exchange":
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `{"provider":"x","token_type":"broker","scope":"tweet.read users.read offline.access","username":"jaypark"}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer ts.Close()

	accessToken := makeSetupTestJWT(t)
	if err := apiMgr.SaveTokens(ts.URL, accessToken, "refresh-token"); err != nil {
		t.Fatalf("SaveTokens: %v", err)
	}

	loginURL, err := createConnectSession("x", ts.URL, apiMgr, "code", 0)
	if err != nil {
		t.Fatalf("createConnectSession: %v", err)
	}
	if !strings.Contains(loginURL, "session=s-1") {
		t.Fatalf("login URL = %q, want session=s-1", loginURL)
	}
	if gotAuth != "Bearer "+accessToken {
		t.Fatalf("Authorization = %q, want Bearer token", gotAuth)
	}
	if gotMode != "code" {
		t.Fatalf("mode = %q, want code", gotMode)
	}
	if gotPort != "" {
		t.Fatalf("port = %q, want empty in code mode", gotPort)
	}
	if err := exchangeConnectCode(ts.URL, "code-1", serviceMgr); err != nil {
		t.Fatalf("exchangeConnectCode: %v", err)
	}
	if got, ok := secrets.Get(core.ServiceTokenNamespace("x"), "token_type"); !ok || got != "broker" {
		t.Fatalf("stored x token_type = (%q, %v), want broker", got, ok)
	}
	if got, ok := secrets.Get(core.ServiceTokenNamespace("x"), "access_token"); ok || got != "" {
		t.Fatalf("stored x access_token = (%q, %v), want absent", got, ok)
	}
	if got, ok := secrets.Get(core.ServiceTokenNamespace("x"), "refresh_token"); ok || got != "" {
		t.Fatalf("stored x refresh_token = (%q, %v), want absent", got, ok)
	}
}

func TestConnectXCodeFlowCreatesPreauthSessionAndExchangesOnConnectBaseURL(t *testing.T) {
	secrets := testSecretsStore(t)
	apiMgr := core.NewAPITokenManager("", secrets)
	serviceMgr := core.NewServiceTokenManager(secrets)

	accessToken := makeSetupTestJWT(t)
	var gotAuth string
	var gotMode string
	var gotExchangeCode string
	var exchangeHits int
	var connectTS *httptest.Server
	connectTS = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/connect/x/sessions":
			gotAuth = r.Header.Get("Authorization")
			var payload struct {
				Mode string `json:"mode"`
			}
			if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
				t.Errorf("decode session payload: %v", err)
			}
			gotMode = payload.Mode
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprintf(w, `{"login_url":%q}`, connectTS.URL+"/connect/x/login?session=s-runtime")
		case "/connect/cli/exchange":
			exchangeHits++
			var payload struct {
				Code string `json:"code"`
			}
			if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
				t.Errorf("decode exchange payload: %v", err)
			}
			gotExchangeCode = payload.Code
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `{"provider":"x","token_type":"broker","scope":"tweet.read users.read offline.access","username":"jaypark"}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer connectTS.Close()

	var apiExchangeHits int
	apiTS := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/discovery":
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprintf(w, `{"api_base_url":%q,"connect_base_url":%q}`, apiTSURL(r), connectTS.URL)
		case "/connect/cli/exchange":
			apiExchangeHits++
			http.Error(w, "exchange should use connect_base_url", http.StatusInternalServerError)
		default:
			http.NotFound(w, r)
		}
	}))
	defer apiTS.Close()

	if err := apiMgr.SaveTokens(apiTS.URL, accessToken, "refresh-token"); err != nil {
		t.Fatalf("SaveTokens: %v", err)
	}

	var err error
	out := withConnectStdin(t, "code-1\n", func() string {
		return captureStdout(t, func() {
			err = connectServiceCode("x", apiTS.URL, apiMgr, serviceMgr)
		})
	})
	if err != nil {
		t.Fatalf("connectServiceCode: %v", err)
	}
	if !strings.Contains(out, "session=s-runtime") {
		t.Fatalf("stdout = %q, want preauth session login URL", out)
	}
	if strings.Contains(out, "/connect/x/login?mode=code") {
		t.Fatalf("stdout = %q, must not use direct X login URL", out)
	}
	if gotAuth != "Bearer "+accessToken {
		t.Fatalf("Authorization = %q, want Bearer token", gotAuth)
	}
	if gotMode != "code" {
		t.Fatalf("session mode = %q, want code", gotMode)
	}
	if exchangeHits != 1 || apiExchangeHits != 0 {
		t.Fatalf("exchange hits connect=%d api=%d, want connect=1 api=0", exchangeHits, apiExchangeHits)
	}
	if gotExchangeCode != "code-1" {
		t.Fatalf("exchange code = %q, want code-1", gotExchangeCode)
	}
	if got, ok := secrets.Get(core.ServiceTokenNamespace("x"), "connect_base_url"); !ok || got != connectTS.URL {
		t.Fatalf("stored x connect_base_url = (%q, %v), want %q", got, ok, connectTS.URL)
	}
	if got, ok := secrets.Get(core.ServiceTokenNamespace("x"), "access_token"); ok || got != "" {
		t.Fatalf("stored x access_token = (%q, %v), want absent", got, ok)
	}
	if got, ok := secrets.Get(core.ServiceTokenNamespace("x"), "refresh_token"); ok || got != "" {
		t.Fatalf("stored x refresh_token = (%q, %v), want absent", got, ok)
	}
}

func TestConnectGmailCodeFlowDoesNotRequirePortalLoginToken(t *testing.T) {
	secrets := testSecretsStore(t)
	apiMgr := core.NewAPITokenManager("", secrets)
	serviceMgr := core.NewServiceTokenManager(secrets)

	var sessionHits int
	var exchangeHits int
	var connectTS *httptest.Server
	connectTS = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/connect/gmail/sessions":
			sessionHits++
			http.Error(w, "gmail must not create preauth sessions", http.StatusInternalServerError)
		case "/connect/cli/exchange":
			exchangeHits++
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `{"provider":"gmail","access_token":"access-1","refresh_token":"refresh-1","token_type":"Bearer","expires_in":3600,"scope":"gmail.readonly","email":"alice@example.com"}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer connectTS.Close()

	apiTS := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/discovery" {
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprintf(w, `{"api_base_url":%q,"connect_base_url":%q}`, apiTSURL(r), connectTS.URL)
			return
		}
		http.NotFound(w, r)
	}))
	defer apiTS.Close()

	var err error
	out := withConnectStdin(t, "code-1\n", func() string {
		return captureStdout(t, func() {
			err = connectServiceCode("gmail", apiTS.URL, apiMgr, serviceMgr)
		})
	})
	if err != nil {
		t.Fatalf("connectServiceCode: %v", err)
	}
	if !strings.Contains(out, "/connect/gmail/login?mode=code") {
		t.Fatalf("stdout = %q, want direct Gmail login URL", out)
	}
	if sessionHits != 0 {
		t.Fatalf("gmail session hits = %d, want 0", sessionHits)
	}
	if exchangeHits != 1 {
		t.Fatalf("gmail exchange hits = %d, want 1", exchangeHits)
	}
}

func TestConnectXPreauthSessionHTTPModeSendsPort(t *testing.T) {
	secrets := testSecretsStore(t)
	apiMgr := core.NewAPITokenManager("", secrets)

	var gotMode string
	var gotPort string
	var ts *httptest.Server
	ts = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/discovery":
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprintf(w, `{"api_base_url":%q,"connect_base_url":%q}`, ts.URL, ts.URL)
		case "/connect/x/sessions":
			var payload struct {
				Mode string `json:"mode"`
				Port string `json:"port"`
			}
			if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
				t.Errorf("decode session payload: %v", err)
			}
			gotMode = payload.Mode
			gotPort = payload.Port
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprintf(w, `{"login_url":%q}`, ts.URL+"/connect/x/login?session=s-1")
		default:
			http.NotFound(w, r)
		}
	}))
	defer ts.Close()

	if err := apiMgr.SaveTokens(ts.URL, makeSetupTestJWT(t), "refresh-token"); err != nil {
		t.Fatalf("SaveTokens: %v", err)
	}
	if _, err := createConnectSession("x", ts.URL, apiMgr, "http", 49152); err != nil {
		t.Fatalf("createConnectSession: %v", err)
	}
	if gotMode != "http" {
		t.Fatalf("mode = %q, want http", gotMode)
	}
	if gotPort != "49152" {
		t.Fatalf("port = %q, want 49152", gotPort)
	}
}

func TestConnectXHTTPFlowCreatesPreauthSessionAndExchangesOnConnectBaseURL(t *testing.T) {
	secrets := testSecretsStore(t)
	apiMgr := core.NewAPITokenManager("", secrets)
	serviceMgr := core.NewServiceTokenManager(secrets)

	accessToken := makeSetupTestJWT(t)
	var gotAuth string
	var gotMode string
	var gotPort string
	var gotExchangeCode string
	var openedURL string
	var exchangeHits int
	var connectTS *httptest.Server
	connectTS = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/connect/x/sessions":
			gotAuth = r.Header.Get("Authorization")
			var payload struct {
				Mode string `json:"mode"`
				Port string `json:"port"`
			}
			if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
				t.Errorf("decode session payload: %v", err)
			}
			gotMode = payload.Mode
			gotPort = payload.Port
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprintf(w, `{"login_url":%q}`, connectTS.URL+"/connect/x/login?session=s-http")
		case "/connect/cli/exchange":
			exchangeHits++
			var payload struct {
				Code string `json:"code"`
			}
			if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
				t.Errorf("decode exchange payload: %v", err)
			}
			gotExchangeCode = payload.Code
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `{"provider":"x","token_type":"broker","scope":"tweet.read users.read offline.access","username":"jaypark"}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer connectTS.Close()

	var apiExchangeHits int
	apiTS := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/discovery":
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprintf(w, `{"api_base_url":%q,"connect_base_url":%q}`, apiTSURL(r), connectTS.URL)
		case "/connect/cli/exchange":
			apiExchangeHits++
			http.Error(w, "exchange should use connect_base_url", http.StatusInternalServerError)
		default:
			http.NotFound(w, r)
		}
	}))
	defer apiTS.Close()

	if err := apiMgr.SaveTokens(apiTS.URL, accessToken, "refresh-token"); err != nil {
		t.Fatalf("SaveTokens: %v", err)
	}

	oldOpenBrowser := connectOpenBrowser
	t.Cleanup(func() { connectOpenBrowser = oldOpenBrowser })
	connectOpenBrowser = func(url string) error {
		openedURL = url
		go func(port string) {
			_, _ = http.Get("http://127.0.0.1:" + port + "/callback?code=code-http")
		}(gotPort)
		return nil
	}

	if err := connectServiceHTTP("x", "X", apiTS.URL, apiMgr, serviceMgr); err != nil {
		t.Fatalf("connectServiceHTTP: %v", err)
	}
	if !strings.Contains(openedURL, "session=s-http") {
		t.Fatalf("opened URL = %q, want preauth session login URL", openedURL)
	}
	if strings.Contains(openedURL, "/connect/x/login?mode=http") {
		t.Fatalf("opened URL = %q, must not use direct X login URL", openedURL)
	}
	if gotAuth != "Bearer "+accessToken {
		t.Fatalf("Authorization = %q, want Bearer token", gotAuth)
	}
	if gotMode != "http" {
		t.Fatalf("session mode = %q, want http", gotMode)
	}
	if gotPort == "" {
		t.Fatal("session port is empty, want local callback port")
	}
	if exchangeHits != 1 || apiExchangeHits != 0 {
		t.Fatalf("exchange hits connect=%d api=%d, want connect=1 api=0", exchangeHits, apiExchangeHits)
	}
	if gotExchangeCode != "code-http" {
		t.Fatalf("exchange code = %q, want code-http", gotExchangeCode)
	}
	if got, ok := secrets.Get(core.ServiceTokenNamespace("x"), "connect_base_url"); !ok || got != connectTS.URL {
		t.Fatalf("stored x connect_base_url = (%q, %v), want %q", got, ok, connectTS.URL)
	}
	if got, ok := secrets.Get(core.ServiceTokenNamespace("x"), "access_token"); ok || got != "" {
		t.Fatalf("stored x access_token = (%q, %v), want absent", got, ok)
	}
	if got, ok := secrets.Get(core.ServiceTokenNamespace("x"), "refresh_token"); ok || got != "" {
		t.Fatalf("stored x refresh_token = (%q, %v), want absent", got, ok)
	}
}

func TestConnectXPreauthSessionResponseErrors(t *testing.T) {
	tests := []struct {
		name       string
		statusCode int
		body       string
		want       string
	}{
		{
			name:       "non-200",
			statusCode: http.StatusForbidden,
			body:       "denied\n",
			want:       "session failed (403): denied",
		},
		{
			name:       "invalid JSON",
			statusCode: http.StatusOK,
			body:       "{",
			want:       "decode session response",
		},
		{
			name:       "missing login URL",
			statusCode: http.StatusOK,
			body:       `{}`,
			want:       "session response missing login_url",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			secrets := testSecretsStore(t)
			apiMgr := core.NewAPITokenManager("", secrets)
			var ts *httptest.Server
			ts = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/discovery":
					w.Header().Set("Content-Type", "application/json")
					fmt.Fprintf(w, `{"api_base_url":%q,"connect_base_url":%q}`, ts.URL, ts.URL)
				case "/connect/x/sessions":
					w.WriteHeader(tt.statusCode)
					_, _ = fmt.Fprint(w, tt.body)
				default:
					http.NotFound(w, r)
				}
			}))
			defer ts.Close()

			if err := apiMgr.SaveTokens(ts.URL, makeSetupTestJWT(t), "refresh-token"); err != nil {
				t.Fatalf("SaveTokens: %v", err)
			}
			_, err := createConnectSession("x", ts.URL, apiMgr, "code", 0)
			if err == nil {
				t.Fatal("createConnectSession succeeded, want error")
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("error = %v, want containing %q", err, tt.want)
			}
		})
	}
}

func TestConnectXPreauthSessionRequiresPortalLogin(t *testing.T) {
	secrets := testSecretsStore(t)
	apiMgr := core.NewAPITokenManager("", secrets)

	var ts *httptest.Server
	ts = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/discovery" {
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprintf(w, `{"api_base_url":%q,"connect_base_url":%q}`, ts.URL, ts.URL)
			return
		}
		http.NotFound(w, r)
	}))
	defer ts.Close()

	_, err := createConnectSession("x", ts.URL, apiMgr, "code", 0)
	if err == nil {
		t.Fatal("createConnectSession succeeded without portal login token")
	}
	if !strings.Contains(err.Error(), "kittypaw login --api-url") {
		t.Fatalf("error = %v, want login hint", err)
	}
}

func withConnectStdin(t *testing.T, input string, fn func() string) string {
	t.Helper()
	old := os.Stdin
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe stdin: %v", err)
	}
	if _, err := w.WriteString(input); err != nil {
		t.Fatalf("write stdin pipe: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("close stdin pipe writer: %v", err)
	}
	os.Stdin = r
	defer func() {
		os.Stdin = old
		_ = r.Close()
	}()
	return fn()
}

func apiTSURL(r *http.Request) string {
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	return scheme + "://" + r.Host
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
