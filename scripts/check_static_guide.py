#!/usr/bin/env python3
from html.parser import HTMLParser
from pathlib import Path
import sys
from urllib.parse import urlparse


ROOT = Path(__file__).resolve().parents[1]
HTML_ROOT = ROOT / "html"

REQUIRED_FILES = [
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
    "https://kittypaw.app/guide/",
    "https://kittypaw.app/guide/quickstart.html",
    "https://kittypaw.app/guide/skills.html",
    "https://kittypaw.app/guide/channels.html",
    "https://kittypaw.app/guide/models.html",
    "https://kittypaw.app/guide/reflection.html",
    "https://kittypaw.app/guide/security.html",
    "https://kittypaw.app/guide/operations.html",
]


class LinkParser(HTMLParser):
    def __init__(self):
        super().__init__()
        self.links = []

    def handle_starttag(self, tag, attrs):
        if tag != "a":
            return
        for name, value in attrs:
            if name == "href" and value:
                self.links.append(value)


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

    home = HTML_ROOT / "index.html"
    home_text = read(home)
    if 'href="guide/"' not in home_text:
        errors.append('Homepage is missing href="guide/" entry point')
    if "어디서 시작할까요?" not in home_text:
        errors.append("Homepage is missing the Korean guide path section title")

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

    pages_to_check = [home] + [path for path in REQUIRED_FILES if path.exists()]
    for page in pages_to_check:
        parser = LinkParser()
        parser.feed(read(page))
        for href in parser.links:
            if not target_exists(page, href):
                errors.append(f"Broken local link in {page.relative_to(ROOT)}: {href}")

    if errors:
        for error in errors:
            print(f"ERROR: {error}")
        return 1

    print("Static guide checks passed")
    return 0


if __name__ == "__main__":
    sys.exit(main())
