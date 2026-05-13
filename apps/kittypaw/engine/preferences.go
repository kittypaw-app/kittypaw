package engine

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"
	"time"

	"github.com/jinto/kittypaw/core"
)

const (
	exchangeRateDisplayPreferenceName = "exchange_rate.display"
	preferencePrefix                  = "preference:"
	preferenceCandidatePrefix         = "preference_candidate:"
	preferencePendingPrefix           = "preference_pending_confirmation:"
	preferenceRejectedPrefix          = "preference_rejected:"
	preferenceSurfacedPrefix          = "preference_candidate_surfaced:"
)

type exchangeRateDisplayPreference struct {
	Base   string `json:"base"`
	Unit   int    `json:"unit,omitempty"`
	Reason string `json:"reason,omitempty"`
}

type PreferenceConfirmationBranch struct{}

func (b *PreferenceConfirmationBranch) Execute(_ context.Context, sess *AccountRuntime, event core.Event, intent Intent) (string, error) {
	if sess == nil || sess.Store == nil {
		return "", errBranchFallback
	}
	pending, ok := loadPendingPreferenceConfirmation(sess)
	if !ok {
		return "", errBranchFallback
	}
	reply := ""
	if p, err := event.ParsePayload(); err == nil {
		reply = p.Text
	}
	if reply == "" {
		return "", errBranchFallback
	}
	switch {
	case isBareAffirmative(reply):
		return acceptPreferenceConfirmation(sess, pending)
	case isBareNegative(reply):
		return rejectPreferenceConfirmation(sess, pending)
	default:
		return "", errBranchFallback
	}
}

func loadPendingPreferenceConfirmation(sess *AccountRuntime) (PendingPreferenceConfirmation, bool) {
	if sess == nil {
		return PendingPreferenceConfirmation{}, false
	}
	if sess.Pipeline != nil {
		if pending, ok := sess.Pipeline.RecentPendingPreferenceConfirmation(); ok {
			return pending, true
		}
	}
	if raw, ok := getUserContextValue(sess, preferencePendingPrefix+exchangeRateDisplayPreferenceName); ok {
		return PendingPreferenceConfirmation{
			Key:   exchangeRateDisplayPreferenceName,
			Value: raw,
		}, true
	}
	return PendingPreferenceConfirmation{}, false
}

func acceptPreferenceConfirmation(sess *AccountRuntime, pending PendingPreferenceConfirmation) (string, error) {
	switch pending.Key {
	case exchangeRateDisplayPreferenceName:
		pref, ok := parseExchangeRateDisplayPreference(pending.Value)
		if !ok {
			clearPendingPreference(sess, pending.Key)
			return "", errBranchFallback
		}
		raw := marshalExchangeRateDisplayPreference(pref)
		_ = sess.Store.SetUserContext(preferencePrefix+pending.Key, raw, "user_preference")
		_, _ = sess.Store.DeleteUserContext(preferenceCandidatePrefix + pending.Key)
		clearPendingPreference(sess, pending.Key)
		return "앞으로 환율은 " + formatExchangeRateUnit(pref.Unit) + " " + pref.Base + " 기준으로 보여드릴게요.", nil
	default:
		return "", errBranchFallback
	}
}

func rejectPreferenceConfirmation(sess *AccountRuntime, pending PendingPreferenceConfirmation) (string, error) {
	_ = sess.Store.SetUserContext(preferenceRejectedPrefix+pending.Key, pending.Value, "user_rejection")
	clearPendingPreference(sess, pending.Key)
	switch pending.Key {
	case exchangeRateDisplayPreferenceName:
		return "알겠습니다. 환율은 기본 형식으로 보여드릴게요.", nil
	default:
		return "알겠습니다. 이 선호는 적용하지 않을게요.", nil
	}
}

func clearPendingPreference(sess *AccountRuntime, key string) {
	if sess == nil {
		return
	}
	if sess.Pipeline != nil {
		sess.Pipeline.ClearPendingPreferenceConfirmation()
	}
	if sess.Store != nil && key != "" {
		_, _ = sess.Store.DeleteUserContext(preferencePendingPrefix + key)
	}
}

func loadExchangeRateDisplayPreference(sess *AccountRuntime) (exchangeRateDisplayPreference, bool) {
	raw, ok := getUserContextValue(sess, preferencePrefix+exchangeRateDisplayPreferenceName)
	if !ok {
		return exchangeRateDisplayPreference{}, false
	}
	return parseExchangeRateDisplayPreference(raw)
}

func exchangeRateDisplayPreferenceCandidate(sess *AccountRuntime) (exchangeRateDisplayPreference, bool) {
	if _, ok := loadExchangeRateDisplayPreference(sess); ok {
		return exchangeRateDisplayPreference{}, false
	}
	if _, ok := getUserContextValue(sess, preferenceRejectedPrefix+exchangeRateDisplayPreferenceName); ok {
		return exchangeRateDisplayPreference{}, false
	}
	if _, ok := getUserContextValue(sess, preferenceSurfacedPrefix+exchangeRateDisplayPreferenceName); ok {
		return exchangeRateDisplayPreference{}, false
	}
	if raw, ok := getUserContextValue(sess, preferenceCandidatePrefix+exchangeRateDisplayPreferenceName); ok {
		return parseExchangeRateDisplayPreference(raw)
	}
	if raw, ok := getUserContextValue(sess, "currency_display_preference"); ok {
		normalized := strings.ToLower(strings.ReplaceAll(raw, ",", ""))
		if strings.Contains(normalized, "1000") && (strings.Contains(normalized, "원") || strings.Contains(normalized, "krw")) {
			return exchangeRateDisplayPreference{
				Base:   "KRW",
				Unit:   1000,
				Reason: "legacy currency_display_preference",
			}, true
		}
	}
	return exchangeRateDisplayPreference{}, false
}

func parseExchangeRateDisplayPreference(raw string) (exchangeRateDisplayPreference, bool) {
	var pref exchangeRateDisplayPreference
	if err := json.Unmarshal([]byte(raw), &pref); err != nil {
		return exchangeRateDisplayPreference{}, false
	}
	pref.Base = strings.ToUpper(strings.TrimSpace(pref.Base))
	if pref.Unit <= 0 {
		pref.Unit = 1
	}
	if pref.Base == "" {
		return exchangeRateDisplayPreference{}, false
	}
	return pref, true
}

func exchangeRatePreferenceParams(pref exchangeRateDisplayPreference) map[string]any {
	params := map[string]any{
		"base":    pref.Base,
		"symbols": defaultExchangeRateSymbols(pref.Base),
	}
	if pref.Unit > 1 {
		params["unit"] = pref.Unit
	}
	return params
}

func exchangeRateRequestedUnit(params map[string]any) int {
	if len(params) == 0 {
		return 1
	}
	switch v := params["unit"].(type) {
	case int:
		if v > 0 {
			return v
		}
	case int64:
		if v > 0 {
			return int(v)
		}
	case float64:
		if v > 0 {
			return int(v)
		}
	case string:
		if n, err := strconv.Atoi(strings.ReplaceAll(v, ",", "")); err == nil && n > 0 {
			return n
		}
	}
	return 1
}

func exchangeRateDisplayUnitForText(text string) int {
	t := strings.ToLower(strings.ReplaceAll(text, ",", ""))
	switch {
	case strings.Contains(t, "1000원") || strings.Contains(t, "1000 krw") || strings.Contains(t, "1000krw") || strings.Contains(t, "천원"):
		return 1000
	default:
		return 1
	}
}

func applyExchangeRateDisplayParams(output string, params map[string]any) string {
	if output == "" || len(params) == 0 {
		return output
	}
	base := exchangeRateRequestedBase(params)
	if base != "" && exchangeRateOutputBase(output) != base {
		if rebased, ok := rebaseExchangeRateOutput(output, base); ok {
			output = rebased
		}
	}
	unit := exchangeRateRequestedUnit(params)
	if unit > 1 {
		if scaled, ok := scaleExchangeRateOutput(output, unit); ok {
			output = scaled
		}
	}
	return output
}

func scaleExchangeRateOutput(output string, unit int) (string, bool) {
	if unit <= 1 {
		return output, true
	}
	table, ok := parseExchangeRateOutput(output)
	if !ok || table.base == "" {
		return "", false
	}
	header := table.header
	if header == "" {
		header = "📈 환율"
	}
	var sb strings.Builder
	sb.WriteString(header)
	sb.WriteString("\n\n")
	amount := formatExchangeRateUnit(unit)
	for i, code := range table.order {
		if code == table.base {
			continue
		}
		rate, ok := table.rates[code]
		if !ok {
			continue
		}
		if i > 0 {
			sb.WriteString("\n")
		}
		sb.WriteString(amount)
		sb.WriteString(" ")
		sb.WriteString(table.base)
		sb.WriteString(" = ")
		sb.WriteString(formatScaledExchangeRateNumber(rate * float64(unit)))
		sb.WriteString(" ")
		sb.WriteString(code)
	}
	if table.source != "" {
		sb.WriteString("\n\n")
		sb.WriteString(table.source)
	}
	return sb.String(), true
}

func formatExchangeRateUnit(unit int) string {
	s := strconv.Itoa(unit)
	if len(s) <= 3 {
		return s
	}
	var parts []string
	for len(s) > 3 {
		parts = append([]string{s[len(s)-3:]}, parts...)
		s = s[:len(s)-3]
	}
	parts = append([]string{s}, parts...)
	return strings.Join(parts, ",")
}

func formatScaledExchangeRateNumber(v float64) string {
	if v >= 100 {
		return strconv.FormatFloat(v, 'f', 1, 64)
	}
	if v < 1 {
		return strconv.FormatFloat(v, 'f', 2, 64)
	}
	out := strconv.FormatFloat(v, 'f', 2, 64)
	return strings.TrimRight(strings.TrimRight(out, "0"), ".")
}

func maybeAppendExchangeRatePreferenceConfirmation(sess *AccountRuntime, output string) string {
	if sess == nil || sess.Store == nil || sess.Pipeline == nil {
		return output
	}
	pref, ok := exchangeRateDisplayPreferenceCandidate(sess)
	if !ok {
		return output
	}
	raw := marshalExchangeRateDisplayPreference(pref)
	pending := PendingPreferenceConfirmation{
		Key:   exchangeRateDisplayPreferenceName,
		Value: raw,
	}
	sess.Pipeline.RecordPendingPreferenceConfirmation(pending)
	_ = sess.Store.SetUserContext(preferencePendingPrefix+exchangeRateDisplayPreferenceName, raw, "preference")
	_ = sess.Store.SetUserContext(preferenceSurfacedPrefix+exchangeRateDisplayPreferenceName, time.Now().UTC().Format(time.RFC3339), "preference")
	return output + "\n\n이전 대화 기록상 환율을 " + formatExchangeRateUnit(pref.Unit) + " " + pref.Base + " 기준으로 보길 원하신 것 같아요. 앞으로 그렇게 보여드릴까요?"
}

func marshalExchangeRateDisplayPreference(pref exchangeRateDisplayPreference) string {
	data, _ := json.Marshal(pref)
	return string(data)
}

func getUserContextValue(sess *AccountRuntime, key string) (string, bool) {
	if sess == nil || sess.Store == nil || key == "" {
		return "", false
	}
	value, ok, err := sess.Store.GetUserContext(key)
	if err != nil || !ok || value == "" {
		return "", false
	}
	return value, true
}
