package connectadmin

import (
	"reflect"
	"strings"
	"testing"

	"github.com/kittypaw-app/kittyportal/internal/connect"
)

func TestDefaultProviderRegistry(t *testing.T) {
	registry := DefaultProviderRegistry(ProviderRegistryConfig{
		GmailConfigured: true,
		XConfigured:     false,
	})

	gmail, ok := registry.Provider(connect.GmailProviderID)
	if !ok {
		t.Fatal("gmail provider not found")
	}
	gmailScopes := strings.Fields(connect.GmailReadOnlyScope)
	assertProviderInfo(t, gmail, ProviderInfo{
		ID:           connect.GmailProviderID,
		DisplayName:  "Gmail",
		Configured:   true,
		Scopes:       gmailScopes,
		WriteCapable: false,
		CostBearing:  false,
		DocsURL:      "https://developers.google.com/workspace/gmail/api/auth/scopes",
		DefaultPolicy: ProviderPolicy{
			ProviderID:         connect.GmailProviderID,
			Enabled:            true,
			DefaultEntitlement: DefaultEntitlementAllow,
			RequestedScopes:    gmailScopes,
			VerificationStatus: VerificationTesting,
			CostMode:           CostModeNone,
		},
	})

	x, ok := registry.Provider(connect.XProviderID)
	if !ok {
		t.Fatal("x provider not found")
	}
	xScopes := strings.Fields(connect.XReadOnlyScope)
	assertProviderInfo(t, x, ProviderInfo{
		ID:           connect.XProviderID,
		DisplayName:  "X",
		Configured:   false,
		Scopes:       xScopes,
		WriteCapable: false,
		CostBearing:  true,
		DocsURL:      "https://docs.x.com/x-api/fundamentals/post-cap",
		DefaultPolicy: ProviderPolicy{
			ProviderID:         connect.XProviderID,
			Enabled:            true,
			DefaultEntitlement: DefaultEntitlementDeny,
			RequestedScopes:    xScopes,
			VerificationStatus: VerificationNotApplicable,
			CostMode:           CostModeKittyPaid,
		},
	})
}

func TestProviderRegistryListOrder(t *testing.T) {
	registry := DefaultProviderRegistry(ProviderRegistryConfig{})

	got := registry.List()
	if len(got) != 2 {
		t.Fatalf("len(List()) = %d, want 2", len(got))
	}
	if got[0].ID != connect.GmailProviderID || got[1].ID != connect.XProviderID {
		t.Fatalf("List order = [%q, %q], want [%q, %q]", got[0].ID, got[1].ID, connect.GmailProviderID, connect.XProviderID)
	}
}

func TestProviderRegistryUnknownProvider(t *testing.T) {
	registry := DefaultProviderRegistry(ProviderRegistryConfig{})

	if got, ok := registry.Provider("unknown"); ok {
		t.Fatalf("Provider unknown ok = true, got %#v", got)
	}
}

func assertProviderInfo(t *testing.T, got, want ProviderInfo) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("provider info mismatch:\ngot  %#v\nwant %#v", got, want)
	}
}
