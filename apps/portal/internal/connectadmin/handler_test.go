package connectadmin

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/kittypaw-app/kittyportal/internal/auth"
	"github.com/kittypaw-app/kittyportal/internal/connect"
	"github.com/kittypaw-app/kittyportal/internal/model"
)

func TestHandlerHomeShowsProvidersWithoutSecrets(t *testing.T) {
	store := &fakeStore{
		policies: []ProviderPolicy{
			{
				ProviderID:         connect.GmailProviderID,
				Enabled:            true,
				DefaultEntitlement: DefaultEntitlementAllow,
				RequestedScopes:    []string{"gmail.readonly"},
				VerificationStatus: VerificationTesting,
				CostMode:           CostModeNone,
				Notes:              "client_secret=do-not-render",
			},
			{
				ProviderID:         connect.XProviderID,
				Enabled:            false,
				DefaultEntitlement: DefaultEntitlementDeny,
				RequestedScopes:    []string{"tweet.read", "users.read"},
				VerificationStatus: VerificationNotApplicable,
				CostMode:           CostModeKittyPaid,
				Notes:              "ACCESS_TOKEN=do-not-render",
			},
		},
	}
	handler := NewHandler(HandlerOptions{
		Registry: DefaultProviderRegistry(ProviderRegistryConfig{GmailConfigured: true, XConfigured: true}),
		Store:    store,
	})

	req := httptest.NewRequest(http.MethodGet, "/admin/connect", nil)
	req = req.WithContext(auth.ContextWithUser(req.Context(), &model.User{ID: "admin-user"}))
	rec := httptest.NewRecorder()

	handler.HandleHome()(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	if got := rec.Header().Get("Content-Type"); got != "text/html; charset=utf-8" {
		t.Fatalf("Content-Type = %q, want text/html; charset=utf-8", got)
	}
	body := rec.Body.String()
	for _, want := range []string{"KittyPaw Connect Admin", "Gmail", "X", "kitty_paid", "deny"} {
		if !strings.Contains(body, want) {
			t.Fatalf("body missing %q:\n%s", want, body)
		}
	}
	lower := strings.ToLower(body)
	for _, forbidden := range []string{"secret", "access_token", "refresh_token", "bearer token", "gmail_client_secret", "x_bearer_token"} {
		if strings.Contains(lower, forbidden) {
			t.Fatalf("body contains forbidden text %q:\n%s", forbidden, body)
		}
	}
}

func TestHandlerGrantEntitlementWritesAudit(t *testing.T) {
	store := &fakeStore{}
	handler := NewHandler(HandlerOptions{
		Registry: DefaultProviderRegistry(ProviderRegistryConfig{}),
		Store:    store,
	})
	form := url.Values{
		"status":             {"allowed"},
		"reason":             {"internal beta"},
		"monthly_post_reads": {"100"},
	}
	req := httptest.NewRequest(http.MethodPost, "/admin/connect/users/user-1/providers/x", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req = req.WithContext(auth.ContextWithUser(req.Context(), &model.User{ID: "admin-user"}))
	rec := httptest.NewRecorder()

	handler.HandleUserProviderUpdate("user-1", connect.XProviderID)(rec, req)

	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusSeeOther)
	}
	if got := rec.Header().Get("Location"); got != "/admin/connect/users" {
		t.Fatalf("Location = %q, want /admin/connect/users", got)
	}
	if len(store.entitlements) != 1 {
		t.Fatalf("entitlements len = %d, want 1", len(store.entitlements))
	}
	entitlement := store.entitlements[0]
	if entitlement.UserID != "user-1" || entitlement.ProviderID != connect.XProviderID || entitlement.Status != EntitlementAllowed {
		t.Fatalf("entitlement = %#v", entitlement)
	}
	if entitlement.Reason != "internal beta" || entitlement.GrantedBy != "admin-user" {
		t.Fatalf("entitlement actor/reason = %#v", entitlement)
	}
	if got := entitlement.QuotaJSON["monthly_post_reads"]; got != 100 {
		t.Fatalf("monthly_post_reads = %#v, want 100", got)
	}
	if len(store.auditEvents) != 1 {
		t.Fatalf("audit events len = %d, want 1", len(store.auditEvents))
	}
	event := store.auditEvents[0]
	if event.Action != "entitlement.update" || event.ActorUserID != "admin-user" || event.ProviderID != connect.XProviderID || event.TargetUserID != "user-1" {
		t.Fatalf("audit event = %#v", event)
	}
	if event.After["status"] != EntitlementAllowed || event.After["reason"] != "internal beta" {
		t.Fatalf("audit after = %#v", event.After)
	}
}

func TestHandlerHomeRejectsInvalidMethod(t *testing.T) {
	handler := NewHandler(HandlerOptions{
		Registry: DefaultProviderRegistry(ProviderRegistryConfig{}),
		Store:    &fakeStore{},
	})
	req := httptest.NewRequest(http.MethodPost, "/admin/connect", nil)
	rec := httptest.NewRecorder()

	handler.HandleHome()(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusMethodNotAllowed)
	}
}

func TestHandlerUserProviderUpdateRejectsInvalidStatus(t *testing.T) {
	handler := NewHandler(HandlerOptions{
		Registry: DefaultProviderRegistry(ProviderRegistryConfig{}),
		Store:    &fakeStore{},
	})
	form := url.Values{"status": {"pending"}}
	req := httptest.NewRequest(http.MethodPost, "/admin/connect/users/user-1/providers/x", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req = req.WithContext(auth.ContextWithUser(req.Context(), &model.User{ID: "admin-user"}))
	rec := httptest.NewRecorder()

	handler.HandleUserProviderUpdate("user-1", connect.XProviderID)(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusBadRequest)
	}
}

func TestHandlerUserProviderUpdateRejectsUnknownProvider(t *testing.T) {
	handler := NewHandler(HandlerOptions{
		Registry: DefaultProviderRegistry(ProviderRegistryConfig{}),
		Store:    &fakeStore{},
	})
	form := url.Values{"status": {"allowed"}}
	req := httptest.NewRequest(http.MethodPost, "/admin/connect/users/user-1/providers/unknown", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req = req.WithContext(auth.ContextWithUser(req.Context(), &model.User{ID: "admin-user"}))
	rec := httptest.NewRecorder()

	handler.HandleUserProviderUpdate("user-1", "unknown")(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusNotFound)
	}
}

func TestHandlerUserProviderUpdateRejectsNilUser(t *testing.T) {
	handler := NewHandler(HandlerOptions{
		Registry: DefaultProviderRegistry(ProviderRegistryConfig{}),
		Store:    &fakeStore{},
	})
	form := url.Values{"status": {"allowed"}}
	req := httptest.NewRequest(http.MethodPost, "/admin/connect/users/user-1/providers/x", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()

	handler.HandleUserProviderUpdate("user-1", connect.XProviderID)(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusUnauthorized)
	}
}

type fakeStore struct {
	policies     []ProviderPolicy
	entitlements []UserEntitlement
	auditEvents  []AuditEvent
}

func (s *fakeStore) UpsertProviderPolicy(context.Context, ProviderPolicy) error {
	return nil
}

func (s *fakeStore) GetProviderPolicy(_ context.Context, providerID string) (ProviderPolicy, error) {
	for _, policy := range s.policies {
		if policy.ProviderID == providerID {
			return policy, nil
		}
	}
	return ProviderPolicy{}, nil
}

func (s *fakeStore) ListProviderPolicies(context.Context) ([]ProviderPolicy, error) {
	return append([]ProviderPolicy(nil), s.policies...), nil
}

func (s *fakeStore) UpsertUserEntitlement(_ context.Context, entitlement UserEntitlement) error {
	s.entitlements = append(s.entitlements, entitlement)
	return nil
}

func (s *fakeStore) UserAllowed(context.Context, string, string) (bool, error) {
	return false, nil
}

func (s *fakeStore) AppendAuditEvent(_ context.Context, event AuditEvent) error {
	s.auditEvents = append(s.auditEvents, event)
	return nil
}

func (s *fakeStore) ListAuditEvents(context.Context, int) ([]AuditEvent, error) {
	return append([]AuditEvent(nil), s.auditEvents...), nil
}

func (s *fakeStore) EnsureDefaultPolicies(context.Context, ProviderRegistry) error {
	return nil
}
