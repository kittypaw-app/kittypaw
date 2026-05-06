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

func (h *Handler) HandleUserProviderUpdate(userID, providerID string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		setAdminSecurityHeaders(w)
		if r.Method != http.MethodPost {
			w.Header().Set("Allow", http.MethodPost)
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		if _, ok := h.registry.Provider(providerID); !ok {
			http.NotFound(w, r)
			return
		}
		if err := r.ParseForm(); err != nil {
			http.Error(w, "invalid form", http.StatusBadRequest)
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
}

func setAdminSecurityHeaders(w http.ResponseWriter) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Referrer-Policy", "no-referrer")
}
