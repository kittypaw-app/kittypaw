package connectadmin

import "time"

const (
	DefaultEntitlementAllow = "allow"
	DefaultEntitlementDeny  = "deny"

	VerificationUnknown       = "unknown"
	VerificationNotApplicable = "not_applicable"
	VerificationTesting       = "testing"
	VerificationSubmitted     = "submitted"
	VerificationVerified      = "verified"
	VerificationBlocked       = "blocked"

	CostModeNone           = "none"
	CostModeExternalPolicy = "external_policy"
	CostModeKittyPaid      = "kitty_paid"

	EntitlementAllowed = "allowed"
	EntitlementBlocked = "blocked"
	EntitlementRevoked = "revoked"
)

type ProviderPolicy struct {
	ProviderID         string
	Enabled            bool
	DefaultEntitlement string
	RequestedScopes    []string
	VerificationStatus string
	CostMode           string
	Notes              string
	UpdatedBy          string
	UpdatedAt          time.Time
}

type UserEntitlement struct {
	ID         string
	UserID     string
	ProviderID string
	Status     string
	QuotaJSON  map[string]any
	Reason     string
	GrantedBy  string
	GrantedAt  time.Time
	RevokedAt  *time.Time
}

type AuditEvent struct {
	ID           string
	ActorUserID  string
	Action       string
	ProviderID   string
	TargetUserID string
	Before       map[string]any
	After        map[string]any
	CreatedAt    time.Time
}
