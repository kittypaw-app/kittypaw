package engine

import (
	"strconv"
	"strings"
)

// normalizeGeneratedCode absorbs common LLM formatting drift before the
// sandbox sees it. Valid-looking JavaScript stays untouched; plain prose is
// converted into a JS return value so harmless acknowledgements do not burn the
// retry budget.
func normalizeGeneratedCode(raw string) string {
	code := strings.TrimSpace(stripFences(raw))
	if code == "" || looksLikeGeneratedJavaScript(code) {
		return code
	}
	return "return " + strconv.Quote(code) + ";"
}

func looksLikeGeneratedJavaScript(code string) bool {
	tokens := []string{
		"return ", "const ", "let ", "var ",
		"if (", "if(", "for (", "for(", "while (", "while(",
		"try {", "catch (", "function ", "=>",
		"Skill.", "Web.", "Llm.", "Code.", "Http.", "Telegram.",
		"Memory.", "Storage.", "Runner.", "Staff.", "Share.", "Projects.",
		"JSON.", "Math.", "Date(", "new ",
	}
	for _, token := range tokens {
		if strings.Contains(code, token) {
			return true
		}
	}

	trimmed := strings.TrimSpace(code)
	return strings.HasPrefix(trimmed, "{") ||
		strings.HasPrefix(trimmed, "[") ||
		strings.HasPrefix(trimmed, "(") ||
		strings.HasPrefix(trimmed, "\"") ||
		strings.HasPrefix(trimmed, "'") ||
		strings.HasPrefix(trimmed, "`")
}
