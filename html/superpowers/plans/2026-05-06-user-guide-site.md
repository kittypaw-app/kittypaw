# KittyPaw User Guide Site Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a Hermes-style user guide surface for KittyPaw without making the homepage heavy.

**Architecture:** Keep the homepage as a lightweight product overview and add a bottom "where to start" section that links into a dedicated guide hub. The guide hub and pages are static HTML under `html/guide/`, sharing the existing `html/style.css` and `html/main.js`.

**Tech Stack:** Static HTML, shared CSS, existing IntersectionObserver reveal script, no new JavaScript dependencies.

---

### Task 1: Add Static Guide Checks

**Files:**
- Create: `scripts/check_static_guide.py`

- [ ] **Step 1: Add a checker that fails until guide pages and links exist**

Create `scripts/check_static_guide.py` with checks for:
- guide pages under `html/guide/`
- homepage guide section link to `guide/`
- sitemap entries for public guide pages
- no broken same-site `.html` or directory links from the checked pages

- [ ] **Step 2: Run checker to verify it fails**

Run: `python3 scripts/check_static_guide.py`

Expected: FAIL because `html/guide/index.html` does not exist yet.

### Task 2: Add Homepage Entry Point

**Files:**
- Modify: `html/index.html`
- Modify: `html/style.css`

- [ ] **Step 1: Add "어디서 시작할까요?" below the tech section**

Add a four-card path section:
- 10분 안에 실행
- 매일 아침 브리핑
- 반복 요청 자동화
- 안전하게 운영

- [ ] **Step 2: Style the section**

Add `.guide-path-*` CSS classes near the existing CTA/footer styles and responsive handling for mobile.

### Task 3: Add Guide Hub And Core Pages

**Files:**
- Create: `html/guide/index.html`
- Create: `html/guide/quickstart.html`
- Create: `html/guide/skills.html`
- Create: `html/guide/channels.html`
- Create: `html/guide/models.html`
- Create: `html/guide/reflection.html`
- Create: `html/guide/security.html`
- Create: `html/guide/operations.html`
- Modify: `html/style.css`

- [ ] **Step 1: Create the guide hub**

The hub should include:
- guide hero
- quick start command
- pick-your-path cards
- guide map cards
- architecture/data flow summary

- [ ] **Step 2: Create detail pages**

Each detail page should be concise but substantial enough to stand alone:
- prerequisites
- commands or setup flow
- what to verify
- status/limits
- next recommended page

Add a model/provider guide for the named model workflow:
- default setup model versus extra named models
- `kittypaw model add`
- `/model <id>` in chat
- cloud/local provider choice
- connection checks and secret handling

- [ ] **Step 2a: Add architecture diagram to the guide hub**

Add a static HTML/CSS data-flow diagram to `html/guide/index.html` that shows user surfaces, local server/account config, model routing, skill sandbox, local SQLite, and channel delivery.

- [ ] **Step 3: Add shared guide page CSS**

Use existing design tokens. Avoid nested cards and avoid adding new JS.

### Task 4: Wire Navigation And Sitemap

**Files:**
- Modify: `html/index.html`
- Modify: `html/sitemap.xml`

- [ ] **Step 1: Add guide link to Korean homepage nav**

Keep EN/JA untouched for this iteration because the new guide content is Korean.

- [ ] **Step 2: Add sitemap URLs for guide pages**

Add public guide pages under `https://kittypaw.app/guide/`.

### Task 5: Verify

**Files:**
- Use: `scripts/check_static_guide.py`

- [ ] **Step 1: Run static guide checker**

Run: `python3 scripts/check_static_guide.py`

Expected: PASS.

- [ ] **Step 2: Inspect changed files**

Run: `git diff -- html/index.html html/style.css html/sitemap.xml html/guide scripts/check_static_guide.py`

Expected: changes are scoped to the guide feature.
