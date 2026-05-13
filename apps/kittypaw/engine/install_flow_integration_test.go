package engine

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jinto/kittypaw/core"
	"github.com/jinto/kittypaw/llm"
	"github.com/jinto/kittypaw/sandbox"
	"github.com/jinto/kittypaw/store"
)

type registryPackageFixture struct {
	ID   string
	TOML string
	JS   string
}

func newInstallFlowSession(t *testing.T, registryURL string, responses ...string) *AccountRuntime {
	t.Helper()
	skipWithoutRuntime(t)

	baseDir := t.TempDir()
	cfg := core.DefaultConfig()
	cfg.Registry.URL = registryURL

	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { st.Close() })

	llmResponses := make([]*llm.Response, 0, len(responses))
	for _, response := range responses {
		llmResponses = append(llmResponses, mockResp(response))
	}

	return &AccountRuntime{
		Provider:       &mockProvider{responses: llmResponses},
		Sandbox:        sandbox.New(cfg.Sandbox),
		Store:          st,
		Config:         &cfg,
		BaseDir:        baseDir,
		PackageManager: core.NewPackageManagerFrom(baseDir, nil),
		Pipeline:       NewPipelineState(),
	}
}

func newFakeSkillRegistry(t *testing.T, packages ...registryPackageFixture) *httptest.Server {
	t.Helper()
	byID := make(map[string]registryPackageFixture, len(packages))
	for _, pkg := range packages {
		byID[pkg.ID] = pkg
	}

	var serverURL string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/index.json" {
			type entry struct {
				ID          string `json:"id"`
				Name        string `json:"name"`
				Version     string `json:"version"`
				Description string `json:"description"`
				URL         string `json:"url"`
			}
			entries := make([]entry, 0, len(packages))
			for _, pkg := range packages {
				entries = append(entries, entry{
					ID:          pkg.ID,
					Name:        packageNameFromTOML(pkg.TOML),
					Version:     "1.0.0",
					Description: "local test package",
					URL:         serverURL + "/" + pkg.ID,
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

func packageNameFromTOML(toml string) string {
	for _, line := range strings.Split(toml, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "name = ") {
			return strings.Trim(strings.TrimPrefix(line, "name = "), `"`)
		}
	}
	return "Local Test Package"
}

func TestInstallConsentInstallsAndRunsExchangeRateFromRegistry(t *testing.T) {
	t.Setenv("KITTYPAW_ALLOW_INSECURE_REGISTRY", "1")
	t.Setenv("KITTYPAW_CONFIG_DIR", t.TempDir())

	registry := newFakeSkillRegistry(t, exchangeRatePackageFixture())
	defer registry.Close()
	sess := newInstallFlowSession(t, registry.URL)

	offer, err := sess.Run(context.Background(), webChatEvent("환율 알려줘"), nil)
	if err != nil {
		t.Fatalf("first turn Run error: %v", err)
	}
	if !strings.Contains(offer, "환율 조회") || !strings.Contains(offer, "설치") {
		t.Fatalf("expected exchange-rate install offer, got %q", offer)
	}

	out, err := sess.Run(context.Background(), webChatEvent("네"), nil)
	if err != nil {
		t.Fatalf("consent turn Run error: %v", err)
	}
	for _, want := range []string{"설치했어요", "환율", "1 USD = 1477 KRW"} {
		if !strings.Contains(out, want) {
			t.Fatalf("install/run output missing %q:\n%s", want, out)
		}
	}

	if _, err := os.Stat(filepath.Join(sess.BaseDir, "packages", "exchange-rate", "package.toml")); err != nil {
		t.Fatalf("exchange-rate package was not installed: %v", err)
	}
}

func TestInstalledExchangeRateFollowupRunsWithoutReinstallOffer(t *testing.T) {
	t.Setenv("KITTYPAW_ALLOW_INSECURE_REGISTRY", "1")
	t.Setenv("KITTYPAW_CONFIG_DIR", t.TempDir())

	registry := newFakeSkillRegistry(t, exchangeRatePackageFixture())
	defer registry.Close()
	sess := newInstallFlowSession(t, registry.URL)

	if _, err := sess.Run(context.Background(), webChatEvent("환율 알려줘"), nil); err != nil {
		t.Fatalf("offer turn Run error: %v", err)
	}
	if _, err := sess.Run(context.Background(), webChatEvent("네"), nil); err != nil {
		t.Fatalf("install turn Run error: %v", err)
	}

	out, err := sess.Run(context.Background(), webChatEvent("환율"), nil)
	if err != nil {
		t.Fatalf("followup turn Run error: %v", err)
	}
	if strings.Contains(out, "설치해서") || strings.Contains(out, "설치하면") {
		t.Fatalf("installed follow-up should not offer reinstall:\n%s", out)
	}
	if !strings.Contains(out, "1 USD = 1477 KRW") {
		t.Fatalf("installed follow-up did not run exchange-rate:\n%s", out)
	}
}

func TestInstallConsentInstallsAndRunsWeatherNowWithStructuredLocation(t *testing.T) {
	t.Setenv("KITTYPAW_ALLOW_INSECURE_REGISTRY", "1")
	t.Setenv("KITTYPAW_CONFIG_DIR", t.TempDir())

	registry := newFakeSkillRegistry(t, weatherNowPackageFixture())
	defer registry.Close()
	sess := newInstallFlowSession(t, registry.URL, `{"location_query":"강남역"}`)

	geo := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Query().Get("q"); got != "강남역" {
			t.Fatalf("geo query = %q, want 강남역", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"lat":37.4979,"lon":127.0276,"name_matched":"강남역"}`))
	}))
	defer geo.Close()
	oldBaseURL := kittypawAPIBaseURL
	kittypawAPIBaseURL = geo.URL
	t.Cleanup(func() { kittypawAPIBaseURL = oldBaseURL })

	offer, err := sess.Run(context.Background(), webChatEvent("강남역에 비오나? 지금?"), nil)
	if err != nil {
		t.Fatalf("first turn Run error: %v", err)
	}
	if !strings.Contains(offer, "현재 날씨") || !strings.Contains(offer, "설치") {
		t.Fatalf("expected weather-now install offer, got %q", offer)
	}

	out, err := sess.Run(context.Background(), webChatEvent("네"), nil)
	if err != nil {
		t.Fatalf("consent turn Run error: %v", err)
	}
	for _, want := range []string{"설치했어요", "강남역 현재 날씨", "37.4979", "127.0276"} {
		if !strings.Contains(out, want) {
			t.Fatalf("install/run output missing %q:\n%s", want, out)
		}
	}
}

func exchangeRatePackageFixture() registryPackageFixture {
	return registryPackageFixture{
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

func weatherNowPackageFixture() registryPackageFixture {
	return registryPackageFixture{
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
