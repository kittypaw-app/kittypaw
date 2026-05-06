package connectadmin

import (
	"bytes"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"html/template"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/kittypaw-app/kittyportal/internal/auth"
	"github.com/kittypaw-app/kittyportal/internal/model"
)

const csrfCookieName = "kp_connect_admin_csrf"

const connectAdminHomeTemplate = `<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<title>KittyPaw Connect Admin</title>
</head>
<body>
<h1>KittyPaw Connect Admin</h1>
<p><a href="/admin/connect/users">User entitlements</a></p>
<table>
<thead>
<tr>
<th>Provider</th>
<th>Configured</th>
<th>Enabled</th>
<th>Default</th>
<th>Verification</th>
<th>Cost</th>
<th>Scopes</th>
</tr>
</thead>
<tbody>
{{range .Providers}}
<tr>
<td>{{.Name}}</td>
<td>{{.Configured}}</td>
<td>{{.Enabled}}</td>
<td>{{.DefaultEntitlement}}</td>
<td>{{.VerificationStatus}}</td>
<td>{{.CostMode}}</td>
<td>{{.Scopes}}</td>
</tr>
{{end}}
</tbody>
</table>
</body>
</html>
`

var connectAdminHome = template.Must(template.New("connect-admin-home").Parse(connectAdminHomeTemplate))

const connectAdminUsersTemplate = `<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<title>KittyPaw Connect User Entitlements</title>
</head>
<body>
<h1>Connect User Entitlements</h1>
<p><a href="/admin/connect">Provider policies</a></p>
<form method="post" action="/admin/connect/users">
<input type="hidden" name="csrf_token" value="{{.CSRFToken}}">
<label>User email <input name="user_email" type="email" required></label>
<label>Provider
<select name="provider_id">
{{range .Providers}}
<option value="{{.ID}}">{{.DisplayName}}</option>
{{end}}
</select>
</label>
<label>Status
<select name="status">
<option value="allowed">allowed</option>
<option value="blocked">blocked</option>
<option value="revoked">revoked</option>
</select>
</label>
<label>Monthly post reads <input name="monthly_post_reads" inputmode="numeric"></label>
<label>Reason <input name="reason"></label>
<button type="submit">Save entitlement</button>
</form>

<h2>Existing entitlements</h2>
<form method="get" action="/admin/connect/users">
<label>Email <input name="email" value="{{.Filters.EmailQuery}}"></label>
<label>Provider
<select name="provider">
<option value="">All</option>
{{range .Providers}}
<option value="{{.ID}}" {{if eq $.Filters.ProviderID .ID}}selected{{end}}>{{.DisplayName}}</option>
{{end}}
</select>
</label>
<label>Status
<select name="status">
<option value="">All</option>
<option value="allowed" {{if eq .Filters.Status "allowed"}}selected{{end}}>allowed</option>
<option value="blocked" {{if eq .Filters.Status "blocked"}}selected{{end}}>blocked</option>
<option value="revoked" {{if eq .Filters.Status "revoked"}}selected{{end}}>revoked</option>
</select>
</label>
<label>Per page
<select name="per_page">
<option value="10" {{if eq .Filters.PerPage 10}}selected{{end}}>10</option>
<option value="25" {{if eq .Filters.PerPage 25}}selected{{end}}>25</option>
<option value="50" {{if eq .Filters.PerPage 50}}selected{{end}}>50</option>
<option value="100" {{if eq .Filters.PerPage 100}}selected{{end}}>100</option>
</select>
</label>
<button type="submit">Filter</button>
</form>

<p>Page {{.Page}} - {{len .Entitlements}} of {{.Total}} entitlements</p>
<table>
<thead>
<tr>
<th>User</th>
<th>Provider</th>
<th>Status</th>
<th>Monthly post reads</th>
<th>Reason</th>
<th>Granted</th>
<th>Action</th>
</tr>
</thead>
<tbody>
{{range .Entitlements}}
<tr>
<td>{{.UserEmail}}{{if .UserName}}<br>{{.UserName}}{{end}}</td>
<td>{{.ProviderID}}</td>
<td colspan="5">
<form method="post" action="/admin/connect/users/{{.UserID}}/providers/{{.ProviderID}}">
<input type="hidden" name="csrf_token" value="{{$.CSRFToken}}">
<label>Status
<select name="status">
<option value="allowed" {{if eq .Status "allowed"}}selected{{end}}>allowed</option>
<option value="blocked" {{if eq .Status "blocked"}}selected{{end}}>blocked</option>
<option value="revoked" {{if eq .Status "revoked"}}selected{{end}}>revoked</option>
</select>
</label>
<label>Monthly post reads <input name="monthly_post_reads" inputmode="numeric" value="{{monthlyPostReads .QuotaJSON}}"></label>
<label>Reason <input name="reason" value="{{.Reason}}"></label>
<span>{{formatTime .GrantedAt}}</span>
<button type="submit">Save</button>
</form>
</td>
</tr>
{{else}}
<tr><td colspan="7">No entitlements</td></tr>
{{end}}
</tbody>
</table>
<nav>
{{if .PrevURL}}<a href="{{.PrevURL}}">Prev</a>{{end}}
{{if .NextURL}}<a href="{{.NextURL}}">Next</a>{{end}}
</nav>
</body>
</html>
`

var connectAdminUsers = template.Must(template.New("connect-admin-users").Funcs(template.FuncMap{
	"monthlyPostReads": monthlyPostReads,
	"formatTime":       formatEntitlementTime,
}).Parse(connectAdminUsersTemplate))

func monthlyPostReads(quota map[string]any) string {
	if quota == nil {
		return ""
	}
	switch value := quota["monthly_post_reads"].(type) {
	case int:
		return strconv.Itoa(value)
	case int64:
		return strconv.FormatInt(value, 10)
	case float64:
		if value == float64(int64(value)) {
			return strconv.FormatInt(int64(value), 10)
		}
		return strconv.FormatFloat(value, 'f', -1, 64)
	case string:
		return value
	default:
		return ""
	}
}

func formatEntitlementTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format("2006-01-02 15:04 UTC")
}

type HandlerOptions struct {
	Registry         ProviderRegistry
	Store            Store
	UserStore        model.UserStore
	CSRFCookieSecure bool
}

type Handler struct {
	registry         ProviderRegistry
	store            Store
	userStore        model.UserStore
	csrfCookieSecure bool
}

func NewHandler(opts HandlerOptions) *Handler {
	return &Handler{
		registry:         opts.Registry,
		store:            opts.Store,
		userStore:        opts.UserStore,
		csrfCookieSecure: opts.CSRFCookieSecure,
	}
}

func (h *Handler) HandleHome() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		setAdminSecurityHeaders(w)
		if r.Method != http.MethodGet {
			w.Header().Set("Allow", http.MethodGet)
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}

		policies, err := h.store.ListProviderPolicies(r.Context())
		if err != nil {
			http.Error(w, "list provider policies", http.StatusInternalServerError)
			return
		}
		policiesByProvider := make(map[string]ProviderPolicy, len(policies))
		for _, policy := range policies {
			policiesByProvider[policy.ProviderID] = policy
		}

		type providerRow struct {
			Name               string
			Configured         bool
			Enabled            bool
			DefaultEntitlement string
			VerificationStatus string
			CostMode           string
			Scopes             string
		}
		data := struct {
			Providers []providerRow
		}{}
		for _, provider := range h.registry.List() {
			policy := provider.DefaultPolicy
			if persisted, ok := policiesByProvider[provider.ID]; ok {
				policy = persisted
			}
			data.Providers = append(data.Providers, providerRow{
				Name:               provider.DisplayName,
				Configured:         provider.Configured,
				Enabled:            policy.Enabled,
				DefaultEntitlement: policy.DefaultEntitlement,
				VerificationStatus: policy.VerificationStatus,
				CostMode:           policy.CostMode,
				Scopes:             strings.Join(policy.RequestedScopes, ", "),
			})
		}

		var buf bytes.Buffer
		if err := connectAdminHome.Execute(&buf, data); err != nil {
			http.Error(w, "render connect admin", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		if _, err := w.Write(buf.Bytes()); err != nil {
			return
		}
	}
}

func (h *Handler) HandleUsers() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		setAdminSecurityHeaders(w)
		if r.Method != http.MethodGet {
			w.Header().Set("Allow", http.MethodGet)
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}

		csrfToken, err := h.setCSRFToken(w)
		if err != nil {
			http.Error(w, "generate csrf token", http.StatusInternalServerError)
			return
		}
		opts := parseUserEntitlementListOptions(r)
		result, err := h.store.ListUserEntitlements(r.Context(), opts)
		if err != nil {
			http.Error(w, "list user entitlements", http.StatusInternalServerError)
			return
		}
		data := struct {
			Providers    []ProviderInfo
			CSRFToken    string
			Filters      UserEntitlementListOptions
			Entitlements []UserEntitlementRow
			Page         int
			PerPage      int
			Total        int
			PrevURL      string
			NextURL      string
		}{
			Providers:    h.registry.List(),
			CSRFToken:    csrfToken,
			Filters:      resultOptionsOrFallback(result, opts),
			Entitlements: result.Items,
			Page:         result.Page,
			PerPage:      result.PerPage,
			Total:        result.Total,
		}
		if result.Page > 1 {
			data.PrevURL = buildUserEntitlementsPageURL(data.Filters, result.Page-1)
		}
		if result.Page*result.PerPage < result.Total {
			data.NextURL = buildUserEntitlementsPageURL(data.Filters, result.Page+1)
		}
		var buf bytes.Buffer
		if err := connectAdminUsers.Execute(&buf, data); err != nil {
			http.Error(w, "render connect admin users", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		if _, err := w.Write(buf.Bytes()); err != nil {
			return
		}
	}
}

func parseUserEntitlementListOptions(r *http.Request) UserEntitlementListOptions {
	q := r.URL.Query()
	opts := UserEntitlementListOptions{
		Page:       parsePositiveInt(q.Get("page"), 1),
		PerPage:    parsePositiveInt(q.Get("per_page"), 25),
		ProviderID: strings.TrimSpace(q.Get("provider")),
		Status:     strings.TrimSpace(q.Get("status")),
		EmailQuery: strings.TrimSpace(q.Get("email")),
	}
	if opts.PerPage > 100 {
		opts.PerPage = 100
	}
	if opts.Status != "" && opts.Status != EntitlementAllowed && opts.Status != EntitlementBlocked && opts.Status != EntitlementRevoked {
		opts.Status = ""
	}
	return opts
}

func parsePositiveInt(raw string, fallback int) int {
	n, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil || n < 1 {
		return fallback
	}
	return n
}

func resultOptionsOrFallback(result UserEntitlementListResult, fallback UserEntitlementListOptions) UserEntitlementListOptions {
	if result.Page > 0 {
		fallback.Page = result.Page
	}
	if result.PerPage > 0 {
		fallback.PerPage = result.PerPage
	}
	return fallback
}

func buildUserEntitlementsPageURL(opts UserEntitlementListOptions, page int) string {
	parts := []string{
		fmt.Sprintf("page=%d", page),
		fmt.Sprintf("per_page=%d", opts.PerPage),
	}
	if opts.ProviderID != "" {
		parts = append(parts, "provider="+urlQueryEscape(opts.ProviderID))
	}
	if opts.Status != "" {
		parts = append(parts, "status="+urlQueryEscape(opts.Status))
	}
	if opts.EmailQuery != "" {
		parts = append(parts, "email="+urlQueryEscape(opts.EmailQuery))
	}
	return "/admin/connect/users?" + strings.Join(parts, "&")
}

func urlQueryEscape(value string) string {
	return strings.ReplaceAll(url.QueryEscape(value), "+", "%20")
}

func (h *Handler) HandleUserProviderUpdateFromForm() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		setAdminSecurityHeaders(w)
		if r.Method != http.MethodPost {
			w.Header().Set("Allow", http.MethodPost)
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		if err := r.ParseForm(); err != nil {
			http.Error(w, "invalid form", http.StatusBadRequest)
			return
		}
		if !h.validCSRF(r) {
			http.Error(w, "invalid csrf token", http.StatusForbidden)
			return
		}
		userEmail := strings.TrimSpace(r.FormValue("user_email"))
		providerID := strings.TrimSpace(r.FormValue("provider_id"))
		if userEmail == "" || providerID == "" {
			http.Error(w, "missing user_email or provider_id", http.StatusBadRequest)
			return
		}
		if h.userStore == nil {
			http.Error(w, "user lookup unavailable", http.StatusInternalServerError)
			return
		}
		user, err := h.userStore.FindByEmail(r.Context(), userEmail)
		if err != nil {
			switch {
			case errors.Is(err, model.ErrNotFound):
				http.Error(w, "user not found", http.StatusNotFound)
			case errors.Is(err, model.ErrAmbiguous):
				http.Error(w, "multiple users have this email; use the user-specific update route", http.StatusConflict)
			default:
				http.Error(w, "lookup user", http.StatusInternalServerError)
			}
			return
		}
		h.updateUserProvider(w, r, user.ID, providerID)
	}
}

func (h *Handler) HandleUserProviderUpdate(userID, providerID string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		setAdminSecurityHeaders(w)
		if r.Method != http.MethodPost {
			w.Header().Set("Allow", http.MethodPost)
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		if err := r.ParseForm(); err != nil {
			http.Error(w, "invalid form", http.StatusBadRequest)
			return
		}
		if !h.validCSRF(r) {
			http.Error(w, "invalid csrf token", http.StatusForbidden)
			return
		}
		h.updateUserProvider(w, r, strings.TrimSpace(userID), strings.TrimSpace(providerID))
	}
}

func (h *Handler) updateUserProvider(w http.ResponseWriter, r *http.Request, userID, providerID string) {
	if userID == "" {
		http.Error(w, "missing user_id", http.StatusBadRequest)
		return
	}
	if _, ok := h.registry.Provider(providerID); !ok {
		http.NotFound(w, r)
		return
	}

	status := r.FormValue("status")
	if status != EntitlementAllowed && status != EntitlementBlocked && status != EntitlementRevoked {
		http.Error(w, "invalid status", http.StatusBadRequest)
		return
	}

	quota := map[string]any{}
	if raw := strings.TrimSpace(r.FormValue("monthly_post_reads")); raw != "" {
		value, err := strconv.Atoi(raw)
		if err != nil || value < 0 {
			http.Error(w, "invalid monthly_post_reads", http.StatusBadRequest)
			return
		}
		quota["monthly_post_reads"] = value
	}

	actor := auth.UserFromContext(r.Context())
	if actor == nil {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	reason := r.FormValue("reason")

	// Admin membership is enforced by router middleware; these handlers only
	// require the authenticated actor for audit attribution.
	entitlement := UserEntitlement{
		UserID:     userID,
		ProviderID: providerID,
		Status:     status,
		Reason:     reason,
		QuotaJSON:  quota,
		GrantedBy:  actor.ID,
	}
	event := AuditEvent{
		ActorUserID:  actor.ID,
		Action:       "entitlement.update",
		ProviderID:   providerID,
		TargetUserID: userID,
		After: map[string]any{
			"status": status,
			"reason": reason,
			"quota":  quota,
		},
	}
	if err := h.store.UpdateUserEntitlementWithAudit(r.Context(), entitlement, event); err != nil {
		http.Error(w, "update entitlement", http.StatusInternalServerError)
		return
	}

	http.Redirect(w, r, "/admin/connect/users", http.StatusSeeOther)
}

func setAdminSecurityHeaders(w http.ResponseWriter) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Referrer-Policy", "no-referrer")
}

func (h *Handler) setCSRFToken(w http.ResponseWriter) (string, error) {
	token, err := randomCSRFToken()
	if err != nil {
		return "", err
	}
	http.SetCookie(w, &http.Cookie{
		Name:     csrfCookieName,
		Value:    token,
		Path:     "/admin/connect",
		MaxAge:   3600,
		HttpOnly: true,
		Secure:   h.csrfCookieSecure,
		SameSite: http.SameSiteLaxMode,
	})
	return token, nil
}

func (h *Handler) validCSRF(r *http.Request) bool {
	cookie, err := r.Cookie(csrfCookieName)
	if err != nil || cookie.Value == "" {
		return false
	}
	formToken := strings.TrimSpace(r.FormValue("csrf_token"))
	if formToken == "" {
		return false
	}
	if len(formToken) != len(cookie.Value) {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(formToken), []byte(cookie.Value)) == 1
}

func randomCSRFToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}
