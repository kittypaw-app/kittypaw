package identity

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/kittypaw-app/kittyspace/internal/broker"
	"github.com/kittypaw-app/kittyspace/internal/openai"
)

const (
	AudienceKittyAPI   = "https://api.kittypaw.app"
	AudienceKittySpace = "https://space.kittypaw.app"
	IssuerKittyAPI     = "https://portal.kittypaw.app/auth"

	CredentialVersion1 = 1
	CredentialVersion2 = 2
)

type Scope string

const (
	ScopeChatRelay     Scope = "chat:relay"
	ScopeModelsRead    Scope = "models:read"
	ScopeDaemonConnect Scope = "daemon:connect"
)

var ErrUnauthorized = errors.New("unauthorized")

type APIClientClaims struct {
	Subject   string
	Audiences []string
	Version   int
	Scopes    []Scope
	UserID    string
	DeviceID  string
	AccountID string
}

func (c APIClientClaims) Principal() openai.Principal {
	return openai.Principal{
		UserID:    c.UserID,
		DeviceID:  c.DeviceID,
		AccountID: c.AccountID,
		Scopes:    scopeStrings(c.Scopes),
	}
}

type DeviceClaims struct {
	Subject         string
	Audiences       []string
	Version         int
	Scopes          []Scope
	UserID          string
	DeviceID        string
	LocalAccountIDs []string
}

func (c DeviceClaims) Principal() broker.DevicePrincipal {
	return broker.DevicePrincipal{
		UserID:          c.UserID,
		DeviceID:        c.DeviceID,
		LocalAccountIDs: append([]string(nil), c.LocalAccountIDs...),
	}
}

type CredentialVerifier interface {
	VerifyAPIClient(ctx context.Context, token string) (APIClientClaims, error)
	VerifyDevice(ctx context.Context, token string) (DeviceClaims, error)
}

type SplitCredentialVerifier struct {
	API    CredentialVerifier
	Device CredentialVerifier
}

func (v SplitCredentialVerifier) VerifyAPIClient(ctx context.Context, token string) (APIClientClaims, error) {
	if v.API == nil {
		return APIClientClaims{}, ErrUnauthorized
	}
	return v.API.VerifyAPIClient(ctx, token)
}

func (v SplitCredentialVerifier) VerifyDevice(ctx context.Context, token string) (DeviceClaims, error) {
	if v.Device == nil {
		return DeviceClaims{}, ErrUnauthorized
	}
	return v.Device.VerifyDevice(ctx, token)
}

type ChainCredentialVerifier struct {
	Verifiers []CredentialVerifier
}

func (v ChainCredentialVerifier) VerifyAPIClient(ctx context.Context, token string) (APIClientClaims, error) {
	for _, verifier := range v.Verifiers {
		if verifier == nil {
			continue
		}
		claims, err := verifier.VerifyAPIClient(ctx, token)
		if err == nil {
			return claims, nil
		}
		if !errors.Is(err, ErrUnauthorized) {
			return APIClientClaims{}, err
		}
	}
	return APIClientClaims{}, ErrUnauthorized
}

func (v ChainCredentialVerifier) VerifyDevice(ctx context.Context, token string) (DeviceClaims, error) {
	for _, verifier := range v.Verifiers {
		if verifier == nil {
			continue
		}
		claims, err := verifier.VerifyDevice(ctx, token)
		if err == nil {
			return claims, nil
		}
		if !errors.Is(err, ErrUnauthorized) {
			return DeviceClaims{}, err
		}
	}
	return DeviceClaims{}, ErrUnauthorized
}

type MemoryCredentialVerifier struct {
	mu      sync.RWMutex
	api     map[string]APIClientClaims
	devices map[string]DeviceClaims
}

func NewMemoryCredentialVerifier() *MemoryCredentialVerifier {
	return &MemoryCredentialVerifier{
		api:     make(map[string]APIClientClaims),
		devices: make(map[string]DeviceClaims),
	}
}

func (v *MemoryCredentialVerifier) AddAPIClient(token string, claims APIClientClaims) error {
	if token == "" {
		return fmt.Errorf("api token is required")
	}
	if err := validateAPIClientClaims(claims); err != nil {
		return err
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	v.api[token] = cloneAPIClientClaims(claims)
	return nil
}

func (v *MemoryCredentialVerifier) AddDevice(token string, claims DeviceClaims) error {
	if token == "" {
		return fmt.Errorf("device token is required")
	}
	if err := validateDeviceClaims(claims); err != nil {
		return err
	}
	if len(claims.LocalAccountIDs) == 0 {
		return fmt.Errorf("at least one local account is required")
	}
	for _, accountID := range claims.LocalAccountIDs {
		if accountID == "" {
			return fmt.Errorf("local account id is required")
		}
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	v.devices[token] = cloneDeviceClaims(claims)
	return nil
}

func (v *MemoryCredentialVerifier) VerifyAPIClient(ctx context.Context, token string) (APIClientClaims, error) {
	if err := ctx.Err(); err != nil {
		return APIClientClaims{}, err
	}
	if token == "" {
		return APIClientClaims{}, ErrUnauthorized
	}
	v.mu.RLock()
	defer v.mu.RUnlock()
	claims, ok := v.api[token]
	if !ok {
		return APIClientClaims{}, ErrUnauthorized
	}
	return cloneAPIClientClaims(claims), nil
}

func (v *MemoryCredentialVerifier) VerifyDevice(ctx context.Context, token string) (DeviceClaims, error) {
	if err := ctx.Err(); err != nil {
		return DeviceClaims{}, err
	}
	if token == "" {
		return DeviceClaims{}, ErrUnauthorized
	}
	v.mu.RLock()
	defer v.mu.RUnlock()
	claims, ok := v.devices[token]
	if !ok {
		return DeviceClaims{}, ErrUnauthorized
	}
	return cloneDeviceClaims(claims), nil
}

func validateAPIClientClaims(claims APIClientClaims) error {
	if err := validateCommonClaims(claims.Subject, claims.Audiences, claims.Version, claims.Scopes); err != nil {
		return err
	}
	if claims.UserID == "" {
		return fmt.Errorf("user_id is required")
	}
	if claims.DeviceID == "" {
		return fmt.Errorf("device_id is required")
	}
	if claims.AccountID == "" {
		return fmt.Errorf("account_id is required")
	}
	return nil
}

func validateDeviceClaims(claims DeviceClaims) error {
	if err := validateCommonClaims(claims.Subject, claims.Audiences, claims.Version, claims.Scopes); err != nil {
		return err
	}
	if claims.UserID == "" {
		return fmt.Errorf("user_id is required")
	}
	if claims.DeviceID == "" {
		return fmt.Errorf("device_id is required")
	}
	if claims.Subject != "device:"+claims.DeviceID {
		return fmt.Errorf("device subject must match device_id")
	}
	return nil
}

func validateCommonClaims(subject string, audiences []string, version int, scopes []Scope) error {
	if subject == "" {
		return fmt.Errorf("subject is required")
	}
	if !hasAudience(audiences, AudienceKittySpace) {
		return fmt.Errorf("audience must include %q", AudienceKittySpace)
	}
	if version != CredentialVersion1 && version != CredentialVersion2 {
		return fmt.Errorf("unsupported credential version %d", version)
	}
	if len(scopes) == 0 {
		return fmt.Errorf("at least one scope is required")
	}
	for _, scope := range scopes {
		if !knownScope(scope) {
			return fmt.Errorf("unknown scope %q", scope)
		}
	}
	return nil
}

func hasAudience(audiences []string, want string) bool {
	for _, audience := range audiences {
		if audience == want {
			return true
		}
	}
	return false
}

func knownScope(scope Scope) bool {
	switch scope {
	case ScopeChatRelay, ScopeModelsRead, ScopeDaemonConnect:
		return true
	default:
		return false
	}
}

func cloneAPIClientClaims(claims APIClientClaims) APIClientClaims {
	claims.Audiences = append([]string(nil), claims.Audiences...)
	claims.Scopes = append([]Scope(nil), claims.Scopes...)
	return claims
}

func cloneDeviceClaims(claims DeviceClaims) DeviceClaims {
	claims.Audiences = append([]string(nil), claims.Audiences...)
	claims.Scopes = append([]Scope(nil), claims.Scopes...)
	claims.LocalAccountIDs = append([]string(nil), claims.LocalAccountIDs...)
	return claims
}

func scopeStrings(scopes []Scope) []string {
	values := make([]string, 0, len(scopes))
	for _, scope := range scopes {
		values = append(values, string(scope))
	}
	return values
}
