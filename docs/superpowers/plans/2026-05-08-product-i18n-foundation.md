# Product I18n Foundation Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build the centralized i18n catalog/generator and apply it to the local `apps/kittypaw` web surfaces with a globe language picker.

**Architecture:** Translation source lives in root `i18n/catalog.yaml`; app-local generated assets are committed for embed compatibility. Local `kittypaw` web loads a generated JS runtime before existing web modules, uses `t(key, params)` for UI-owned strings, stores the selected locale in `localStorage`, and persists authenticated choices through a small account-scoped settings endpoint. Hosted `space`, legacy `chat`, and `portal` are intentionally deferred to follow-up plans that reuse the same catalog and generator.

**Tech Stack:** Go standard library generator, YAML subset parser for the fixed catalog shape, vanilla browser JavaScript, existing `apps/kittypaw/server` Go tests, static asset tests.

---

## Scope

This plan implements the first working slice from
`docs/superpowers/specs/2026-05-08-product-i18n-design.md`:

- Central catalog files.
- Generator and `--check` verification.
- Local `apps/kittypaw` generated web i18n asset.
- Local web language picker.
- Local web locale preference endpoint.
- Translation of local `App`, `Chat`, `Settings`, `Skills`, and `Kanban`
  UI-owned strings.

Follow-up plans will apply the same generated catalog to:

- `apps/space/internal/server/web`
- `apps/chat/internal/server/web`
- `apps/chat/internal/server/manual`
- `apps/portal` Go-rendered pages
- API error code hardening
- CLI language helper cleanup

## File Structure

Create:

- `i18n/catalog.yaml` - human-edited translation source of truth.
- `i18n/glossary.yaml` - canonical product terms and translations.
- `i18n/schema.json` - documented catalog shape for editor/tooling use.
- `scripts/generate_i18n.go` - validates catalog and writes app-local assets.
- `apps/kittypaw/server/web/i18n.generated.js` - generated local web runtime and messages.
- `apps/kittypaw/server/web_i18n_test.go` - static and endpoint tests for local web i18n.

Modify:

- `Makefile` - add `i18n-check`.
- `TASKS.md` - track the product i18n plan.
- `apps/kittypaw/server/web/index.html` - load `i18n.generated.js` before other modules.
- `apps/kittypaw/server/web/app.js` - app-level `t()`, picker, login/setup/dashboard strings.
- `apps/kittypaw/server/web/chat.js` - chat UI strings.
- `apps/kittypaw/server/web/settings.js` - settings UI strings.
- `apps/kittypaw/server/web/skills.js` - skills UI strings.
- `apps/kittypaw/server/web/kanban.js` - Kanban UI strings.
- `apps/kittypaw/server/web/style.css` - language picker layout.
- `apps/kittypaw/server/api_settings.go` - account-scoped locale preference endpoints.
- `apps/kittypaw/server/server.go` or route wiring file that mounts settings routes.
- Existing `apps/kittypaw/server/*_test.go` only where endpoint routing tests belong.

## Task 1: Catalog And Generator

**Files:**

- Create: `i18n/catalog.yaml`
- Create: `i18n/glossary.yaml`
- Create: `i18n/schema.json`
- Create: `scripts/generate_i18n.go`
- Modify: `Makefile`

- [ ] **Step 1: Create the initial catalog**

Create `i18n/catalog.yaml` with this structure and enough keys for local
`kittypaw` web foundation:

```yaml
locales: [ko, ja, en]
default: en

keys:
  common.language:
    ko: 언어
    ja: 言語
    en: Language
  common.korean:
    ko: 한국어
    ja: 韓国語
    en: Korean
  common.japanese:
    ko: 일본어
    ja: 日本語
    en: Japanese
  common.english:
    ko: 영어
    ja: 英語
    en: English
  common.refresh:
    ko: 새로고침
    ja: 更新
    en: Refresh
  common.loading:
    ko: 불러오는 중
    ja: 読み込み中
    en: Loading
  common.save:
    ko: 저장
    ja: 保存
    en: Save
  common.cancel:
    ko: 취소
    ja: キャンセル
    en: Cancel
  common.close:
    ko: 닫기
    ja: 閉じる
    en: Close
  common.connected:
    ko: 연결됨
    ja: 接続済み
    en: Connected
  common.notConnected:
    ko: 연결 안 됨
    ja: 未接続
    en: Not connected
  app.signIn:
    ko: 로그인
    ja: サインイン
    en: Sign in
  app.accountId:
    ko: 계정 ID
    ja: アカウントID
    en: Account ID
  app.password:
    ko: 비밀번호
    ja: パスワード
    en: Password
  app.invalidLogin:
    ko: 계정 ID 또는 비밀번호가 올바르지 않습니다.
    ja: アカウントIDまたはパスワードが正しくありません。
    en: Invalid account ID or password.
  app.sessionExpired:
    ko: 세션이 만료되었습니다. 다시 로그인하세요.
    ja: セッションが期限切れです。もう一度サインインしてください。
    en: Session expired. Sign in again.
  app.runSetupChat:
    ko: 웹 채팅을 시작하기 전에 터미널에서 kittypaw setup을 실행하세요.
    ja: Webチャットを始める前に、ターミナルでkittypaw setupを実行してください。
    en: Run kittypaw setup in your terminal before starting web chat.
  app.runSetupLocal:
    ko: 로컬 설정을 완료하려면 터미널에서 kittypaw setup을 실행하세요.
    ja: ローカル設定を完了するには、ターミナルでkittypaw setupを実行してください。
    en: Run kittypaw setup in your terminal to finish local setup.
  nav.dashboard:
    ko: 대시보드
    ja: ダッシュボード
    en: Dashboard
  nav.skills:
    ko: 스킬
    ja: スキル
    en: Skills
  nav.settings:
    ko: 설정
    ja: 設定
    en: Settings
  chat.placeholder:
    ko: 메시지를 입력하세요...
    ja: メッセージを入力...
    en: Type a message...
  chat.send:
    ko: 보내기
    ja: 送信
    en: Send
  chat.connecting:
    ko: 연결 중...
    ja: 接続中...
    en: Connecting...
  chat.connected:
    ko: 연결됨
    ja: 接続済み
    en: Connected
  chat.disconnected:
    ko: 연결 끊김
    ja: 切断されました
    en: Disconnected
  chat.connectionError:
    ko: 연결 오류
    ja: 接続エラー
    en: Connection error
  chat.connectionLost:
    ko: 연결이 끊겼습니다
    ja: 接続が失われました
    en: Connection lost
  chat.reconnect:
    ko: 다시 연결
    ja: 再接続
    en: Reconnect
  chat.permissionRequest:
    ko: 권한 요청
    ja: 権限リクエスト
    en: Permission Request
  chat.allow:
    ko: 허용
    ja: 許可
    en: Allow
  chat.deny:
    ko: 거부
    ja: 拒否
    en: Deny
  settings.title:
    ko: 설정
    ja: 設定
    en: Settings
  settings.workspaces:
    ko: 워크스페이스
    ja: ワークスペース
    en: Workspaces
  settings.noWorkspaces:
    ko: 워크스페이스 없음
    ja: ワークスペースがありません
    en: No workspaces
  settings.addWorkspace:
    ko: 워크스페이스 추가
    ja: ワークスペースを追加
    en: Add Workspace
  settings.channels:
    ko: 채널
    ja: チャンネル
    en: Channels
  settings.llmProvider:
    ko: LLM 제공자
    ja: LLMプロバイダー
    en: LLM Provider
  settings.change:
    ko: 변경
    ja: 変更
    en: Change
  settings.connect:
    ko: 연결
    ja: 接続
    en: Connect
  skills.title:
    ko: 스킬 갤러리
    ja: スキルギャラリー
    en: Skill Gallery
  skills.subtitle:
    ko: 자동화 스킬을 찾아 설치하고 설정합니다.
    ja: 自動化スキルを探して、インストールし、設定します。
    en: Browse, install, and configure automation skills.
  skills.search:
    ko: 스킬 검색...
    ja: スキルを検索...
    en: Search skills...
  kanban.title:
    ko: 칸반
    ja: カンバン
    en: Kanban
  kanban.task:
    ko: 작업
    ja: タスク
    en: Task
  kanban.newTask:
    ko: 새 작업
    ja: 新規タスク
    en: New Task
  kanban.titleField:
    ko: 제목
    ja: タイトル
    en: Title
  kanban.assignee:
    ko: 담당자
    ja: 担当者
    en: Assignee
  kanban.priority:
    ko: 우선순위
    ja: 優先度
    en: Priority
  kanban.status:
    ko: 상태
    ja: ステータス
    en: Status
  kanban.milestone:
    ko: 마일스톤
    ja: マイルストーン
    en: Milestone
  kanban.body:
    ko: 본문
    ja: 本文
    en: Body
  kanban.empty:
    ko: 비어 있음
    ja: 空です
    en: Empty
  kanban.allMilestones:
    ko: 모든 마일스톤
    ja: すべてのマイルストーン
    en: All milestones
  kanban.none:
    ko: 없음
    ja: なし
    en: None
  kanban.comments:
    ko: 댓글
    ja: コメント
    en: Comments
  kanban.noComments:
    ko: 댓글 없음
    ja: コメントはありません
    en: No comments
  kanban.runs:
    ko: 실행 기록
    ja: 実行履歴
    en: Runs
  kanban.noRuns:
    ko: 실행 기록 없음
    ja: 実行履歴はありません
    en: No runs
  kanban.claim:
    ko: 잡기
    ja: 取得
    en: Claim
  kanban.heartbeat:
    ko: 하트비트
    ja: ハートビート
    en: Heartbeat
  kanban.complete:
    ko: 완료
    ja: 完了
    en: Complete
  kanban.block:
    ko: 차단
    ja: ブロック
    en: Block
  kanban.unblock:
    ko: 차단 해제
    ja: ブロック解除
    en: Unblock
  kanban.reclaim:
    ko: 다시 잡기
    ja: 再取得
    en: Reclaim
  kanban.archive:
    ko: 보관
    ja: アーカイブ
    en: Archive
```

- [ ] **Step 2: Create the glossary**

Create `i18n/glossary.yaml`:

```yaml
terms:
  daemon:
    ko: 데몬
    ja: デーモン
    en: daemon
  workspace:
    ko: 워크스페이스
    ja: ワークスペース
    en: workspace
  route:
    ko: 라우트
    ja: ルート
    en: route
  kanban:
    ko: 칸반
    ja: カンバン
    en: Kanban
  milestone:
    ko: 마일스톤
    ja: マイルストーン
    en: milestone
  hostedChat:
    ko: 호스티드 채팅
    ja: ホステッドチャット
    en: hosted chat
```

- [ ] **Step 3: Create schema documentation**

Create `i18n/schema.json`:

```json
{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "type": "object",
  "required": ["locales", "default", "keys"],
  "properties": {
    "locales": {
      "type": "array",
      "items": { "enum": ["ko", "ja", "en"] },
      "minItems": 3,
      "maxItems": 3
    },
    "default": { "enum": ["en"] },
    "keys": {
      "type": "object",
      "additionalProperties": {
        "type": "object",
        "required": ["ko", "ja", "en"],
        "properties": {
          "ko": { "type": "string" },
          "ja": { "type": "string" },
          "en": { "type": "string" }
        }
      }
    }
  }
}
```

- [ ] **Step 4: Implement generator with validation**

Create `scripts/generate_i18n.go` as a `package main` file using only standard
library imports. It must:

- Parse the fixed catalog YAML subset.
- Validate locale set is exactly `ko`, `ja`, `en`.
- Validate every key has all three locales.
- Validate placeholder names match across locales.
- Write `apps/kittypaw/server/web/i18n.generated.js`.
- Support `--check` to fail if generated files are stale.

Core functions to include:

```go
var supportedLocales = []string{"ko", "ja", "en"}

type catalog struct {
    Locales []string
    Default string
    Keys    map[string]map[string]string
}

func validateCatalog(cat catalog) error {
    if !reflect.DeepEqual(cat.Locales, supportedLocales) {
        return fmt.Errorf("locales = %v, want %v", cat.Locales, supportedLocales)
    }
    if cat.Default != "en" {
        return fmt.Errorf("default locale = %q, want en", cat.Default)
    }
    for key, entries := range cat.Keys {
        base := placeholders(entries["en"])
        for _, locale := range supportedLocales {
            value, ok := entries[locale]
            if !ok || strings.TrimSpace(value) == "" {
                return fmt.Errorf("%s missing locale %s", key, locale)
            }
            if !sameStringSet(base, placeholders(value)) {
                return fmt.Errorf("%s placeholder mismatch in %s", key, locale)
            }
        }
    }
    return nil
}
```

- [ ] **Step 5: Add Makefile target**

Modify root `Makefile`:

```make
.PHONY: help list contracts-check i18n-check smoke-local e2e-local full-local-live

i18n-check:
	@go run scripts/generate_i18n.go --check
```

- [ ] **Step 6: Verify generator failure then pass**

Run once after creating the generator:

```bash
go run scripts/generate_i18n.go --check
```

Expected before generation: FAIL with a stale generated file message.

Run:

```bash
go run scripts/generate_i18n.go
go run scripts/generate_i18n.go --check
```

Expected after generation: PASS with no output.

- [ ] **Step 7: Commit foundation generator**

```bash
git add i18n scripts/generate_i18n.go Makefile apps/kittypaw/server/web/i18n.generated.js
git commit -m "feat: add product i18n catalog generator"
```

## Task 2: Local Locale Preference API

**Files:**

- Modify: `apps/kittypaw/server/api_settings.go`
- Modify: route wiring file under `apps/kittypaw/server`
- Test: `apps/kittypaw/server/api_settings_test.go`

- [ ] **Step 1: Write failing tests**

Add tests for:

- `GET /api/settings/locale` returns stored `pref.lang`.
- missing preference falls back to `en`.
- `POST /api/settings/locale` accepts only `ko`, `ja`, `en`.
- invalid locale returns `400`.

Test helper expectation:

```go
func TestSettingsLocalePreference(t *testing.T) {
    srv := newSettingsTestServer(t)
    req := httptest.NewRequest(http.MethodPost, "/api/settings/locale", strings.NewReader(`{"locale":"ko"}`))
    rr := httptest.NewRecorder()
    srv.routes().ServeHTTP(rr, req)
    if rr.Code != http.StatusOK {
        t.Fatalf("set locale code = %d body=%s", rr.Code, rr.Body.String())
    }

    req = httptest.NewRequest(http.MethodGet, "/api/settings/locale", nil)
    rr = httptest.NewRecorder()
    srv.routes().ServeHTTP(rr, req)
    if rr.Code != http.StatusOK {
        t.Fatalf("get locale code = %d body=%s", rr.Code, rr.Body.String())
    }
    var body struct{ Locale string `json:"locale"` }
    if err := json.NewDecoder(rr.Body).Decode(&body); err != nil {
        t.Fatalf("decode locale: %v", err)
    }
    if body.Locale != "ko" {
        t.Fatalf("locale = %q, want ko", body.Locale)
    }
}
```

- [ ] **Step 2: Run failing tests**

```bash
cd apps/kittypaw
go test ./server -run TestSettingsLocalePreference -count=1
```

Expected: FAIL because `/api/settings/locale` is not mounted.

- [ ] **Step 3: Implement handlers**

Add constants and handlers in `api_settings.go`:

```go
const userLocalePreferenceKey = "pref.lang"

func normalizeUILocale(value string) (string, bool) {
    switch strings.TrimSpace(value) {
    case "ko", "ja", "en":
        return strings.TrimSpace(value), true
    case "":
        return "en", true
    default:
        return "", false
    }
}

func (s *Server) handleSettingsLocaleGet(w http.ResponseWriter, r *http.Request) {
    acct, status, err := s.settingsRequestAccount(r)
    if err != nil {
        writeError(w, status, err.Error())
        return
    }
    value, ok, err := acct.Store.GetUserContext(userLocalePreferenceKey)
    if err != nil {
        writeError(w, http.StatusInternalServerError, err.Error())
        return
    }
    locale, valid := normalizeUILocale(value)
    if !ok || !valid {
        locale = "en"
    }
    writeJSON(w, http.StatusOK, map[string]string{"locale": locale})
}

func (s *Server) handleSettingsLocalePost(w http.ResponseWriter, r *http.Request) {
    acct, status, err := s.settingsRequestAccount(r)
    if err != nil {
        writeError(w, status, err.Error())
        return
    }
    var body struct {
        Locale string `json:"locale"`
    }
    if !decodeBody(w, r, &body) {
        return
    }
    locale, ok := normalizeUILocale(body.Locale)
    if !ok {
        writeError(w, http.StatusBadRequest, "unsupported locale")
        return
    }
    if err := acct.Store.SetUserContext(userLocalePreferenceKey, locale, "settings"); err != nil {
        writeError(w, http.StatusInternalServerError, err.Error())
        return
    }
    writeJSON(w, http.StatusOK, map[string]string{"locale": locale})
}
```

- [ ] **Step 4: Mount routes**

Add:

```go
r.Get("/api/settings/locale", s.handleSettingsLocaleGet)
r.Post("/api/settings/locale", s.handleSettingsLocalePost)
```

near the existing settings routes.

- [ ] **Step 5: Verify tests pass**

```bash
cd apps/kittypaw
go test ./server -run 'TestSettingsLocalePreference|TestSettings' -count=1
```

Expected: PASS.

- [ ] **Step 6: Commit locale API**

```bash
git add apps/kittypaw/server/api_settings.go apps/kittypaw/server/*_test.go
git commit -m "feat: persist local web locale preference"
```

## Task 3: Local Web Runtime And Picker

**Files:**

- Modify: `apps/kittypaw/server/web/index.html`
- Modify: `apps/kittypaw/server/web/app.js`
- Modify: `apps/kittypaw/server/web/style.css`
- Test: `apps/kittypaw/server/web_i18n_test.go`

- [ ] **Step 1: Write static source tests**

Create `apps/kittypaw/server/web_i18n_test.go`:

```go
package server

import (
    "os"
    "strings"
    "testing"
)

func TestWebLoadsI18nBeforeAppModules(t *testing.T) {
    body := readWebAssetForI18nTest(t, "web/index.html")
    i18n := strings.Index(body, `<script src="/i18n.generated.js"></script>`)
    app := strings.Index(body, `<script src="/app.js"></script>`)
    if i18n < 0 || app < 0 || i18n > app {
        t.Fatalf("index.html must load i18n.generated.js before app.js")
    }
}

func TestWebI18nRuntimeExposesPickerAndTranslation(t *testing.T) {
    body := readWebAssetForI18nTest(t, "web/i18n.generated.js")
    for _, token := range []string{
        "window.KittyPawI18n",
        "function t(",
        "function setLocale(",
        "function mountLanguagePicker(",
        "kp_lang",
        "common.language",
    } {
        if !strings.Contains(body, token) {
            t.Fatalf("i18n runtime missing %s", token)
        }
    }
}

func TestWebAppUsesI18nPicker(t *testing.T) {
    body := readWebAssetForI18nTest(t, "web/app.js")
    for _, token := range []string{"KittyPawI18n", "mountLanguagePicker", "app.signIn", "nav.settings"} {
        if !strings.Contains(body, token) {
            t.Fatalf("app.js missing i18n token %s", token)
        }
    }
}

func readWebAssetForI18nTest(t *testing.T, path string) string {
    t.Helper()
    raw, err := os.ReadFile(path)
    if err != nil {
        t.Fatalf("read %s: %v", path, err)
    }
    return string(raw)
}
```

- [ ] **Step 2: Run failing tests**

```bash
cd apps/kittypaw
go test ./server -run TestWeb.*I18n -count=1
```

Expected: FAIL because `i18n.generated.js` is not loaded and `app.js` has no
picker wiring.

- [ ] **Step 3: Load generated runtime**

Modify `index.html` so the scripts appear in this order:

```html
  <script src="/i18n.generated.js"></script>
  <script src="/app.js"></script>
  <script src="/chat.js"></script>
  <script src="/skills.js"></script>
  <script src="/kanban.js"></script>
  <script src="/settings.js"></script>
```

- [ ] **Step 4: Add app-level helper usage**

At the top of `app.js`, add:

```js
const I18n = window.KittyPawI18n;
const t = (key, params) => I18n ? I18n.t(key, params) : key;
```

Add a method:

```js
mountLanguagePicker(target) {
  if (!I18n || !target) return;
  I18n.mountLanguagePicker(target, {
    onChange: async (locale) => {
      document.documentElement.lang = locale;
      try {
        await apiRaw('/api/settings/locale', {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({ locale }),
        });
      } catch (_) {
        // Local storage already preserves the visible choice.
      }
      await this.startCurrentSurface();
    },
  });
}
```

Use it in shells with:

```html
<div class="app-language" id="app-language"></div>
```

and after render:

```js
this.mountLanguagePicker(document.getElementById('app-language'));
```

- [ ] **Step 5: Add picker styles**

Add to `style.css`:

```css
.app-language,
.i18n-language {
  display: flex;
  align-items: center;
  justify-content: flex-end;
  gap: 8px;
}

.i18n-language-button {
  min-width: 40px;
  height: 40px;
  border: 1px solid var(--border);
  background: var(--surface);
  color: var(--text);
  border-radius: 8px;
  font: inherit;
  cursor: pointer;
}

.i18n-language-select {
  min-height: 40px;
  border: 1px solid var(--border);
  border-radius: 8px;
  padding: 0 10px;
  background: var(--surface);
  color: var(--text);
}
```

- [ ] **Step 6: Verify static tests pass**

```bash
cd apps/kittypaw
go test ./server -run TestWeb.*I18n -count=1
```

Expected: PASS.

- [ ] **Step 7: Commit runtime picker**

```bash
git add apps/kittypaw/server/web/index.html apps/kittypaw/server/web/app.js apps/kittypaw/server/web/style.css apps/kittypaw/server/web_i18n_test.go
git commit -m "feat: add local web language picker"
```

## Task 4: Translate Local Web Surfaces

**Files:**

- Modify: `apps/kittypaw/server/web/app.js`
- Modify: `apps/kittypaw/server/web/chat.js`
- Modify: `apps/kittypaw/server/web/settings.js`
- Modify: `apps/kittypaw/server/web/skills.js`
- Modify: `apps/kittypaw/server/web/kanban.js`
- Test: `apps/kittypaw/server/web_i18n_test.go`

- [ ] **Step 1: Extend tests for key usage**

Add one static test that requires key usage in each local web module:

```go
func TestLocalWebModulesUseI18nKeys(t *testing.T) {
    cases := map[string][]string{
        "web/chat.js": {"chat.placeholder", "chat.send", "chat.permissionRequest"},
        "web/settings.js": {"settings.title", "settings.workspaces", "settings.llmProvider"},
        "web/skills.js": {"skills.title", "skills.subtitle", "skills.search"},
        "web/kanban.js": {"kanban.title", "kanban.newTask", "kanban.comments", "kanban.runs"},
    }
    for path, keys := range cases {
        body := readWebAssetForI18nTest(t, path)
        for _, key := range keys {
            if !strings.Contains(body, key) {
                t.Fatalf("%s missing key %s", path, key)
            }
        }
    }
}
```

- [ ] **Step 2: Run failing test**

```bash
cd apps/kittypaw
go test ./server -run TestLocalWebModulesUseI18nKeys -count=1
```

Expected: FAIL because modules still use hard-coded strings.

- [ ] **Step 3: Add module helpers**

Add this helper near the top of each module:

```js
const I18n = window.KittyPawI18n;
const t = (key, params) => I18n ? I18n.t(key, params) : key;
```

If `app.js` already defines a global `t`, avoid duplicate global declarations by
wrapping module code or naming the local helper `tt`.

- [ ] **Step 4: Replace chat strings**

In `chat.js`, replace:

```js
placeholder="Type a message..."
Send
Permission Request
Allow
Deny
Connecting...
Connected
Disconnected
Connection error
Connection lost
Reconnect
```

with `t()` keys from `chat.*`.

- [ ] **Step 5: Replace settings strings**

In `settings.js`, replace headings, labels, and buttons with `settings.*` and
`common.*` keys. Preserve provider names such as `OpenAI`, `Gemini`, and
`Anthropic` as data, not translations.

- [ ] **Step 6: Replace skills strings**

In `skills.js`, replace gallery-owned headings, search placeholder, loading,
installed/available badges, enabled/disabled labels, and action buttons with
`skills.*` and `common.*` keys. Preserve package and skill metadata returned
from APIs.

- [ ] **Step 7: Replace Kanban strings**

In `kanban.js`, replace UI-owned labels, status display labels, empty states,
action button labels, prompt labels, and confirm text with `kanban.*` and
`common.*` keys. Keep internal status keys as `triage`, `todo`, `ready`,
`running`, `blocked`, and `done`.

- [ ] **Step 8: Add missing catalog keys and regenerate**

For any UI string not covered by Task 1 keys, add it to `i18n/catalog.yaml` with
all three locales, then run:

```bash
go run scripts/generate_i18n.go
go run scripts/generate_i18n.go --check
```

Expected: generated file is up to date after the first command.

- [ ] **Step 9: Verify local web tests**

```bash
cd apps/kittypaw
go test ./server -run 'TestWeb.*I18n|TestLocalWebModulesUseI18nKeys|TestKanbanWeb|TestWebChat|TestWebApp' -count=1
```

Expected: PASS.

- [ ] **Step 10: Commit translated local web**

```bash
git add i18n/catalog.yaml apps/kittypaw/server/web/*.js apps/kittypaw/server/web/i18n.generated.js apps/kittypaw/server/web_i18n_test.go
git commit -m "feat: translate local kittypaw web"
```

## Task 5: TASKS Tracking And Verification

**Files:**

- Modify: `TASKS.md`

- [ ] **Step 1: Update task tracker**

Add a section to `TASKS.md`:

```markdown
## Plan: Product-Wide I18n ← 현재

> Spec: `docs/superpowers/specs/2026-05-08-product-i18n-design.md`
> Plan: `docs/superpowers/plans/2026-05-08-product-i18n-foundation.md`
> Branch: `feature/product-i18n`

- [x] **P0**: Create isolated worktree and baseline server/UI tests.
- [ ] **T1**: Central catalog, glossary, schema, generator, generated local web asset.
- [ ] **T2**: Local account locale preference API.
- [ ] **T3**: Local web i18n runtime and globe picker.
- [ ] **T4**: Translate local `kittypaw` App/Chat/Settings/Skills/Kanban UI strings.
- [ ] **T5**: Follow-up plans for Space, legacy Chat, Portal, API error codes, and CLI cleanup.
```

- [ ] **Step 2: Run verification**

```bash
make i18n-check
cd apps/kittypaw && go test ./server -count=1
cd apps/space && go test ./internal/server/... -count=1
cd ../chat && go test ./internal/server/... -count=1
cd ../portal && go test ./... -count=1
```

Expected: all pass. If any pre-existing unrelated failure appears, record the
command and failure in the final implementation note before proceeding.

- [ ] **Step 3: Commit tracker and final verification notes**

```bash
git add TASKS.md
git commit -m "docs: track product i18n rollout"
```

## Self-Review Checklist

- Spec coverage: central catalog, generated assets, local web picker, locale
  persistence, and local web translation are covered here.
- Deferred scope: Space, legacy Chat, Portal, API error codes, and CLI cleanup
  are explicitly deferred to follow-up plans.
- Tests: every task has a command that fails before implementation or verifies
  the implemented behavior.
- Contracts: no public API response shape changes are introduced in this plan.
