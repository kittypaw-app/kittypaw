package connectadmin

import (
	"strings"

	"github.com/kittypaw-app/kittyportal/internal/connect"
)

type ProviderRegistryConfig struct {
	GmailConfigured bool
	XConfigured     bool
}

type ProviderInfo struct {
	ID            string
	DisplayName   string
	Configured    bool
	Scopes        []string
	WriteCapable  bool
	CostBearing   bool
	DocsURL       string
	DefaultPolicy ProviderPolicy
}

type ProviderRegistry struct {
	providers []ProviderInfo
	byID      map[string]ProviderInfo
}

func (r ProviderRegistry) Provider(id string) (ProviderInfo, bool) {
	provider, ok := r.byID[id]
	return provider, ok
}

func (r ProviderRegistry) List() []ProviderInfo {
	out := make([]ProviderInfo, len(r.providers))
	copy(out, r.providers)
	return out
}

func DefaultProviderRegistry(cfg ProviderRegistryConfig) ProviderRegistry {
	gmailScopes := strings.Fields(connect.GmailReadOnlyScope)
	xScopes := strings.Fields(connect.XReadOnlyScope)
	providers := []ProviderInfo{
		{
			ID:           connect.GmailProviderID,
			DisplayName:  "Gmail",
			Configured:   cfg.GmailConfigured,
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
		},
		{
			ID:           connect.XProviderID,
			DisplayName:  "X",
			Configured:   cfg.XConfigured,
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
		},
	}

	byID := make(map[string]ProviderInfo, len(providers))
	for _, provider := range providers {
		byID[provider.ID] = provider
	}
	return ProviderRegistry{
		providers: providers,
		byID:      byID,
	}
}
