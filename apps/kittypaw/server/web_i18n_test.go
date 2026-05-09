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
		`<script src="/kanban.js"></script>`,
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
	} {
		if !strings.Contains(src, token) {
			t.Fatalf("web app missing i18n token %s", token)
		}
	}
}
