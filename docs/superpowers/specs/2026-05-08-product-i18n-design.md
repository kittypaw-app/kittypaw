# Product-Wide I18n Design

Date: 2026-05-08
Status: Approved for implementation planning by user

## Goal

Add product-wide UI language support for Korean, Japanese, and English across
KittyPaw's user-facing web surfaces while keeping monorepo app boundaries
explicit.

The source of truth for translations will be centralized. Runtime code remains
inside each owning app so `apps/kittypaw`, `apps/space`, `apps/chat`, and
`apps/portal` do not depend on a shared runtime package.

Supported locale codes:

- `ko`: Korean
- `ja`: Japanese
- `en`: English

Default and fallback locale: `en`.

## Non-Goals

- Do not translate logs, internal metrics, trace labels, or developer-facing
  errors.
- Do not change public API response contracts in the first phase.
- Do not introduce a shared runtime package imported by multiple apps.
- Do not translate user-authored content, model output, task titles, comments,
  package descriptions, provider names, or workspace paths.
- Do not support arbitrary locales beyond `ko`, `ja`, and `en` in this phase.

## Current State

User-facing strings are scattered:

- `apps/kittypaw/server/web`: local web shell, chat, settings, skills, and
  Kanban.
- `apps/space/internal/server/web`: hosted Space chat and Kanban pages.
- `apps/chat/internal/server/web` and `apps/chat/internal/server/manual`:
  legacy hosted chat and manual QA UI.
- `apps/portal/cmd/server/web.go`, `apps/portal/internal/auth/pages.go`,
  `apps/portal/internal/connect/handler.go`, and
  `apps/portal/internal/connectadmin/handler.go`: server-rendered Portal,
  Connect, auth, and admin pages.
- `apps/kittyapi` and `apps/kakao` are mostly API/relay services; their
  user-facing scope is limited to public error text and external webhook
  responses.

Existing language-related foundations:

- `apps/kittypaw/core.UserConfig.Locale`.
- Engine locale injection for package calls.
- Store-backed user context keys such as `pref.lang`.
- Some CLI branches already use `detectLang()` for Korean/Japanese/English
  messages.

There is no common web i18n layer, catalog schema, generated assets, or missing
translation check.

## Architecture

Translations are centrally authored and generated into app-owned runtime files.

```text
i18n/
  catalog.yaml
  glossary.yaml
  schema.json
scripts/
  generate_i18n.go
apps/kittypaw/server/web/i18n.generated.js
apps/space/internal/server/web/i18n.generated.js
apps/chat/internal/server/web/i18n.generated.js
apps/chat/internal/server/manual/i18n.generated.js
apps/portal/internal/i18n/generated.go
```

`i18n/catalog.yaml` is the only human-edited source for UI translation strings.
Generated files are committed because the app binaries embed app-local assets.

Each app owns a thin runtime helper:

- Web apps expose `window.KittyPawI18n` or an app-specific equivalent with
  `t(key, params?)`, `getLocale()`, `setLocale(locale)`, and
  `onLocaleChange(callback)`.
- Portal Go pages use a small `internal/i18n` helper generated from the same
  catalog.

This keeps the translation source centralized without creating an implicit
shared runtime dependency between apps.

## Catalog Format

The catalog is key-based, not source-string-based.

```yaml
locales: [ko, ja, en]
default: en

keys:
  common.language:
    ko: 언어
    ja: 言語
    en: Language

  common.refresh:
    ko: 새로고침
    ja: 更新
    en: Refresh

  kanban.title:
    ko: 칸반
    ja: カンバン
    en: Kanban
```

Rules:

- Every key must have `ko`, `ja`, and `en`.
- Keys use dotted namespaces: `common.*`, `chat.*`, `kanban.*`,
  `settings.*`, `skills.*`, `space.*`, `portal.*`, `auth.*`, `connect.*`,
  and `errors.*`.
- Interpolation uses named placeholders such as `{count}` and `{name}`.
- Placeholder sets must match across all locales.
- English is the fallback when runtime locale selection is missing or invalid.

`i18n/glossary.yaml` records product terms and approved translations. Examples:

- Daemon
- Workspace
- Route
- Kanban
- Milestone
- Claim
- Reclaim
- Heartbeat
- Hosted chat

## Locale Selection

Every browser UI gets a consistent language picker:

- Placement: upper-right of the app header or toolbar.
- Icon: globe-style button.
- Options: `한국어`, `日本語`, `English`.
- The selected option updates visible UI without requiring a full browser
  restart.

Persistence:

- Local `kittypaw` web stores the locale in `localStorage` immediately and
  also persists it to account preference when an authenticated account is
  available. The account preference write targets the existing user context
  preference key first. The web foundation phase does not rewrite
  `config.toml`; the CLI cleanup phase owns any later config-file sync.
- Hosted `space` and `chat` store the locale in `localStorage` and a `kp_lang`
  cookie.
- Portal reads `ui_lang` query first, then `kp_lang` cookie, then
  `Accept-Language`, then falls back to `en`.
- OAuth/login redirects preserve the selected language using
  `ui_lang=ko|ja|en` where safe.

The HTML `lang` attribute matches the selected locale.

## Translation Scope

Translate:

- Page titles, headings, labels, buttons, placeholders, empty states, tooltips,
  status text, confirm/prompt strings, and UI-owned error wrappers.
- Browser-only route status messages such as "Loading routes" and
  "No daemon online".
- Portal landing/auth/connect/admin page copy.

Do not translate:

- User-generated task titles, chat messages, comments, paths, provider names,
  model names, package names, package descriptions, logs, or metrics.
- API error payloads in the first phase, except when the UI maps a stable error
  code to a localized display string.
- OpenAI-compatible relay payloads.
- Kakao OpenBuilder protocol fields.

## API Error Strategy

The first phase leaves server error text unchanged. Browser UIs may translate
client-side wrapper text, but raw server messages remain fallback text.

Future API work moves user-facing errors to stable codes:

```json
{
  "error": "title is required",
  "code": "kanban.title_required"
}
```

The UI can then translate `code` and fall back to `error` when a code is missing
or unknown. Contracted endpoints must add codes in a backward-compatible way.

## App Rollout

Phase 1: Central catalog and generator

- Add `i18n/catalog.yaml`, `i18n/glossary.yaml`, and `i18n/schema.json`.
- Add `scripts/generate_i18n.go`.
- Generate app-local assets without changing runtime behavior.
- Add tests that fail on missing locale entries or placeholder mismatch.

Phase 2: Local `apps/kittypaw` web

- Add the language picker to the local web shell and standalone `/chat`,
  `/kanban`, and `/_settings` surfaces.
- Translate `App`, `Chat`, `Settings`, `Skills`, and `Kanban` UI strings.
- Persist locale locally and in account preference for authenticated local web
  sessions.

Phase 3: Hosted `apps/space` and legacy `apps/chat`

- Add language picker to Space `/chat` and `/kanban`.
- Translate legacy hosted chat entry/app/manual UI.
- Preserve selected language across login redirects.

Phase 4: `apps/portal`

- Add generated Go i18n helper.
- Translate Portal home, Connect home, auth success/error pages, and connect
  admin pages.
- Read `ui_lang`, `kp_lang`, and `Accept-Language`.

Phase 5: Error code hardening

- Add stable error codes to selected local/hosted APIs in a backward-compatible
  shape.
- Translate UI display from error codes.
- Keep public API contract changes behind tests and contract fixtures.

Phase 6: CLI cleanup

- Consolidate existing `detectLang()` branches into a small CLI helper.
- Make `user.locale` / `pref.lang` behavior explicit in setup/config flows.

## Files By Phase

Create:

- `i18n/catalog.yaml`
- `i18n/glossary.yaml`
- `i18n/schema.json`
- `scripts/generate_i18n.go`
- `apps/kittypaw/server/web/i18n.generated.js`
- `apps/space/internal/server/web/i18n.generated.js`
- `apps/chat/internal/server/web/i18n.generated.js`
- `apps/chat/internal/server/manual/i18n.generated.js`
- `apps/portal/internal/i18n/generated.go`

Modify incrementally:

- `apps/kittypaw/server/web/index.html`
- `apps/kittypaw/server/web/app.js`
- `apps/kittypaw/server/web/chat.js`
- `apps/kittypaw/server/web/settings.js`
- `apps/kittypaw/server/web/skills.js`
- `apps/kittypaw/server/web/kanban.js`
- `apps/space/internal/server/web/*.html`
- `apps/space/internal/server/web/*.js`
- `apps/chat/internal/server/web/*.html`
- `apps/chat/internal/server/web/*.js`
- `apps/chat/internal/server/manual/*`
- Portal Go-rendered page files under `apps/portal`.

## Testing

Catalog tests:

- Catalog parses.
- Supported locale set is exactly `ko`, `ja`, `en`.
- Every key has every locale.
- Placeholder names match across locales.
- Glossary-required terms are present where applicable.

Generated asset tests:

- Generated files are up to date with the catalog.
- App-specific generated assets include only the namespaces that app needs.

Runtime tests:

- Language picker renders.
- Invalid locale falls back to English.
- Selection persists to local storage/cookie.
- HTML `lang` changes when locale changes.
- Key UI surfaces show Korean, Japanese, and English text.

Suggested verification commands by phase:

```bash
go test ./...
cd apps/kittypaw && go test ./server -count=1
cd apps/space && go test ./internal/server/... -count=1
cd apps/chat && go test ./internal/server/... -count=1
cd apps/portal && go test ./... -count=1
```

## Risks

- String extraction can create churn in large vanilla JS files. Mitigation:
  convert one surface at a time.
- Centralized keys can become noisy. Mitigation: keep namespaces app-oriented
  and enforce key ownership in tests.
- Portal server-rendered HTML and browser JS will need different runtime
  helpers. Mitigation: generate both from the same catalog.
- API error translation can break clients if done by replacing existing error
  strings. Mitigation: add error codes while preserving existing `error`.
- `space` and `chat` contain similar but not identical hosted chat code.
  Mitigation: share catalog keys, not runtime code, during this rollout.

## Resolved Decisions

- Local `kittypaw` language selection stores to `localStorage` and account
  preference when authenticated. It does not rewrite `config.toml` during the
  web foundation phase.
- Generated JS assets use app-specific namespace subsets rather than embedding
  the entire catalog in every app.
- The globe picker appears on login and setup-required screens as well as the
  authenticated shells, so users can recover from an unwanted language before
  signing in.
