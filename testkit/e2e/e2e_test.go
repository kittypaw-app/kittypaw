package e2e_test

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/pgx/v5"
	_ "github.com/golang-migrate/migrate/v4/source/file"
	"github.com/jinto/kittypaw/core"
	"github.com/jinto/kittypaw/llm"
	"github.com/jinto/kittypaw/remote/chatrelay"
	"github.com/jinto/kittypaw/sandbox"
	kittypawserver "github.com/jinto/kittypaw/server"
	"github.com/jinto/kittypaw/store"
)

const localAccountID = "alice"

type tokenResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	TokenType    string `json:"token_type"`
	ExpiresIn    int    `json:"expires_in"`
}

type pairResponse struct {
	DeviceID           string `json:"device_id"`
	DeviceAccessToken  string `json:"device_access_token"`
	DeviceRefreshToken string `json:"device_refresh_token"`
	ExpiresIn          int    `json:"expires_in"`
}

func TestPortalSpaceBrowserSessionRelay(t *testing.T) {
	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		t.Skip("DATABASE_URL not set")
	}
	requireTestDatabase(t, dbURL)

	root := repoRoot(t)
	migratePortalDB(t, root, dbURL)

	ctx, cancel := context.WithTimeout(context.Background(), 75*time.Second)
	defer cancel()

	fakeGoogle := newFakeGoogle(t)
	defer fakeGoogle.Close()

	portalAddr := reserveAddr(t)
	spaceAddr := reserveAddr(t)
	portalURL := "http://" + portalAddr
	spaceURL := "http://" + spaceAddr
	spaceCallback := spaceURL + "/auth/callback"

	portal := startGoRun(ctx, t, "portal", filepath.Join(root, "apps/portal"), []string{"./cmd/server"}, map[string]string{
		"PORT":                       portOnly(portalAddr),
		"DATABASE_URL":               dbURL,
		"JWT_PRIVATE_KEY_PEM_B64":    generatePrivateKeyB64(t),
		"BASE_URL":                   portalURL,
		"API_BASE_URL":               portalURL,
		"SPACE_BASE_URL":             spaceURL,
		"CORS_ORIGINS":               spaceURL,
		"WEB_REDIRECT_URI_ALLOWLIST": spaceCallback,
		"GOOGLE_CLIENT_ID":           "local-e2e-client",
		"GOOGLE_CLIENT_SECRET":       "local-e2e-secret",
		"GOOGLE_AUTH_URL":            fakeGoogle.URL + "/o/oauth2/v2/auth",
		"GOOGLE_TOKEN_URL":           fakeGoogle.URL + "/token",
		"GOOGLE_USERINFO_URL":        fakeGoogle.URL + "/userinfo",
	})
	waitForHealth(ctx, t, portalURL+"/health", portal)

	space := startGoRun(ctx, t, "space", filepath.Join(root, "apps/space"), []string{"./cmd/kittyspace"}, map[string]string{
		"KITTYSPACE_BIND_ADDR":         spaceAddr,
		"KITTYSPACE_PUBLIC_BASE_URL":   spaceURL,
		"KITTYSPACE_API_AUTH_BASE_URL": portalURL + "/auth",
		"KITTYSPACE_JWKS_URL":          portalURL + "/.well-known/jwks.json",
		"KITTYSPACE_VERSION":           "local-e2e",
	})
	waitForHealth(ctx, t, spaceURL+"/health", space)

	apiClient := &http.Client{Timeout: 10 * time.Second}
	userTokens := portalGoogleLogin(t, apiClient, portalURL)
	device := pairDevice(t, apiClient, portalURL, userTokens.AccessToken)

	relayCtx, relayCancel := context.WithCancel(ctx)
	t.Cleanup(relayCancel)
	connector := &chatrelay.Connector{
		Config: chatrelay.ConnectorConfig{
			RelayURL:      spaceURL,
			Credential:    device.DeviceAccessToken,
			DeviceID:      device.DeviceID,
			LocalAccounts: []string{localAccountID},
			DaemonVersion: "local-e2e",
			Capabilities:  []string{chatrelay.OperationOpenAIChatCompletions, chatrelay.OperationOpenAIModels},
		},
		Dispatcher: e2eDispatcher{},
	}
	go connector.Run(relayCtx, chatrelay.RunOptions{
		RetryInitialDelay: 100 * time.Millisecond,
		RetryMaxDelay:     250 * time.Millisecond,
		Logf: func(format string, args ...any) {
			t.Logf("space relay: "+format, args...)
		},
	})

	browser := newBrowserClient(t)
	spaceBrowserLogin(t, browser, spaceURL)
	assertBrowserSession(t, browser, spaceURL)
	waitForBrowserRoute(ctx, t, browser, spaceURL, device.DeviceID)
	assertBrowserChatCompletion(t, browser, spaceURL, device.DeviceID)
}

func TestPortalSpaceRelayRunsKittypawSkillInstallFlow(t *testing.T) {
	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		t.Skip("DATABASE_URL not set")
	}
	requireTestDatabase(t, dbURL)

	root := repoRoot(t)
	migratePortalDB(t, root, dbURL)

	ctx, cancel := context.WithTimeout(context.Background(), 75*time.Second)
	defer cancel()

	fakeGoogle := newFakeGoogle(t)
	defer fakeGoogle.Close()

	registry := newFakeRegistry(t, exchangeRateRegistryPackage(), weatherNowRegistryPackage())
	defer registry.Close()
	kittypawAPI := newFakeKittypawAPI(t)
	defer kittypawAPI.Close()
	t.Setenv("KITTYPAW_ALLOW_INSECURE_REGISTRY", "1")

	portalAddr := reserveAddr(t)
	spaceAddr := reserveAddr(t)
	portalURL := "http://" + portalAddr
	spaceURL := "http://" + spaceAddr
	spaceCallback := spaceURL + "/auth/callback"

	portal := startGoRun(ctx, t, "portal", filepath.Join(root, "apps/portal"), []string{"./cmd/server"}, map[string]string{
		"PORT":                       portOnly(portalAddr),
		"DATABASE_URL":               dbURL,
		"JWT_PRIVATE_KEY_PEM_B64":    generatePrivateKeyB64(t),
		"BASE_URL":                   portalURL,
		"API_BASE_URL":               portalURL,
		"SPACE_BASE_URL":             spaceURL,
		"CORS_ORIGINS":               spaceURL,
		"WEB_REDIRECT_URI_ALLOWLIST": spaceCallback,
		"GOOGLE_CLIENT_ID":           "local-e2e-client",
		"GOOGLE_CLIENT_SECRET":       "local-e2e-secret",
		"GOOGLE_AUTH_URL":            fakeGoogle.URL + "/o/oauth2/v2/auth",
		"GOOGLE_TOKEN_URL":           fakeGoogle.URL + "/token",
		"GOOGLE_USERINFO_URL":        fakeGoogle.URL + "/userinfo",
	})
	waitForHealth(ctx, t, portalURL+"/health", portal)

	space := startGoRun(ctx, t, "space", filepath.Join(root, "apps/space"), []string{"./cmd/kittyspace"}, map[string]string{
		"KITTYSPACE_BIND_ADDR":         spaceAddr,
		"KITTYSPACE_PUBLIC_BASE_URL":   spaceURL,
		"KITTYSPACE_API_AUTH_BASE_URL": portalURL + "/auth",
		"KITTYSPACE_JWKS_URL":          portalURL + "/.well-known/jwks.json",
		"KITTYSPACE_VERSION":           "local-e2e",
	})
	waitForHealth(ctx, t, spaceURL+"/health", space)

	apiClient := &http.Client{Timeout: 10 * time.Second}
	userTokens := portalGoogleLogin(t, apiClient, portalURL)
	device := pairDevice(t, apiClient, portalURL, userTokens.AccessToken)
	kittypaw := newKittypawRelayServer(t, registry.URL,
		withKittypawAPIBaseURL(kittypawAPI.URL),
		withE2EWeatherSlots("강남역"),
	)

	relayCtx, relayCancel := context.WithCancel(ctx)
	t.Cleanup(relayCancel)
	connector := &chatrelay.Connector{
		Config: chatrelay.ConnectorConfig{
			RelayURL:      spaceURL,
			Credential:    device.DeviceAccessToken,
			DeviceID:      device.DeviceID,
			LocalAccounts: []string{localAccountID},
			DaemonVersion: "local-e2e",
			Capabilities:  []string{chatrelay.OperationOpenAIChatCompletions, chatrelay.OperationOpenAIModels},
		},
		Dispatcher: kittypawserver.NewChatRelayDispatcher(kittypaw),
	}
	go connector.Run(relayCtx, chatrelay.RunOptions{
		RetryInitialDelay: 100 * time.Millisecond,
		RetryMaxDelay:     250 * time.Millisecond,
		Logf: func(format string, args ...any) {
			t.Logf("space relay: "+format, args...)
		},
	})

	browser := newBrowserClient(t)
	spaceBrowserLogin(t, browser, spaceURL)
	assertBrowserSession(t, browser, spaceURL)
	waitForBrowserRoute(ctx, t, browser, spaceURL, device.DeviceID)

	offer := browserChatCompletion(t, browser, spaceURL, device.DeviceID, "환율 알려줘")
	if !strings.Contains(offer, "환율 조회") || !strings.Contains(offer, "설치") {
		t.Fatalf("first chat response did not offer exchange-rate install:\n%s", offer)
	}

	installed := browserChatCompletion(t, browser, spaceURL, device.DeviceID, "네")
	for _, want := range []string{"설치했어요", "환율", "1 USD = 1477 KRW"} {
		if !strings.Contains(installed, want) {
			t.Fatalf("install chat response missing %q:\n%s", want, installed)
		}
	}

	reused := browserChatCompletion(t, browser, spaceURL, device.DeviceID, "원화로 환율 다시 알려줘")
	if strings.Contains(reused, "설치해서") || strings.Contains(reused, "설치하면") {
		t.Fatalf("installed follow-up should not offer exchange-rate reinstall:\n%s", reused)
	}
	for _, want := range []string{"1 KRW =", "USD", "JPY"} {
		if !strings.Contains(reused, want) {
			t.Fatalf("installed follow-up did not convert exchange-rate with %q:\n%s", want, reused)
		}
	}

	weatherOffer := browserChatCompletion(t, browser, spaceURL, device.DeviceID, "강남역 날씨 알려줘")
	if !strings.Contains(weatherOffer, "현재 날씨") || !strings.Contains(weatherOffer, "설치") {
		t.Fatalf("weather chat response did not offer weather install:\n%s", weatherOffer)
	}

	weatherInstalled := browserChatCompletion(t, browser, spaceURL, device.DeviceID, "네")
	for _, want := range []string{"설치했어요", "강남역 현재 날씨", "37.4979", "127.0276"} {
		if !strings.Contains(weatherInstalled, want) {
			t.Fatalf("weather install chat response missing %q:\n%s", want, weatherInstalled)
		}
	}

	weatherReused := browserChatCompletion(t, browser, spaceURL, device.DeviceID, "강남역 날씨 다시")
	if strings.Contains(weatherReused, "설치해서") || strings.Contains(weatherReused, "설치하면") {
		t.Fatalf("installed weather follow-up should not offer reinstall:\n%s", weatherReused)
	}
	if !strings.Contains(weatherReused, "강남역 현재 날씨") {
		t.Fatalf("installed weather follow-up did not run weather-now:\n%s", weatherReused)
	}
}

func newFakeGoogle(t *testing.T) *httptest.Server {
	t.Helper()
	var mu sync.Mutex
	validCodes := make(map[string]struct{})
	seq := 0

	mux := http.NewServeMux()
	mux.HandleFunc("/o/oauth2/v2/auth", func(w http.ResponseWriter, r *http.Request) {
		redirectURI := r.URL.Query().Get("redirect_uri")
		state := r.URL.Query().Get("state")
		if redirectURI == "" || state == "" {
			http.Error(w, "missing redirect_uri or state", http.StatusBadRequest)
			return
		}
		mu.Lock()
		seq++
		code := fmt.Sprintf("fake-code-%d", seq)
		validCodes[code] = struct{}{}
		mu.Unlock()

		target, err := url.Parse(redirectURI)
		if err != nil {
			http.Error(w, "bad redirect_uri", http.StatusBadRequest)
			return
		}
		q := target.Query()
		q.Set("code", code)
		q.Set("state", state)
		target.RawQuery = q.Encode()
		http.Redirect(w, r, target.String(), http.StatusFound)
	})
	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		if err := r.ParseForm(); err != nil {
			http.Error(w, "bad form", http.StatusBadRequest)
			return
		}
		code := r.Form.Get("code")
		if code == "" || r.Form.Get("code_verifier") == "" {
			http.Error(w, "missing code or verifier", http.StatusBadRequest)
			return
		}
		mu.Lock()
		_, ok := validCodes[code]
		delete(validCodes, code)
		mu.Unlock()
		if !ok {
			http.Error(w, "unknown code", http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"access_token": "fake-google-access"})
	})
	mux.HandleFunc("/userinfo", func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer fake-google-access" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{
			"id":      "local-e2e-user",
			"email":   "local-e2e@example.test",
			"name":    "Local E2E",
			"picture": "https://example.test/avatar.png",
		})
	})
	return httptest.NewServer(mux)
}

func portalGoogleLogin(t *testing.T, client *http.Client, portalURL string) tokenResponse {
	t.Helper()
	resp, err := client.Get(portalURL + "/auth/google")
	if err != nil {
		t.Fatalf("portal google login: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("portal google login status = %d; body=%s", resp.StatusCode, body)
	}
	var out tokenResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode portal token response: %v", err)
	}
	if out.AccessToken == "" || out.RefreshToken == "" {
		t.Fatalf("portal token response missing tokens: %+v", out)
	}
	return out
}

func pairDevice(t *testing.T, client *http.Client, portalURL, accessToken string) pairResponse {
	t.Helper()
	body := strings.NewReader(`{"name":"local-e2e","capabilities":{"daemon_version":"local-e2e"}}`)
	req, err := http.NewRequest(http.MethodPost, portalURL+"/auth/devices/pair", body)
	if err != nil {
		t.Fatalf("new pair request: %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("pair device: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(resp.Body)
		t.Fatalf("pair device status = %d; body=%s", resp.StatusCode, raw)
	}
	var out pairResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode pair response: %v", err)
	}
	if out.DeviceID == "" || out.DeviceAccessToken == "" || out.DeviceRefreshToken == "" {
		t.Fatalf("pair response missing credentials: %+v", out)
	}
	return out
}

func newBrowserClient(t *testing.T) *http.Client {
	t.Helper()
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("new cookie jar: %v", err)
	}
	return &http.Client{
		Timeout:   10 * time.Second,
		Jar:       jar,
		Transport: noBrowserAuthTransport{base: http.DefaultTransport},
	}
}

type noBrowserAuthTransport struct {
	base http.RoundTripper
}

func (t noBrowserAuthTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if strings.HasPrefix(req.URL.Path, "/chat/api/") && req.Header.Get("Authorization") != "" {
		return nil, fmt.Errorf("browser chat API request carried Authorization header")
	}
	return t.base.RoundTrip(req)
}

func spaceBrowserLogin(t *testing.T, browser *http.Client, spaceURL string) {
	t.Helper()
	resp, err := browser.Get(spaceURL + "/auth/login/google")
	if err != nil {
		t.Fatalf("space browser login: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(resp.Body)
		t.Fatalf("space browser login final status = %d; url=%s body=%s", resp.StatusCode, resp.Request.URL.String(), raw)
	}
	if resp.Request == nil || resp.Request.URL.Path != "/chat/" {
		t.Fatalf("space browser login final URL = %v, want /chat/", resp.Request.URL)
	}
}

func assertBrowserSession(t *testing.T, browser *http.Client, spaceURL string) {
	t.Helper()
	resp, err := browser.Get(spaceURL + "/chat/api/session")
	if err != nil {
		t.Fatalf("browser session: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(resp.Body)
		t.Fatalf("browser session status = %d; body=%s", resp.StatusCode, raw)
	}
}

func waitForBrowserRoute(ctx context.Context, t *testing.T, browser *http.Client, spaceURL, deviceID string) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	var last string
	for time.Now().Before(deadline) {
		if ctx.Err() != nil {
			t.Fatalf("context ended while waiting for route: %v", ctx.Err())
		}
		ok, detail := browserRouteExists(t, browser, spaceURL, deviceID)
		if ok {
			return
		}
		last = detail
		time.Sleep(150 * time.Millisecond)
	}
	t.Fatalf("route for device %s did not appear; last=%s", deviceID, last)
}

func browserRouteExists(t *testing.T, browser *http.Client, spaceURL, deviceID string) (bool, string) {
	t.Helper()
	resp, err := browser.Get(spaceURL + "/chat/api/routes")
	if err != nil {
		return false, err.Error()
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return false, fmt.Sprintf("status=%d body=%s", resp.StatusCode, raw)
	}
	var routes struct {
		Data []struct {
			DeviceID      string   `json:"device_id"`
			LocalAccounts []string `json:"local_accounts"`
		} `json:"data"`
	}
	if err := json.Unmarshal(raw, &routes); err != nil {
		return false, fmt.Sprintf("decode routes: %v body=%s", err, raw)
	}
	for _, route := range routes.Data {
		if route.DeviceID == deviceID && contains(route.LocalAccounts, localAccountID) {
			return true, string(raw)
		}
	}
	return false, string(raw)
}

func assertBrowserChatCompletion(t *testing.T, browser *http.Client, spaceURL, deviceID string) {
	t.Helper()
	raw := browserChatCompletion(t, browser, spaceURL, deviceID, "hello from browser")
	if !bytes.Contains([]byte(raw), []byte("hello from local e2e daemon")) {
		t.Fatalf("chat completion body = %s, want daemon response", raw)
	}
}

func browserChatCompletion(t *testing.T, browser *http.Client, spaceURL, deviceID, message string) string {
	t.Helper()
	body := strings.NewReader(fmt.Sprintf(
		`{"model":"local-e2e","messages":[{"role":"user","content":%q}],"stream":true}`,
		message,
	))
	req, err := http.NewRequest(http.MethodPost, spaceURL+"/chat/api/nodes/"+deviceID+"/accounts/"+localAccountID+"/v1/chat/completions", body)
	if err != nil {
		t.Fatalf("new chat completion request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := browser.Do(req)
	if err != nil {
		t.Fatalf("browser chat completion: %v", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("browser chat completion status = %d; body=%s", resp.StatusCode, raw)
	}
	if got := resp.Header.Get("Content-Type"); !strings.Contains(got, "text/event-stream") {
		t.Fatalf("chat completion content-type = %q, want text/event-stream", got)
	}
	return string(raw)
}

type e2eDispatcher struct{}

func (e2eDispatcher) Dispatch(_ context.Context, req chatrelay.RequestFrame) (chatrelay.DispatchResult, error) {
	if req.Operation != chatrelay.OperationOpenAIChatCompletions {
		return chatrelay.DispatchResult{}, chatrelay.DispatchError{Code: "unsupported_operation", Message: "unsupported operation"}
	}
	if !bytes.Contains(req.Body, []byte("hello from browser")) {
		return chatrelay.DispatchResult{}, fmt.Errorf("unexpected relay body: %s", req.Body)
	}
	return chatrelay.DispatchResult{
		Status:  http.StatusOK,
		Headers: map[string]string{"content-type": "text/event-stream"},
		Body:    []byte("data: {\"choices\":[{\"delta\":{\"content\":\"hello from local e2e daemon\"}}]}\n\ndata: [DONE]\n\n"),
	}, nil
}

type process struct {
	name string
	done chan error
	logs *syncBuffer
	pid  int
}

func startGoRun(ctx context.Context, t *testing.T, name, dir string, args []string, env map[string]string) *process {
	t.Helper()
	cmd := exec.CommandContext(ctx, "go", append([]string{"run"}, args...)...)
	cmd.Dir = dir
	cmd.Env = mergedEnv(env)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	logs := &syncBuffer{}
	cmd.Stdout = logs
	cmd.Stderr = logs
	if err := cmd.Start(); err != nil {
		t.Fatalf("start %s: %v", name, err)
	}
	p := &process{name: name, done: make(chan error, 1), logs: logs, pid: cmd.Process.Pid}
	go func() {
		p.done <- cmd.Wait()
	}()
	t.Cleanup(func() {
		terminateProcessGroup(p.pid, syscall.SIGTERM)
		select {
		case <-p.done:
		case <-time.After(2 * time.Second):
			terminateProcessGroup(p.pid, syscall.SIGKILL)
			<-p.done
		}
	})
	return p
}

func terminateProcessGroup(pid int, sig syscall.Signal) {
	if pid <= 0 {
		return
	}
	_ = syscall.Kill(-pid, sig)
}

func waitForHealth(ctx context.Context, t *testing.T, endpoint string, p *process) {
	t.Helper()
	client := &http.Client{Timeout: 500 * time.Millisecond}
	deadline := time.Now().Add(20 * time.Second)
	var last string
	for time.Now().Before(deadline) {
		select {
		case err := <-p.done:
			t.Fatalf("%s exited before healthy: %v\nlogs:\n%s", p.name, err, p.logs.String())
		default:
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
		if err != nil {
			t.Fatalf("health request: %v", err)
		}
		resp, err := client.Do(req)
		if err == nil {
			raw, _ := io.ReadAll(resp.Body)
			_ = resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return
			}
			last = fmt.Sprintf("status=%d body=%s", resp.StatusCode, raw)
		} else {
			last = err.Error()
		}
		time.Sleep(150 * time.Millisecond)
	}
	t.Fatalf("%s did not become healthy at %s; last=%s\nlogs:\n%s", p.name, endpoint, last, p.logs.String())
}

type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

func migratePortalDB(t *testing.T, root, dbURL string) {
	t.Helper()
	source := "file://" + filepath.Join(root, "apps/portal/migrations")
	target := "pgx5://" + stripScheme(dbURL)
	m, err := migrate.New(source, target)
	if err != nil {
		t.Fatalf("migrate new: %v", err)
	}
	if err := m.Drop(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		t.Fatalf("migrate drop: %v", err)
	}
	_, _ = m.Close()
	m, err = migrate.New(source, target)
	if err != nil {
		t.Fatalf("migrate new after drop: %v", err)
	}
	defer func() { _, _ = m.Close() }()
	if err := m.Up(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		t.Fatalf("migrate up: %v", err)
	}
}

func repoRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime caller failed")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(file), "../.."))
}

func reserveAddr(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserve port: %v", err)
	}
	defer ln.Close()
	return ln.Addr().String()
}

func portOnly(addr string) string {
	_, port, err := net.SplitHostPort(addr)
	if err != nil {
		panic(err)
	}
	return port
}

func generatePrivateKeyB64(t *testing.T) string {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate rsa key: %v", err)
	}
	der, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatalf("marshal rsa key: %v", err)
	}
	pemBytes := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})
	return base64.StdEncoding.EncodeToString(pemBytes)
}

func requireTestDatabase(t *testing.T, dbURL string) {
	t.Helper()
	if !strings.Contains(dbURL, "_test") {
		t.Fatalf("DATABASE_URL must point at a test DB (must contain \"_test\"); got %q", dbURL)
	}
}

func stripScheme(rawURL string) string {
	if _, rest, ok := strings.Cut(rawURL, "://"); ok {
		return rest
	}
	return rawURL
}

func mergedEnv(extra map[string]string) []string {
	out := os.Environ()
	if os.Getenv("GOCACHE") == "" {
		out = append(out, "GOCACHE=/private/tmp/kitty-go-build")
	}
	for key, value := range extra {
		out = append(out, key+"="+value)
	}
	return out
}

func contains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

type registryPackage struct {
	ID   string
	TOML string
	JS   string
}

func newFakeRegistry(t *testing.T, packages ...registryPackage) *httptest.Server {
	t.Helper()
	byID := make(map[string]registryPackage, len(packages))
	for _, pkg := range packages {
		byID[pkg.ID] = pkg
	}

	var serverURL string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/index.json" {
			entries := make([]map[string]string, 0, len(packages))
			for _, pkg := range packages {
				entries = append(entries, map[string]string{
					"id":          pkg.ID,
					"name":        registryPackageName(pkg.TOML),
					"version":     "1.0.0",
					"description": "local e2e package",
					"url":         serverURL + "/" + pkg.ID,
				})
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"version": 1, "packages": entries})
			return
		}

		parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
		if len(parts) != 2 {
			http.NotFound(w, r)
			return
		}
		pkg, ok := byID[parts[0]]
		if !ok {
			http.NotFound(w, r)
			return
		}
		switch parts[1] {
		case "package.toml":
			w.Header().Set("Content-Type", "text/plain")
			_, _ = w.Write([]byte(pkg.TOML))
		case "main.js":
			w.Header().Set("Content-Type", "application/javascript")
			_, _ = w.Write([]byte(pkg.JS))
		default:
			http.NotFound(w, r)
		}
	}))
	serverURL = srv.URL
	return srv
}

func registryPackageName(toml string) string {
	for _, line := range strings.Split(toml, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "name = ") {
			return strings.Trim(strings.TrimPrefix(line, "name = "), `"`)
		}
	}
	return "Local E2E Package"
}

func exchangeRateRegistryPackage() registryPackage {
	return registryPackage{
		ID: "exchange-rate",
		TOML: `[meta]
id = "exchange-rate"
name = "환율 조회"
version = "1.0.0"
description = "키 없이 환율 표를 바로 조회합니다."
`,
		JS: `return "📈 환율 (2026-05-03)\n\n1 USD = 1477 KRW\n1 USD = 156.56 JPY";`,
	}
}

func weatherNowRegistryPackage() registryPackage {
	return registryPackage{
		ID: "weather-now",
		TOML: `[meta]
id = "weather-now"
name = "현재 날씨"
version = "1.0.0"
description = "현재 날씨와 비 여부를 즉답합니다."

[permissions]
context = ["location"]
`,
		JS: `const ctx = JSON.parse(__context__);
const loc = ctx.params.location;
return loc.label + " 현재 날씨\n좌표 " + loc.lat + "," + loc.lon + "\n1시간 강수: 없음";`,
	}
}

func newFakeKittypawAPI(t *testing.T) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/geo/resolve", func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Query().Get("q"); got != "강남역" {
			http.Error(w, fmt.Sprintf("geo query = %q, want 강남역", got), http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"lat":37.4979,"lon":127.0276,"name_matched":"강남역"}`))
	})
	return httptest.NewServer(mux)
}

type kittypawRelayConfig struct {
	registryURL          string
	apiBaseURL           string
	weatherSlotsResponse string
}

type kittypawRelayOption func(*kittypawRelayConfig)

func withKittypawAPIBaseURL(apiBaseURL string) kittypawRelayOption {
	return func(cfg *kittypawRelayConfig) {
		cfg.apiBaseURL = apiBaseURL
	}
}

func withE2EWeatherSlots(locationQuery string) kittypawRelayOption {
	return func(cfg *kittypawRelayConfig) {
		cfg.weatherSlotsResponse = fmt.Sprintf(`{"location_query":%q}`, locationQuery)
	}
}

func newKittypawRelayServer(t *testing.T, registryURL string, opts ...kittypawRelayOption) *kittypawserver.Server {
	t.Helper()
	relayCfg := kittypawRelayConfig{registryURL: registryURL}
	for _, opt := range opts {
		opt(&relayCfg)
	}

	baseDir := filepath.Join(t.TempDir(), "accounts", localAccountID)
	cfg := core.DefaultConfig()
	cfg.Registry.URL = relayCfg.registryURL
	cfg.Sandbox.TimeoutSecs = 5

	account := &core.Account{ID: localAccountID, BaseDir: baseDir, Config: &cfg}
	if err := account.EnsureDirs(); err != nil {
		t.Fatalf("ensure account dirs: %v", err)
	}
	st, err := store.Open(account.DBPath())
	if err != nil {
		t.Fatalf("open kittypaw store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	secrets, err := core.LoadSecretsFrom(account.SecretsPath())
	if err != nil {
		t.Fatalf("load kittypaw secrets: %v", err)
	}
	apiTokenMgr := core.NewAPITokenManager(baseDir, secrets)
	if relayCfg.apiBaseURL != "" {
		if err := apiTokenMgr.SaveAPIBaseURL(core.DefaultAPIServerURL, relayCfg.apiBaseURL); err != nil {
			t.Fatalf("save kittypaw api base url: %v", err)
		}
	}

	deps := &kittypawserver.AccountDeps{
		Account:     account,
		Store:       st,
		Provider:    e2eLLMProvider{weatherSlotsResponse: relayCfg.weatherSlotsResponse},
		Sandbox:     sandbox.New(cfg.Sandbox),
		PkgMgr:      core.NewPackageManagerFrom(baseDir, secrets),
		APITokenMgr: apiTokenMgr,
	}
	return kittypawserver.New([]*kittypawserver.AccountDeps{deps}, "local-e2e")
}

type e2eLLMProvider struct {
	weatherSlotsResponse string
}

func (p e2eLLMProvider) Generate(_ context.Context, msgs []core.LlmMessage) (*llm.Response, error) {
	if p.weatherSlotsResponse != "" && strings.Contains(e2eMessageText(msgs), `"location_query"`) {
		return &llm.Response{Content: p.weatherSlotsResponse}, nil
	}
	return &llm.Response{Content: "local e2e provider fallback"}, nil
}

func (p e2eLLMProvider) GenerateWithTools(ctx context.Context, msgs []core.LlmMessage, _ []llm.Tool) (*llm.Response, error) {
	return p.Generate(ctx, msgs)
}

func (e2eLLMProvider) ContextWindow() int { return 128_000 }

func (e2eLLMProvider) MaxTokens() int { return 4_096 }

func e2eMessageText(msgs []core.LlmMessage) string {
	var b strings.Builder
	for _, msg := range msgs {
		b.WriteString(msg.Content)
		for _, block := range msg.ContentBlocks {
			b.WriteString(block.Content)
		}
	}
	return b.String()
}
