package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

const (
	catalogPath = "i18n/catalog.yaml"
	outputPath  = "apps/kittypaw/server/web/i18n.generated.js"
)

var requiredLocales = []string{"ko", "ja", "en"}

type catalog struct {
	Locales       []string
	DefaultLocale string
	Messages      map[string]map[string]string
}

func main() {
	check := flag.Bool("check", false, "verify generated i18n asset is up to date")
	flag.Parse()

	if err := run(*check); err != nil {
		fmt.Fprintf(os.Stderr, "i18n: %v\n", err)
		os.Exit(1)
	}
}

func run(check bool) error {
	data, err := os.ReadFile(catalogPath)
	if err != nil {
		return err
	}

	cat, err := parseCatalog(data)
	if err != nil {
		return err
	}
	if err := validateCatalog(cat); err != nil {
		return err
	}

	generated, err := generateJS(cat)
	if err != nil {
		return err
	}

	if check {
		existing, err := os.ReadFile(outputPath)
		if err != nil {
			if os.IsNotExist(err) {
				return fmt.Errorf("%s is missing; run go run scripts/generate_i18n.go", outputPath)
			}
			return err
		}
		if !bytes.Equal(existing, generated) {
			return fmt.Errorf("%s is stale; run go run scripts/generate_i18n.go", outputPath)
		}
		return nil
	}

	if err := os.MkdirAll(filepath.Dir(outputPath), 0o755); err != nil {
		return err
	}
	return os.WriteFile(outputPath, generated, 0o644)
}

func parseCatalog(data []byte) (catalog, error) {
	cat := catalog{Messages: make(map[string]map[string]string)}
	section := ""
	currentMessage := ""
	lines := strings.Split(string(data), "\n")

	for i, raw := range lines {
		lineNo := i + 1
		line := strings.TrimRight(raw, "\r")
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		if strings.Contains(line, "\t") {
			return cat, fmt.Errorf("%s:%d: tabs are not supported", catalogPath, lineNo)
		}

		indent := leadingSpaces(line)
		switch indent {
		case 0:
			currentMessage = ""
			switch {
			case trimmed == "locales:":
				section = "locales"
			case trimmed == "messages:":
				section = "messages"
			case strings.HasPrefix(trimmed, "default_locale:"):
				value, err := parseKeyValue(trimmed, lineNo)
				if err != nil {
					return cat, err
				}
				cat.DefaultLocale = value
				section = ""
			default:
				return cat, fmt.Errorf("%s:%d: unsupported top-level field %q", catalogPath, lineNo, trimmed)
			}
		case 2:
			switch section {
			case "locales":
				if !strings.HasPrefix(trimmed, "- ") {
					return cat, fmt.Errorf("%s:%d: expected locale list item", catalogPath, lineNo)
				}
				value, err := parseScalar(strings.TrimSpace(strings.TrimPrefix(trimmed, "- ")), lineNo)
				if err != nil {
					return cat, err
				}
				cat.Locales = append(cat.Locales, value)
			case "messages":
				if !strings.HasSuffix(trimmed, ":") {
					return cat, fmt.Errorf("%s:%d: expected message key", catalogPath, lineNo)
				}
				key := strings.TrimSuffix(trimmed, ":")
				if key == "" {
					return cat, fmt.Errorf("%s:%d: empty message key", catalogPath, lineNo)
				}
				if _, exists := cat.Messages[key]; exists {
					return cat, fmt.Errorf("%s:%d: duplicate message key %q", catalogPath, lineNo, key)
				}
				cat.Messages[key] = make(map[string]string)
				currentMessage = key
			default:
				return cat, fmt.Errorf("%s:%d: unexpected indented field outside a section", catalogPath, lineNo)
			}
		case 4:
			if section != "messages" || currentMessage == "" {
				return cat, fmt.Errorf("%s:%d: locale translation must be under a message key", catalogPath, lineNo)
			}
			locale, value, err := parseLocaleValue(trimmed, lineNo)
			if err != nil {
				return cat, err
			}
			if _, exists := cat.Messages[currentMessage][locale]; exists {
				return cat, fmt.Errorf("%s:%d: duplicate locale %q for message %q", catalogPath, lineNo, locale, currentMessage)
			}
			cat.Messages[currentMessage][locale] = value
		default:
			return cat, fmt.Errorf("%s:%d: unsupported indentation", catalogPath, lineNo)
		}
	}

	return cat, nil
}

func leadingSpaces(s string) int {
	count := 0
	for count < len(s) && s[count] == ' ' {
		count++
	}
	return count
}

func parseKeyValue(trimmed string, lineNo int) (string, error) {
	parts := strings.SplitN(trimmed, ":", 2)
	if len(parts) != 2 {
		return "", fmt.Errorf("%s:%d: expected key-value pair", catalogPath, lineNo)
	}
	return parseScalar(parts[1], lineNo)
}

func parseLocaleValue(trimmed string, lineNo int) (string, string, error) {
	parts := strings.SplitN(trimmed, ":", 2)
	if len(parts) != 2 {
		return "", "", fmt.Errorf("%s:%d: expected locale translation", catalogPath, lineNo)
	}
	locale := strings.TrimSpace(parts[0])
	if locale == "" {
		return "", "", fmt.Errorf("%s:%d: empty locale key", catalogPath, lineNo)
	}
	value, err := parseScalar(parts[1], lineNo)
	if err != nil {
		return "", "", err
	}
	return locale, value, nil
}

func parseScalar(value string, lineNo int) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", nil
	}
	if strings.HasPrefix(value, "\"") {
		if !strings.HasSuffix(value, "\"") || len(value) == 1 {
			return "", fmt.Errorf("%s:%d: unterminated double-quoted scalar", catalogPath, lineNo)
		}
		unquoted, err := strconv.Unquote(value)
		if err != nil {
			return "", fmt.Errorf("%s:%d: invalid quoted scalar: %w", catalogPath, lineNo, err)
		}
		return unquoted, nil
	}
	if strings.HasPrefix(value, "'") {
		if !strings.HasSuffix(value, "'") || len(value) == 1 {
			return "", fmt.Errorf("%s:%d: unterminated single-quoted scalar", catalogPath, lineNo)
		}
		return strings.ReplaceAll(value[1:len(value)-1], "''", "'"), nil
	}
	return value, nil
}

func validateCatalog(cat catalog) error {
	if len(cat.Locales) != len(requiredLocales) {
		return fmt.Errorf("catalog locales must be exactly %s", strings.Join(requiredLocales, ", "))
	}

	seenLocales := make(map[string]bool, len(cat.Locales))
	for _, locale := range cat.Locales {
		if seenLocales[locale] {
			return fmt.Errorf("duplicate locale %q", locale)
		}
		seenLocales[locale] = true
	}
	for _, locale := range requiredLocales {
		if !seenLocales[locale] {
			return fmt.Errorf("catalog locales must include %q", locale)
		}
	}

	if cat.DefaultLocale != "en" {
		return fmt.Errorf("default_locale must be en")
	}
	if !seenLocales[cat.DefaultLocale] {
		return fmt.Errorf("default_locale %q is not in locales", cat.DefaultLocale)
	}
	if len(cat.Messages) == 0 {
		return fmt.Errorf("messages must not be empty")
	}

	for key, translations := range cat.Messages {
		if key == "" {
			return fmt.Errorf("message key must not be empty")
		}
		if len(translations) != len(requiredLocales) {
			return fmt.Errorf("message %q must have translations for %s", key, strings.Join(requiredLocales, ", "))
		}
		for locale := range translations {
			if !seenLocales[locale] {
				return fmt.Errorf("message %q has unsupported locale %q", key, locale)
			}
		}
		for _, locale := range requiredLocales {
			if _, ok := translations[locale]; !ok {
				return fmt.Errorf("message %q is missing locale %q", key, locale)
			}
		}
		if err := validatePlaceholders(key, translations); err != nil {
			return err
		}
	}

	return nil
}

func validatePlaceholders(key string, translations map[string]string) error {
	var expected []string
	expectedLocale := ""
	for _, locale := range requiredLocales {
		names, err := placeholderNames(translations[locale])
		if err != nil {
			return fmt.Errorf("message %q locale %q: %w", key, locale, err)
		}
		if expected == nil {
			expected = names
			expectedLocale = locale
			continue
		}
		if strings.Join(expected, "\x00") != strings.Join(names, "\x00") {
			return fmt.Errorf(
				"message %q placeholder mismatch: %s has {%s}, %s has {%s}",
				key,
				expectedLocale,
				strings.Join(expected, ", "),
				locale,
				strings.Join(names, ", "),
			)
		}
	}
	return nil
}

func placeholderNames(s string) ([]string, error) {
	names := make(map[string]bool)
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '{':
			end := strings.IndexByte(s[i+1:], '}')
			if end < 0 {
				return nil, fmt.Errorf("unclosed placeholder")
			}
			end += i + 1
			name := s[i+1 : end]
			if !validPlaceholderName(name) {
				return nil, fmt.Errorf("invalid placeholder %q", "{"+name+"}")
			}
			names[name] = true
			i = end
		case '}':
			return nil, fmt.Errorf("unmatched closing brace")
		}
	}

	out := make([]string, 0, len(names))
	for name := range names {
		out = append(out, name)
	}
	sort.Strings(out)
	return out, nil
}

func validPlaceholderName(name string) bool {
	if name == "" {
		return false
	}
	for i := 0; i < len(name); i++ {
		c := name[i]
		if i == 0 {
			if (c < 'A' || c > 'Z') && (c < 'a' || c > 'z') && c != '_' {
				return false
			}
			continue
		}
		if (c < 'A' || c > 'Z') && (c < 'a' || c > 'z') && (c < '0' || c > '9') && c != '_' {
			return false
		}
	}
	return true
}

func generateJS(cat catalog) ([]byte, error) {
	var b bytes.Buffer
	keys := make([]string, 0, len(cat.Messages))
	for key := range cat.Messages {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	b.WriteString("// Code generated by scripts/generate_i18n.go; DO NOT EDIT.\n")
	b.WriteString("(function () {\n")
	b.WriteString("  \"use strict\";\n\n")
	b.WriteString("  const storageKey = \"kp_lang\";\n")
	b.WriteString("  const supportedLocales = [")
	for i, locale := range cat.Locales {
		if i > 0 {
			b.WriteString(", ")
		}
		b.WriteString(jsString(locale))
	}
	b.WriteString("];\n")
	fmt.Fprintf(&b, "  const defaultLocale = %s;\n", jsString(cat.DefaultLocale))
	b.WriteString("  const messages = {\n")
	for i, key := range keys {
		fmt.Fprintf(&b, "    %s: {\n", jsString(key))
		for j, locale := range cat.Locales {
			fmt.Fprintf(&b, "      %s: %s", jsString(locale), jsString(cat.Messages[key][locale]))
			if j < len(cat.Locales)-1 {
				b.WriteString(",")
			}
			b.WriteString("\n")
		}
		b.WriteString("    }")
		if i < len(keys)-1 {
			b.WriteString(",")
		}
		b.WriteString("\n")
	}
	b.WriteString("  };\n")
	b.WriteString("  const languageNames = {\n")
	b.WriteString("    \"ko\": \"한국어\",\n")
	b.WriteString("    \"ja\": \"日本語\",\n")
	b.WriteString("    \"en\": \"English\"\n")
	b.WriteString("  };\n\n")
	b.WriteString(jsRuntime)
	return b.Bytes(), nil
}

func jsString(s string) string {
	data, err := json.Marshal(s)
	if err != nil {
		panic(err)
	}
	return string(data)
}

const jsRuntime = `  function readStoredLocale() {
    try {
      return window.localStorage ? window.localStorage.getItem(storageKey) : "";
    } catch (err) {
      return "";
    }
  }

  function writeStoredLocale(locale) {
    try {
      if (window.localStorage) {
        window.localStorage.setItem(storageKey, locale);
      }
    } catch (err) {
      return;
    }
  }

  function browserLocale() {
    const nav = window.navigator || {};
    if (Array.isArray(nav.languages) && nav.languages.length > 0) {
      return nav.languages[0];
    }
    return nav.language || "";
  }

  function normalizeLocale(locale) {
    if (!locale) {
      return defaultLocale;
    }
    const normalized = String(locale).toLowerCase().replace(/_/g, "-");
    if (supportedLocales.indexOf(normalized) !== -1) {
      return normalized;
    }
    const base = normalized.split("-")[0];
    if (supportedLocales.indexOf(base) !== -1) {
      return base;
    }
    return defaultLocale;
  }

  let currentLocale = normalizeLocale(readStoredLocale() || browserLocale() || defaultLocale);
  if (window.document && window.document.documentElement) {
    window.document.documentElement.lang = currentLocale;
  }

  function getLocale() {
    return currentLocale || defaultLocale;
  }

  function localeEvent(locale) {
    if (typeof window.CustomEvent === "function") {
      return new window.CustomEvent("kittypaw:localechange", {
        detail: { locale: locale }
      });
    }
    const event = window.document.createEvent("CustomEvent");
    event.initCustomEvent("kittypaw:localechange", false, false, { locale: locale });
    return event;
  }

  function setLocale(locale) {
    const nextLocale = normalizeLocale(locale);
    currentLocale = nextLocale;
    writeStoredLocale(nextLocale);
    if (window.document && window.document.documentElement) {
      window.document.documentElement.lang = nextLocale;
    }
    window.dispatchEvent(localeEvent(nextLocale));
    return nextLocale;
  }

  function t(key, params) {
    const entry = messages[key];
    if (!entry) {
      return key;
    }
    let template = key;
    const locale = getLocale();
    if (Object.prototype.hasOwnProperty.call(entry, locale)) {
      template = entry[locale];
    } else if (Object.prototype.hasOwnProperty.call(entry, defaultLocale)) {
      template = entry[defaultLocale];
    }
    if (!params) {
      return template;
    }
    return template.replace(/\{([A-Za-z_][A-Za-z0-9_]*)\}/g, function (match, name) {
      if (Object.prototype.hasOwnProperty.call(params, name)) {
        return String(params[name]);
      }
      return match;
    });
  }

  function applyTranslations(root) {
    const scope = root || window.document;
    if (!scope) {
      return scope;
    }

    const selector = "[data-i18n], [data-i18n-placeholder], [data-i18n-title], [data-i18n-aria-label]";
    const elements = [];
    if (scope.nodeType === 1 && typeof scope.matches === "function" && scope.matches(selector)) {
      elements.push(scope);
    }
    if (typeof scope.querySelectorAll === "function") {
      const matches = scope.querySelectorAll(selector);
      for (let i = 0; i < matches.length; i++) {
        elements.push(matches[i]);
      }
    }

    for (let i = 0; i < elements.length; i++) {
      const element = elements[i];
      const textKey = element.getAttribute("data-i18n");
      const placeholderKey = element.getAttribute("data-i18n-placeholder");
      const titleKey = element.getAttribute("data-i18n-title");
      const ariaLabelKey = element.getAttribute("data-i18n-aria-label");

      if (textKey) {
        element.textContent = t(textKey);
      }
      if (placeholderKey) {
        element.setAttribute("placeholder", t(placeholderKey));
      }
      if (titleKey) {
        element.setAttribute("title", t(titleKey));
      }
      if (ariaLabelKey) {
        element.setAttribute("aria-label", t(ariaLabelKey));
      }
    }

    return scope;
  }

  function mountLanguagePicker(container, options) {
    const target = typeof container === "string" ? window.document.querySelector(container) : container;
    if (!target) {
      return null;
    }

    const opts = options || {};
    const label = window.document.createElement("label");
    label.className = opts.className || "kp-language-picker";
    label.style.display = "inline-flex";
    label.style.alignItems = "center";
    label.style.gap = "0.375rem";
    label.style.whiteSpace = "nowrap";

    const globe = window.document.createElement("span");
    globe.className = "kp-language-picker__globe";
    globe.setAttribute("role", "img");
    globe.textContent = "\uD83C\uDF10";
    globe.style.lineHeight = "1";

    const select = window.document.createElement("select");
    select.className = "kp-language-picker__select";
    select.value = getLocale();
    select.style.font = "inherit";
    select.style.maxWidth = "10rem";
    if (opts.id) {
      select.id = opts.id;
    }

    for (let i = 0; i < supportedLocales.length; i++) {
      const locale = supportedLocales[i];
      const option = window.document.createElement("option");
      option.value = locale;
      option.textContent = languageNames[locale] || locale;
      select.appendChild(option);
    }

    function syncLabels() {
      const labelText = t("common.language");
      label.setAttribute("aria-label", labelText);
      globe.setAttribute("aria-label", labelText);
      globe.title = labelText;
      select.setAttribute("aria-label", labelText);
      select.title = labelText;
      select.value = getLocale();
    }

    function onChange() {
      const locale = setLocale(select.value);
      applyTranslations(window.document);
      if (typeof opts.onChange === "function") {
        opts.onChange(locale);
      }
    }

    select.addEventListener("change", onChange);
    window.addEventListener("kittypaw:localechange", syncLabels);
    syncLabels();

    label.appendChild(globe);
    label.appendChild(select);
    target.textContent = "";
    target.appendChild(label);

    return {
      element: label,
      select: select,
      destroy: function () {
        select.removeEventListener("change", onChange);
        window.removeEventListener("kittypaw:localechange", syncLabels);
        if (label.parentNode) {
          label.parentNode.removeChild(label);
        }
      }
    };
  }

  window.KittyPawI18n = {
    supportedLocales: supportedLocales.slice(),
    defaultLocale: defaultLocale,
    messages: messages,
    normalizeLocale: normalizeLocale,
    getLocale: getLocale,
    setLocale: setLocale,
    t: t,
    applyTranslations: applyTranslations,
    mountLanguagePicker: mountLanguagePicker
  };
})();
`
