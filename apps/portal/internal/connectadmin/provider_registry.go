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
	if !ok {
		return ProviderInfo{}, false
	}
	return cloneProviderInfo(provider), true
}

func (r ProviderRegistry) List() []ProviderInfo {
	out := make([]ProviderInfo, len(r.providers))
	for i, provider := range r.providers {
		out[i] = cloneProviderInfo(provider)
	}
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
			Scopes:       cloneStringSlice(gmailScopes),
			WriteCapable: false,
			CostBearing:  false,
			DocsURL:      "https://developers.google.com/workspace/gmail/api/auth/scopes",
			DefaultPolicy: ProviderPolicy{
				ProviderID:         connect.GmailProviderID,
				Enabled:            true,
				DefaultEntitlement: DefaultEntitlementAllow,
				RequestedScopes:    cloneStringSlice(gmailScopes),
				VerificationStatus: VerificationTesting,
				CostMode:           CostModeNone,
			},
		},
		{
			ID:           connect.XProviderID,
			DisplayName:  "X",
			Configured:   cfg.XConfigured,
			Scopes:       cloneStringSlice(xScopes),
			WriteCapable: false,
			CostBearing:  true,
			DocsURL:      "https://docs.x.com/x-api/fundamentals/post-cap",
			DefaultPolicy: ProviderPolicy{
				ProviderID:         connect.XProviderID,
				Enabled:            true,
				DefaultEntitlement: DefaultEntitlementDeny,
				RequestedScopes:    cloneStringSlice(xScopes),
				VerificationStatus: VerificationNotApplicable,
				CostMode:           CostModeKittyPaid,
			},
		},
	}

	byID := make(map[string]ProviderInfo, len(providers))
	for _, provider := range providers {
		byID[provider.ID] = cloneProviderInfo(provider)
	}
	return ProviderRegistry{
		providers: providers,
		byID:      byID,
	}
}

func cloneProviderInfo(provider ProviderInfo) ProviderInfo {
	provider.Scopes = cloneStringSlice(provider.Scopes)
	provider.DefaultPolicy = cloneProviderPolicy(provider.DefaultPolicy)
	return provider
}

func cloneProviderPolicy(policy ProviderPolicy) ProviderPolicy {
	policy.RequestedScopes = cloneStringSlice(policy.RequestedScopes)
	return policy
}

func cloneStringSlice(in []string) []string {
	if in == nil {
		return nil
	}
	out := make([]string, len(in))
	copy(out, in)
	return out
}
