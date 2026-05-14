package server

import (
	"os"
	"strings"
	"testing"
)

func readWebAssetForI18nTest(t *testing.T, path string) string {
	t.Helper()
	src, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(src)
}

func TestWebLoadsI18nBeforeAppModules(t *testing.T) {
	src := readWebAssetForI18nTest(t, "web/index.html")
	i18n := strings.Index(src, `<script src="/i18n.generated.js"></script>`)
	if i18n < 0 {
		t.Fatal("web index must load i18n.generated.js")
	}
	for _, module := range []string{
		`<script src="/app.js"></script>`,
		`<script src="/chat.js"></script>`,
		`<script src="/skills.js"></script>`,
		`<script src="/projects.js"></script>`,
		`<script src="/memory.js"></script>`,
		`<script src="/settings.js"></script>`,
	} {
		pos := strings.Index(src, module)
		if pos < 0 {
			t.Fatalf("web index must load %s", module)
		}
		if i18n > pos {
			t.Fatalf("web index must load i18n.generated.js before %s", module)
		}
	}
}

func TestWebI18nRuntimeExposesPickerAndTranslation(t *testing.T) {
	src := readWebAssetForI18nTest(t, "web/i18n.generated.js")
	for _, token := range []string{
		"window.KittyPawI18n",
		"function t(",
		"function setLocale(",
		"function mountLanguagePicker(",
		"kp_lang",
		"common.language",
	} {
		if !strings.Contains(src, token) {
			t.Fatalf("i18n runtime missing token %s", token)
		}
	}
}

func TestWebAppUsesI18nPicker(t *testing.T) {
	src := readWebAssetForI18nTest(t, "web/app.js")
	for _, token := range []string{
		"KittyPawI18n",
		"mountLanguagePicker",
		"app.signIn",
		"nav.settings",
		"dashboard.title",
		"dashboard.todayRuns",
		"dashboard.noExecutions",
	} {
		if !strings.Contains(src, token) {
			t.Fatalf("web app missing i18n token %s", token)
		}
	}
}

func TestWebAppLoadsSavedAccountLocaleBeforeRendering(t *testing.T) {
	src := readWebAssetForI18nTest(t, "web/app.js")
	initStart := strings.Index(src, "async init()")
	if initStart < 0 {
		t.Fatal("web app missing init method")
	}
	initEnd := strings.Index(src[initStart:], "\n  isChatSurface()")
	if initEnd < 0 {
		t.Fatal("web app init method end not found")
	}
	init := src[initStart : initStart+initEnd]
	loadCall := strings.Index(init, "await this.loadAccountLocalePreference()")
	if loadCall < 0 {
		t.Fatalf("web app init must load saved account locale before rendering, got:\n%s", init)
	}
	renderCall := strings.Index(init, "await this.startCurrentSurface()")
	if renderCall < 0 {
		t.Fatalf("web app init missing surface render call, got:\n%s", init)
	}
	if loadCall > renderCall {
		t.Fatalf("web app must load saved account locale before rendering, got:\n%s", init)
	}

	for _, token := range []string{
		"async loadAccountLocalePreference()",
		"'/api/settings/locale'",
		"locale.saved === true",
		"I18n.setLocale(locale.locale)",
	} {
		if !strings.Contains(src, token) {
			t.Fatalf("web app missing saved locale token %s", token)
		}
	}
}

func TestWebI18nAttributeInterpolationsUseAttributeEscaper(t *testing.T) {
	app := readWebAssetForI18nTest(t, "web/app.js")
	if !strings.Contains(app, "function escHTMLAttr(") {
		t.Fatal("web app must expose escHTMLAttr for quoted HTML attributes")
	}
	cases := map[string][]string{
		"web/app.js":      {`class="value ${escHTMLAttr(cls || '')}`},
		"web/chat.js":     {`placeholder="${escHTMLAttr(chatT('chat.placeholder'`},
		"web/settings.js": {`value="${escHTMLAttr(chatID)}"`},
		"web/skills.js":   {`placeholder="' + escHTMLAttr(skillsT('skills.search'`, `data-pkg-id="' + escHTMLAttr(id) + '"`},
		"web/projects.js": {`placeholder="' + escHTMLAttr(projectsT('projects.ticketBody'`, `data-ticket-id="' + escHTMLAttr(ticket.id) + '"`},
		"web/memory.js":   {`placeholder="${escHTMLAttr(memoryT('memory.search'`},
	}
	for path, tokens := range cases {
		src := readWebAssetForI18nTest(t, path)
		for _, token := range tokens {
			if !strings.Contains(src, token) {
				t.Fatalf("%s missing attribute escaper token %s", path, token)
			}
		}
	}
}

func TestLocalWebModulesUseI18nKeys(t *testing.T) {
	cases := map[string][]string{
		"web/chat.js":     {"chat.placeholder", "chat.send", "chat.permissionRequest"},
		"web/settings.js": {"settings.title", "settings.channels", "settings.llmProvider"},
		"web/skills.js":   {"skills.title", "skills.subtitle", "skills.search"},
		"web/projects.js": {"projects.title", "projects.newTicket", "projects.jobs", "projects.drivers", "projects.projectChat", "projects.ticketChat"},
		"web/memory.js":   {"memory.title", "memory.search", "memory.pending", "memory.saved"},
	}
	for path, keys := range cases {
		src := readWebAssetForI18nTest(t, path)
		for _, key := range keys {
			if !strings.Contains(src, key) {
				t.Fatalf("%s missing i18n key %s", path, key)
			}
		}
	}
}

func TestSkillsGalleryRendersScheduleStatusFields(t *testing.T) {
	src := readWebAssetForI18nTest(t, "web/skills.js")
	for _, token := range []string{
		"skill.next_run",
		"skill.last_run",
		"skill.failure_count",
		"_skillScheduleMetaHTML",
	} {
		if !strings.Contains(src, token) {
			t.Fatalf("web/skills.js missing schedule status token %s", token)
		}
	}
}

func TestLocalWebModulesAvoidGlobalRendererCollision(t *testing.T) {
	chat := readWebAssetForI18nTest(t, "web/chat.js")
	skills := readWebAssetForI18nTest(t, "web/skills.js")
	for path, src := range map[string]string{"web/chat.js": chat, "web/skills.js": skills} {
		if strings.Contains(src, "function renderMarkdown(") {
			t.Fatalf("%s must not declare global renderMarkdown", path)
		}
	}
	for path, token := range map[string]string{
		"web/chat.js":   "renderChatMarkdown(result)",
		"web/skills.js": "renderSkillsMarkdown(readme)",
	} {
		src := map[string]string{"web/chat.js": chat, "web/skills.js": skills}[path]
		if !strings.Contains(src, token) {
			t.Fatalf("%s missing module-specific markdown call %s", path, token)
		}
	}
	for path, token := range map[string]string{
		"web/chat.js":   "function renderChatMarkdown(",
		"web/skills.js": "function renderSkillsMarkdown(",
	} {
		src := map[string]string{"web/chat.js": chat, "web/skills.js": skills}[path]
		if !strings.Contains(src, token) {
			t.Fatalf("%s missing module-specific markdown helper %s", path, token)
		}
	}
	if !strings.Contains(skills, "esc(String(skill.version || 1))") {
		t.Fatal("web/skills.js must escape API-provided skill.version before innerHTML insertion")
	}
}
