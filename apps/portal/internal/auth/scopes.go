package auth

// Plan 13 — auth authority vs resource server (URL form).
// docs/specs/kittychat-credential-foundation.md (D2 + D3 + D4 + D8).
//
// Issuer/audience use RFC 7519 / OIDC URL-form identifiers (not opaque strings).
// scope vocabulary is additive only — never rename or remove existing entries.
// To extend, add new constants here and pin them in the spec.

const (
	ScopeChatRelay     = "chat:relay"
	ScopeModelsRead    = "models:read"
	ScopeDaemonConnect = "daemon:connect"

	// AudienceAPI / AudienceChat / AudienceSpace identify the resource servers
	// this token is valid against. During Space migration, OAuth-issued tokens
	// validate against API, legacy chat, and Space resource checks.
	AudienceAPI   = "https://api.kittypaw.app"
	AudienceChat  = "https://chat.kittypaw.app"
	AudienceSpace = "https://space.kittypaw.app"

	// Issuer identifies the auth authority. Public identity contracts are
	// canonical under portal.kittypaw.app; api.kittypaw.app remains the
	// resource-server audience.
	Issuer = "https://portal.kittypaw.app/auth"

	ClaimsVersion = 2
)

// DefaultAPIClientScopes is the scope set granted to OAuth-issued
// access tokens (web/CLI users).
var DefaultAPIClientScopes = []string{ScopeChatRelay, ScopeModelsRead}

// DefaultAPIClientAudiences is the audience set for OAuth-issued access tokens.
var DefaultAPIClientAudiences = []string{AudienceAPI, AudienceChat, AudienceSpace}
