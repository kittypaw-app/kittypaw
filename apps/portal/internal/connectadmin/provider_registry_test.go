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

func TestProviderRegistryProviderReturnsDeepCopy(t *testing.T) {
	registry := DefaultProviderRegistry(ProviderRegistryConfig{})
	originalScopes := strings.Fields(connect.GmailReadOnlyScope)

	got, ok := registry.Provider(connect.GmailProviderID)
	if !ok {
		t.Fatal("gmail provider not found")
	}
	got.Scopes[0] = "mutated.scope"
	got.DefaultPolicy.RequestedScopes[0] = "mutated.policy.scope"

	fresh, ok := registry.Provider(connect.GmailProviderID)
	if !ok {
		t.Fatal("gmail provider not found after mutation")
	}
	if !reflect.DeepEqual(fresh.Scopes, originalScopes) {
		t.Fatalf("fresh Scopes = %#v, want %#v", fresh.Scopes, originalScopes)
	}
	if !reflect.DeepEqual(fresh.DefaultPolicy.RequestedScopes, originalScopes) {
		t.Fatalf("fresh DefaultPolicy.RequestedScopes = %#v, want %#v", fresh.DefaultPolicy.RequestedScopes, originalScopes)
	}
}

func TestProviderRegistryListReturnsDeepCopies(t *testing.T) {
	registry := DefaultProviderRegistry(ProviderRegistryConfig{})
	originalScopes := strings.Fields(connect.XReadOnlyScope)

	got := registry.List()
	if len(got) != 2 {
		t.Fatalf("len(List()) = %d, want 2", len(got))
	}
	got[1].Scopes[0] = "mutated.scope"
	got[1].DefaultPolicy.RequestedScopes[0] = "mutated.policy.scope"

	fresh := registry.List()
	if !reflect.DeepEqual(fresh[1].Scopes, originalScopes) {
		t.Fatalf("fresh List()[1].Scopes = %#v, want %#v", fresh[1].Scopes, originalScopes)
	}
	if !reflect.DeepEqual(fresh[1].DefaultPolicy.RequestedScopes, originalScopes) {
		t.Fatalf("fresh List()[1].DefaultPolicy.RequestedScopes = %#v, want %#v", fresh[1].DefaultPolicy.RequestedScopes, originalScopes)
	}
}

func assertProviderInfo(t *testing.T, got, want ProviderInfo) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("provider info mismatch:\ngot  %#v\nwant %#v", got, want)
	}
}
