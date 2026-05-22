#!/usr/bin/env python3
from html.parser import HTMLParser
import json
from pathlib import Path
import sys
from urllib.parse import urlparse


ROOT = Path(__file__).resolve().parents[1]
HTML_ROOT = ROOT / "html"

REQUIRED_FILES = [
    HTML_ROOT / "index.html",
    HTML_ROOT / "en" / "index.html",
    HTML_ROOT / "ja" / "index.html",
    HTML_ROOT / "cases" / "index.html",
    HTML_ROOT / "cases" / "morning-briefing.html",
    HTML_ROOT / "cases" / "pattern-automation.html",
    HTML_ROOT / "cases" / "site-monitoring.html",
    HTML_ROOT / "cases" / "teach-skill.html",
    HTML_ROOT / "guide" / "index.html",
    HTML_ROOT / "guide" / "quickstart.html",
    HTML_ROOT / "guide" / "skills.html",
    HTML_ROOT / "guide" / "channels.html",
    HTML_ROOT / "guide" / "models.html",
    HTML_ROOT / "guide" / "reflection.html",
    HTML_ROOT / "guide" / "security.html",
    HTML_ROOT / "guide" / "operations.html",
]

REQUIRED_SITEMAP_URLS = [
    "https://kittypaw.app/",
    "https://kittypaw.app/en/",
    "https://kittypaw.app/ja/",
    "https://kittypaw.app/cases/",
    "https://kittypaw.app/cases/morning-briefing.html",
    "https://kittypaw.app/cases/pattern-automation.html",
    "https://kittypaw.app/cases/site-monitoring.html",
    "https://kittypaw.app/cases/teach-skill.html",
    "https://kittypaw.app/guide/",
    "https://kittypaw.app/guide/quickstart.html",
    "https://kittypaw.app/guide/skills.html",
    "https://kittypaw.app/guide/channels.html",
    "https://kittypaw.app/guide/models.html",
    "https://kittypaw.app/guide/reflection.html",
    "https://kittypaw.app/guide/security.html",
    "https://kittypaw.app/guide/operations.html",
    "https://kittypaw.app/llms.txt",
]

REQUIRED_AI_BOTS = [
    "Googlebot",
    "Bingbot",
    "OAI-SearchBot",
    "ChatGPT-User",
    "Claude-SearchBot",
    "Claude-User",
    "PerplexityBot",
]

REQUIRED_LLMS_SECTIONS = [
    "# KittyPaw",
    "## What KittyPaw is",
    "## High-signal facts",
    "## Canonical pages",
    "## AI search and crawler policy",
]


class PageParser(HTMLParser):
    def __init__(self):
        super().__init__()
        self.links = []
        self.title = ""
        self.meta = {}
        self.canonical = ""
        self.json_ld_blocks = []
        self._in_title = False
        self._in_json_ld = False
        self._json_ld_parts = []

    def handle_starttag(self, tag, attrs):
        raw_attrs = attrs
        attrs = dict(attrs)
        if tag == "title":
            self._in_title = True
        if tag == "script" and attrs.get("type") == "application/ld+json":
            self._in_json_ld = True
            self._json_ld_parts = []
        if tag == "link" and attrs.get("rel") == "canonical":
            self.canonical = attrs.get("href", "")
        if tag == "meta":
            key = attrs.get("name") or attrs.get("property")
            if key:
                self.meta[key] = attrs.get("content", "")
        if tag != "a":
            return
        for name, value in raw_attrs:
            if name == "href" and value:
                self.links.append(value)

    def handle_endtag(self, tag):
        if tag == "title":
            self._in_title = False
        if tag == "script" and self._in_json_ld:
            self._in_json_ld = False
            self.json_ld_blocks.append("".join(self._json_ld_parts).strip())
            self._json_ld_parts = []

    def handle_data(self, data):
        if self._in_title:
            self.title += data
        if self._in_json_ld:
            self._json_ld_parts.append(data)


def read(path):
    return path.read_text(encoding="utf-8")


def target_exists(source, href):
    parsed = urlparse(href)
    if parsed.scheme or parsed.netloc:
        return True
    if href.startswith("#") or href.startswith("mailto:") or href.startswith("tel:"):
        return True
    clean = href.split("#", 1)[0]
    if not clean:
        return True
    target = (source.parent / clean).resolve()
    if clean.endswith("/"):
        target = target / "index.html"
    return target.exists()


def main():
    errors = []
    for path in REQUIRED_FILES:
        if not path.exists():
            errors.append(f"Missing required guide file: {path.relative_to(ROOT)}")

    robots_text = read(HTML_ROOT / "robots.txt")
    for bot in REQUIRED_AI_BOTS:
        bot_block = f"User-agent: {bot}"
        if bot_block not in robots_text:
            errors.append(f"robots.txt is missing explicit AI/search crawler block: {bot_block}")
    if "Sitemap: https://kittypaw.app/sitemap.xml" not in robots_text:
        errors.append("robots.txt is missing canonical sitemap URL")
    if "LLM-friendly summary: https://kittypaw.app/llms.txt" not in robots_text:
        errors.append("robots.txt is missing the llms.txt discovery comment")

    llms_path = HTML_ROOT / "llms.txt"
    if not llms_path.exists():
        errors.append("Missing AI-friendly summary: html/llms.txt")
    else:
        llms_text = read(llms_path)
        for section in REQUIRED_LLMS_SECTIONS:
            if section not in llms_text:
                errors.append(f"llms.txt is missing section: {section}")

    home = HTML_ROOT / "index.html"
    home_text = read(home)
    if 'href="guide/"' not in home_text:
        errors.append('Homepage is missing href="guide/" entry point')
    if "어디서 시작할까요?" not in home_text:
        errors.append("Homepage is missing the Korean guide path section title")
    for term in ["KittyPaw는 무엇인가요?", "AI 검색과 크롤러 접근", "llms.txt"]:
        if term not in home_text:
            errors.append(f"Homepage is missing answer-friendly FAQ content: {term}")

    guide_index = HTML_ROOT / "guide" / "index.html"
    if guide_index.exists():
        guide_index_text = read(guide_index)
        if 'href="models.html"' not in guide_index_text:
            errors.append('Guide hub is missing href="models.html"')
        if "guide-flow-diagram" not in guide_index_text:
            errors.append("Guide hub is missing the architecture flow diagram")

    quickstart = HTML_ROOT / "guide" / "quickstart.html"
    if quickstart.exists():
        quickstart_text = read(quickstart)
        if "kittypaw --version" not in quickstart_text:
            errors.append("Quickstart is missing the real version command: kittypaw --version")
        if "kittypaw version" in quickstart_text:
            errors.append("Quickstart still documents unsupported command: kittypaw version")

    models = HTML_ROOT / "guide" / "models.html"
    if models.exists():
        models_text = read(models)
        required_model_terms = [
            "kittypaw model add [id]",
            "kittypaw model list",
            "kittypaw model remove",
            "groq-qwen3-32b",
            "--replace",
            "--force",
            "Add as new",
            "Default",
            "deepseek",
            "cerebras",
            "llamacpp",
            "Enter=keep existing",
        ]
        for term in required_model_terms:
            if term not in models_text:
                errors.append(f"Model guide is missing current model command behavior: {term}")

    sitemap_text = read(HTML_ROOT / "sitemap.xml")
    for url in REQUIRED_SITEMAP_URLS:
        if url not in sitemap_text:
            errors.append(f"Missing sitemap URL: {url}")

    pages_to_check = [path for path in REQUIRED_FILES if path.exists()]
    for page in pages_to_check:
        parser = PageParser()
        page_text = read(page)
        parser.feed(page_text)
        rel_page = page.relative_to(ROOT)
        if not parser.title.strip():
            errors.append(f"{rel_page} is missing <title>")
        if not parser.meta.get("description", "").strip():
            errors.append(f"{rel_page} is missing meta description")
        if not parser.canonical.startswith("https://kittypaw.app/"):
            errors.append(f"{rel_page} is missing canonical kittypaw.app URL")
        for meta_key in ["og:title", "og:description", "og:url", "og:type", "twitter:card", "twitter:title", "twitter:description"]:
            if not parser.meta.get(meta_key, "").strip():
                errors.append(f"{rel_page} is missing {meta_key}")
        if not parser.json_ld_blocks:
            errors.append(f"{rel_page} is missing application/ld+json structured data")
        for block in parser.json_ld_blocks:
            try:
                json.loads(block)
            except json.JSONDecodeError as exc:
                errors.append(f"{rel_page} has invalid JSON-LD: {exc}")
        for href in parser.links:
            if not target_exists(page, href):
                errors.append(f"Broken local link in {rel_page}: {href}")

    if errors:
        for error in errors:
            print(f"ERROR: {error}")
        return 1

    print("Static guide checks passed")
    return 0


if __name__ == "__main__":
    sys.exit(main())
