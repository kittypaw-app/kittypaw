package connectadmin

import (
	"bytes"
	"html/template"
	"net/http"
	"strconv"
	"strings"

	"github.com/kittypaw-app/kittyportal/internal/auth"
)

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
<label>User ID <input name="user_id" required></label>
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
</body>
</html>
`

var connectAdminUsers = template.Must(template.New("connect-admin-users").Parse(connectAdminUsersTemplate))

type HandlerOptions struct {
	Registry ProviderRegistry
	Store    Store
}

type Handler struct {
	registry ProviderRegistry
	store    Store
}

func NewHandler(opts HandlerOptions) *Handler {
	return &Handler{
		registry: opts.Registry,
		store:    opts.Store,
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

		data := struct {
			Providers []ProviderInfo
		}{
			Providers: h.registry.List(),
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
		userID := strings.TrimSpace(r.FormValue("user_id"))
		providerID := strings.TrimSpace(r.FormValue("provider_id"))
		if userID == "" || providerID == "" {
			http.Error(w, "missing user_id or provider_id", http.StatusBadRequest)
			return
		}
		h.updateUserProvider(w, r, userID, providerID)
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
