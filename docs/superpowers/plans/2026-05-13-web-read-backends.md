# Web Read Backends Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Fix Web.search/Web.fetch contract bugs and add pluggable Web.fetch reader backends.

**Architecture:** Keep `Web.search(query)` as query-based search, not URL fetch. Keep `Http.*` body unwrapping for packages, but return structured JSON for `Web.*`. Move page reading behind `ReadBackend`, with `static` as the default and `firecrawl` available when configured.

**Tech Stack:** Go, existing Kittypaw sandbox resolver, `net/http`, existing `[web]` config and Firecrawl secret wiring.

---

### Task 1: Web.search Validation And Package Contract Bugs

**Files:**
- Modify: `apps/kittypaw/engine/executor.go`
- Test: `apps/kittypaw/engine/executor_http_test.go`

- [x] Add tests proving `Web.search("AI news today")` does not pass through URL host validation when `sandbox.allowed_hosts` is set.
- [x] Add tests proving package resolver returns structured JSON for `Web.search` and `Web.fetch`, while `Http.get` still unwraps `body`.
- [x] Move the `search` branch before URL parsing/validation in `executeHTTP`.
- [x] Apply package response unwrapping only to `Http`, not `Web`.
- [x] Run `go test ./apps/kittypaw/engine -run 'WebSearch|PackageResolver|HTTP' -count=1`.

### Task 2: Web.fetch Error Contract

**Files:**
- Modify: `apps/kittypaw/engine/executor.go`
- Test: `apps/kittypaw/engine/executor_http_test.go`
- Modify if needed: `apps/kittypaw/engine/prompt.go`, `apps/kittypaw/core/skillmeta.go`

- [x] Add tests for `Web.fetch` non-2xx, content type, final URL, empty body, and success output.
- [x] Extend `Web.fetch` JSON with `ok`, `error`, `contentType`, `finalUrl`, `backend`, and optional `warning`.
- [x] Preserve existing `text`, `markdown`, `title`, and `status` fields.
- [x] Update user-facing tool contract text if the returned shape changes.
- [x] Run `go test ./apps/kittypaw/engine -run 'WebFetch|Prompt|SkillMeta' -count=1`.

### Task 3: ReadBackend Interface With Static And Firecrawl

**Files:**
- Create: `apps/kittypaw/engine/read_backend.go`
- Modify: `apps/kittypaw/core/config.go`
- Test: `apps/kittypaw/engine/read_backend_test.go`
- Modify: `apps/kittypaw/engine/executor.go`

- [x] Add `ReadBackend`, `ReadResult`, and `ReadOptions` types.
- [x] Extract current HTTP GET + HTML-to-Markdown logic into `StaticReadBackend`.
- [x] Add `FirecrawlReadBackend` using official `POST /v2/scrape` with `formats: ["markdown", "html"]` and `onlyMainContent: true`.
- [x] Add `[web].read_backend` config with `static | firecrawl | browser | auto`; default `auto` chooses Firecrawl only when static output is weak and a key exists.
- [x] Add a browser snapshot reader backend using `Browser.open` plus `Browser.snapshot`.
- [x] Make `webFetch` call `NewReadBackend(&cfg.Web).Read`.
- [x] Run `go test ./apps/kittypaw/engine -run 'ReadBackend|WebFetch' -count=1`.

### Task 4: Verification And Commit

**Files:**
- Review all changed files.

- [ ] Run `gofmt` on changed Go files.
- [ ] Run `go test ./apps/kittypaw/... -timeout=180s`.
- [ ] Run `git diff --check`.
- [ ] Commit with `feat(kittypaw): add web read backends`.
