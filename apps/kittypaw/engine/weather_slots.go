package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/jinto/kittypaw/core"
)

var (
	kittypawAPIBaseURL = "https://api.kittypaw.app"
	geoHTTPClient      = &http.Client{Timeout: 5 * time.Second}
)

type weatherNowSlots struct {
	LocationQuery string `json:"location_query"`
}

type structuredLocation struct {
	Label string
	Lat   float64
	Lon   float64
}

func extractWeatherNowSlots(ctx context.Context, sess *AccountRuntime, userText string) (weatherNowSlots, error) {
	var slots weatherNowSlots
	userText = strings.TrimSpace(userText)
	if sess == nil || sess.Provider == nil || userText == "" {
		return slots, nil
	}
	prompt := buildWeatherSlotPrompt(userText)
	resp, err := sess.Provider.Generate(WithLLMCallKind(ctx, "weather.slots"), buildSubLLMMessages(prompt))
	if err != nil || resp == nil {
		return slots, err
	}
	raw := strings.TrimSpace(stripFences(resp.Content))
	if raw == "" {
		return slots, nil
	}
	slots, err = parseWeatherNowSlots(raw)
	if err != nil {
		return weatherNowSlots{}, err
	}
	if slots.LocationQuery == "" {
		slots.LocationQuery = inferWeatherLocationFromText(userText)
	}
	slots.LocationQuery = normalizeLocationSlot(slots.LocationQuery)
	return slots, nil
}

func parseWeatherNowSlots(raw string) (weatherNowSlots, error) {
	var slots weatherNowSlots
	raw = strings.TrimSpace(stripFences(raw))
	if raw == "" {
		return slots, nil
	}
	jsonRaw := raw
	if start := strings.Index(jsonRaw, "{"); start >= 0 {
		if end := strings.LastIndex(jsonRaw, "}"); end >= start {
			jsonRaw = jsonRaw[start : end+1]
		}
	}
	if strings.HasPrefix(strings.TrimSpace(jsonRaw), "{") {
		if err := json.Unmarshal([]byte(jsonRaw), &slots); err != nil {
			return weatherNowSlots{}, err
		}
		slots.LocationQuery = normalizeLocationSlot(slots.LocationQuery)
		return slots, nil
	}
	loc := normalizeLocationSlot(raw)
	if !looksLikePlainLocationSlot(loc) {
		return slots, nil
	}
	return weatherNowSlots{LocationQuery: loc}, nil
}

func normalizeLocationSlot(query string) string {
	query = strings.TrimSpace(query)
	query = strings.Trim(query, " \t\r\n.。?？!！,，")
	query = compactKoreanAdministrativeSpace(query)
	if query == "" {
		return ""
	}
	suffixes := []string{"이에요", "예요", "이요", "요", "에서", "으로", "로", "은", "는", "이", "가", "을", "를", "에"}
	for {
		changed := false
		for _, suffix := range suffixes {
			if runeCount(query) <= 2 || !strings.HasSuffix(query, suffix) {
				continue
			}
			query = strings.TrimSpace(strings.TrimSuffix(query, suffix))
			query = strings.Trim(query, " \t\r\n.。?？!！,，")
			changed = true
			break
		}
		if !changed {
			break
		}
	}
	return compactKoreanAdministrativeSpace(query)
}

func compactKoreanAdministrativeSpace(query string) string {
	compact := strings.ReplaceAll(query, " ", "")
	if compact == query || compact == "" {
		return query
	}
	if !isHangulOrSpace(query) {
		return query
	}
	for _, suffix := range []string{"역", "동", "구", "시", "군", "읍", "면", "리", "공항", "터미널", "시장", "사거리", "광장", "공원", "해변", "산"} {
		if strings.HasSuffix(compact, suffix) {
			return compact
		}
	}
	return query
}

func isHangulOrSpace(query string) bool {
	for _, r := range query {
		if r == ' ' || r == '\t' {
			continue
		}
		if r < '가' || r > '힣' {
			return false
		}
	}
	return true
}

func looksLikePlainLocationSlot(query string) bool {
	query = strings.TrimSpace(query)
	if query == "" || runeCount(query) > 30 {
		return false
	}
	if strings.ContainsAny(query, "\n\r") {
		return false
	}
	lowered := strings.ToLower(query)
	if containsAny(lowered,
		"사용자", "질문", "문의", "확인", "조회", "도구", "죄송", "정보", "실시간",
		"날씨", "비오", "비 오", "비와", "비 와", "기온", "온도",
	) {
		return false
	}
	if hasKnownLocationSuffix(query) {
		return true
	}
	return isSimpleASCIILocation(query)
}

var koreanWeatherLocationRe = regexp.MustCompile(`([가-힣A-Za-z0-9]+(?:\s+[가-힣A-Za-z0-9]+){0,3}\s*(?:역|동|구|시|군|읍|면|리|공항|터미널|시장|사거리|광장|공원|해변|산))`)

func hasKnownLocationSuffix(query string) bool {
	compact := strings.ReplaceAll(strings.TrimSpace(query), " ", "")
	for _, suffix := range []string{"역", "동", "구", "시", "군", "읍", "면", "리", "공항", "터미널", "시장", "사거리", "광장", "공원", "해변", "산"} {
		if strings.HasSuffix(compact, suffix) && runeCount(compact) > runeCount(suffix) {
			return true
		}
	}
	return false
}

func isSimpleASCIILocation(query string) bool {
	words := strings.Fields(query)
	if len(words) == 0 || len(words) > 4 {
		return false
	}
	if len(words) == 1 {
		runes := []rune(words[0])
		if len(runes) < 4 {
			return false
		}
	}
	for _, r := range query {
		if r == ' ' || r == '-' || r == '\'' || r == '.' {
			continue
		}
		if (r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			continue
		}
		return false
	}
	return true
}

func inferWeatherLocationFromText(text string) string {
	text = strings.TrimSpace(text)
	if text == "" {
		return ""
	}
	m := koreanWeatherLocationRe.FindStringSubmatch(text)
	if len(m) < 2 {
		return ""
	}
	return normalizeLocationSlot(m[1])
}

func buildWeatherSlotPrompt(userText string) string {
	var b strings.Builder
	b.WriteString("Extract structured slots for a current-weather package call.\n")
	b.WriteString("Return exactly one JSON object and nothing else:\n")
	b.WriteString(`{"location_query":""}`)
	b.WriteString("\n\nRules:\n")
	b.WriteString("- location_query is the explicit place the user asked about, including landmarks/stations/cities.\n")
	b.WriteString("- Remove time/weather words such as now/current/rain/weather, but do not invent a place.\n")
	b.WriteString("- If the user did not explicitly name a place, use an empty string.\n")
	b.WriteString("- Do not geocode. Do not answer the weather question.\n\n")
	b.WriteString("User text:\n")
	b.WriteString(userText)
	return b.String()
}

func resolveStructuredLocation(ctx context.Context, sess *AccountRuntime, query string) (structuredLocation, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return structuredLocation{}, nil
	}
	baseURL := effectiveKittypawAPIBaseURL(sess)
	u, err := url.Parse(strings.TrimRight(baseURL, "/") + "/v1/geo/resolve")
	if err != nil {
		return structuredLocation{}, err
	}
	q := u.Query()
	q.Set("q", query)
	u.RawQuery = q.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return structuredLocation{}, err
	}
	resp, err := geoHTTPClient.Do(req)
	if err != nil {
		return structuredLocation{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return structuredLocation{}, fmt.Errorf("geo resolve failed: status %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return structuredLocation{}, err
	}
	var payload struct {
		Lat         float64 `json:"lat"`
		Lon         float64 `json:"lon"`
		NameMatched string  `json:"name_matched"`
		Name        string  `json:"name"`
		Label       string  `json:"label"`
		City        string  `json:"city"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return structuredLocation{}, err
	}
	if math.IsNaN(payload.Lat) || math.IsNaN(payload.Lon) ||
		payload.Lat < -90 || payload.Lat > 90 || payload.Lon < -180 || payload.Lon > 180 {
		return structuredLocation{}, fmt.Errorf("geo resolve returned invalid coordinates")
	}
	label := firstNonEmpty(payload.NameMatched, payload.Label, payload.Name, payload.City, query)
	return structuredLocation{Label: label, Lat: payload.Lat, Lon: payload.Lon}, nil
}

func effectiveKittypawAPIBaseURL(sess *AccountRuntime) string {
	if sess != nil && sess.APITokenMgr != nil {
		if base, ok := sess.APITokenMgr.LoadAPIBaseURL(core.DefaultAPIServerURL); ok && strings.TrimSpace(base) != "" {
			return strings.TrimSpace(base)
		}
	}
	return kittypawAPIBaseURL
}

func weatherLocationParams(loc structuredLocation) map[string]any {
	return map[string]any{
		"location": map[string]any{
			"label": loc.Label,
			"lat":   loc.Lat,
			"lon":   loc.Lon,
		},
	}
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}
