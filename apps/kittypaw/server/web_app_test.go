package server

import (
	"os"
	"strings"
	"testing"
)

func TestWebAppApiRawRoutesUnauthorizedBackToLogin(t *testing.T) {
	src, err := os.ReadFile("web/app.js")
	if err != nil {
		t.Fatalf("read web app: %v", err)
	}
	body := string(src)
	start := strings.Index(body, "async function apiRaw")
	if start < 0 {
		t.Fatal("apiRaw function not found")
	}
	end := strings.Index(body[start:], "\n}\n\n/** Fetch with Bearer auth header. */")
	if end < 0 {
		t.Fatal("apiRaw function end not found")
	}
	apiRaw := body[start : start+end]
	if !strings.Contains(apiRaw, "res.status === 401") || !strings.Contains(apiRaw, "App.showLogin") {
		t.Fatalf("apiRaw must send expired sessions back to login, got:\n%s", apiRaw)
	}
}

func TestWebAppApiRoutesUnauthorizedBackToLogin(t *testing.T) {
	src, err := os.ReadFile("web/app.js")
	if err != nil {
		t.Fatalf("read web app: %v", err)
	}
	body := string(src)
	start := strings.Index(body, "async function api(")
	if start < 0 {
		t.Fatal("api function not found")
	}
	end := strings.Index(body[start:], "\n}\n\nasync function apiPost")
	if end < 0 {
		t.Fatal("api function end not found")
	}
	api := body[start : start+end]
	if !strings.Contains(api, "res.status === 401") || !strings.Contains(api, "App.showLogin") {
		t.Fatalf("api must send expired sessions back to login, got:\n%s", api)
	}
	if !strings.Contains(api, "res.status === 403") {
		t.Fatalf("api must surface forbidden sessions, got:\n%s", api)
	}
}

func TestWebAppBootstrapDoesNotSwallowUnauthorized(t *testing.T) {
	src, err := os.ReadFile("web/app.js")
	if err != nil {
		t.Fatalf("read web app: %v", err)
	}
	body := string(src)
	start := strings.Index(body, "async bootstrap()")
	if start < 0 {
		t.Fatal("bootstrap method not found")
	}
	end := strings.Index(body[start:], "\n  },")
	if end < 0 {
		t.Fatal("bootstrap method end not found")
	}
	bootstrap := body[start : start+end]
	if strings.Contains(bootstrap, "catch") && !strings.Contains(bootstrap, "throw") {
		t.Fatalf("bootstrap must not swallow auth failures, got:\n%s", bootstrap)
	}
}

func TestWebAppAuthenticatedAccountsUseSetupStatus(t *testing.T) {
	src, err := os.ReadFile("web/app.js")
	if err != nil {
		t.Fatalf("read web app: %v", err)
	}
	body := string(src)
	if strings.Contains(body, "auth.is_default === false") {
		t.Fatal("authenticated non-default accounts must not bypass account-scoped setup/status")
	}
	if !strings.Contains(body, "await this.startMainFlow()") {
		t.Fatal("authenticated accounts must enter setup/status flow")
	}
}

func TestWebAppRootIsLoginGateToSettings(t *testing.T) {
	src, err := os.ReadFile("web/app.js")
	if err != nil {
		t.Fatalf("read web app: %v", err)
	}
	body := string(src)
	if !strings.Contains(body, "isSettingsSurface()") {
		t.Fatal("web app must detect the /_settings control surface explicitly")
	}
	if !strings.Contains(body, "redirectToSettingsSurface()") || !strings.Contains(body, "location.replace('/_settings')") {
		t.Fatal("root surface must redirect authenticated users to /_settings")
	}
	if !strings.Contains(body, "location.assign('/_settings')") {
		t.Fatal("successful root login must navigate to /_settings")
	}
}

func TestWebAppDoesNotStartBrowserOnboarding(t *testing.T) {
	appSrc, err := os.ReadFile("web/app.js")
	if err != nil {
		t.Fatalf("read web app: %v", err)
	}
	app := string(appSrc)
	if strings.Contains(app, "Onboarding.start") {
		t.Fatal("web app must not start browser onboarding; first setup belongs to the CLI")
	}
	if strings.Contains(app, "launchWizard") {
		t.Fatal("web app must not expose a setup wizard launcher")
	}
	if !strings.Contains(app, "showCliSetupRequired") {
		t.Fatal("web app must show CLI setup instructions when setup is incomplete")
	}

	indexSrc, err := os.ReadFile("web/index.html")
	if err != nil {
		t.Fatalf("read web index: %v", err)
	}
	if strings.Contains(string(indexSrc), "onboarding.js") {
		t.Fatal("web index must not load browser onboarding code")
	}
}

func TestWebAppNonDefaultShellExposesAccountScopedKanban(t *testing.T) {
	src, err := os.ReadFile("web/app.js")
	if err != nil {
		t.Fatalf("read web app: %v", err)
	}
	body := string(src)
	start := strings.Index(body, "showShell() {")
	if start < 0 {
		t.Fatal("showShell method not found")
	}
	end := strings.Index(body[start:], "\n  switchTab(")
	if end < 0 {
		t.Fatal("showShell method end not found")
	}
	showShell := body[start : start+end]
	if strings.Contains(showShell, "const adminNav = this.isDefault") {
		t.Fatal("showShell must not hide Kanban inside the default-account-only nav")
	}
	if !strings.Contains(showShell, `data-tab="kanban"`) {
		t.Fatalf("showShell must expose account-scoped Kanban for every logged-in account, got:\n%s", showShell)
	}
	if strings.Contains(body, "wizardButton") {
		t.Fatal("showShell must not expose a setup wizard entry")
	}
}

func TestWebAppDoesNotUseDefaultOnlyLoginError(t *testing.T) {
	src, err := os.ReadFile("web/app.js")
	if err != nil {
		t.Fatalf("read web app: %v", err)
	}
	if strings.Contains(string(src), "This Web UI is currently available only for the default account.") {
		t.Fatal("web login must not report a default-account-only restriction")
	}
}

func TestWebAppControlShellDoesNotExposeChatNav(t *testing.T) {
	src, err := os.ReadFile("web/app.js")
	if err != nil {
		t.Fatalf("read web app: %v", err)
	}
	body := string(src)
	start := strings.Index(body, "showShell() {")
	if start < 0 {
		t.Fatal("showShell method not found")
	}
	end := strings.Index(body[start:], "\n  switchTab(")
	if end < 0 {
		t.Fatal("showShell method end not found")
	}
	showShell := body[start : start+end]
	if strings.Contains(showShell, `data-tab="chat"`) {
		t.Fatalf("control shell must not include Chat navigation, got:\n%s", showShell)
	}
	if !strings.Contains(showShell, "this.switchTab('settings')") {
		t.Fatalf("control shell must open Settings by default, got:\n%s", showShell)
	}
}

func TestWebAppChatSurfaceUsesChatOnlyBootstrap(t *testing.T) {
	src, err := os.ReadFile("web/app.js")
	if err != nil {
		t.Fatalf("read web app: %v", err)
	}
	body := string(src)
	if !strings.Contains(body, "chatOnly: false") || !strings.Contains(body, "isChatSurface()") {
		t.Fatal("web app must detect the /chat surface explicitly")
	}
	if !strings.Contains(body, "async bootstrapChat()") || !strings.Contains(body, "/api/chat/bootstrap") {
		t.Fatal("chat surface must use chat-only bootstrap, not control bootstrap")
	}
	if !strings.Contains(body, "showChatSurface()") {
		t.Fatal("chat surface must render a chat-only shell")
	}
	if strings.Contains(body, `data-tab="chat"`) {
		t.Fatal("control shell must not expose Chat navigation; /chat is the only chat surface")
	}
}
