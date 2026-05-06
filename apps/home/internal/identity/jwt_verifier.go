package identity

import (
	"context"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

type JWTVerifierConfig struct {
	Secret       string
	JWKSURL      string
	Issuer       string
	Audience     string
	HTTPClient   *http.Client
	CacheTTL     time.Duration
	Leeway       time.Duration
	FetchTimeout time.Duration
}

type JWTCredentialVerifier struct {
	secret         []byte
	jwksURL        string
	issuer         string
	audience       string
	httpClient     *http.Client
	cacheTTL       time.Duration
	leeway         time.Duration
	fetchTimeout   time.Duration
	mu             sync.Mutex
	keys           map[string]*rsa.PublicKey
	cacheExpiresAt time.Time
	lastKIDRefetch time.Time
}

type jwtCredentialClaims struct {
	Scope           []string `json:"scope,omitempty"`
	Version         int      `json:"v,omitempty"`
	UserID          string   `json:"user_id,omitempty"`
	DeviceID        string   `json:"device_id,omitempty"`
	AccountID       string   `json:"account_id,omitempty"`
	LocalAccountIDs []string `json:"local_accounts,omitempty"`
	jwt.RegisteredClaims
}

var errJWKNotFound = errors.New("jwk not found")

func NewJWTCredentialVerifier(cfg JWTVerifierConfig) (*JWTCredentialVerifier, error) {
	if cfg.Secret == "" && cfg.JWKSURL == "" {
		return nil, fmt.Errorf("jwt secret or jwks url is required")
	}
	if cfg.Issuer == "" {
		cfg.Issuer = IssuerKittyAPI
	}
	if cfg.Audience == "" {
		cfg.Audience = AudienceKittyHome
	}
	if cfg.HTTPClient == nil {
		cfg.HTTPClient = http.DefaultClient
	}
	if cfg.CacheTTL == 0 {
		cfg.CacheTTL = 10 * time.Minute
	}
	if cfg.Leeway == 0 {
		cfg.Leeway = time.Minute
	}
	if cfg.FetchTimeout == 0 {
		cfg.FetchTimeout = 5 * time.Second
	}
	return &JWTCredentialVerifier{
		secret:       []byte(cfg.Secret),
		jwksURL:      cfg.JWKSURL,
		issuer:       cfg.Issuer,
		audience:     cfg.Audience,
		httpClient:   cfg.HTTPClient,
		cacheTTL:     cfg.CacheTTL,
		leeway:       cfg.Leeway,
		fetchTimeout: cfg.FetchTimeout,
		keys:         make(map[string]*rsa.PublicKey),
	}, nil
}

func (v *JWTCredentialVerifier) VerifyAPIClient(ctx context.Context, token string) (APIClientClaims, error) {
	if err := ctx.Err(); err != nil {
		return APIClientClaims{}, err
	}
	claims, err := v.parse(ctx, token)
	if err != nil {
		return APIClientClaims{}, err
	}

	scopes := toScopes(claims.Scope)
	apiClaims := APIClientClaims{
		Subject:   claims.Subject,
		Audiences: []string(claims.Audience),
		Version:   claims.Version,
		Scopes:    scopes,
		UserID:    claims.Subject,
		DeviceID:  claims.DeviceID,
		AccountID: claims.AccountID,
	}
	if err := validateUserScopedAPIClientClaims(apiClaims); err != nil {
		return APIClientClaims{}, ErrUnauthorized
	}
	if !hasScope(scopes, ScopeChatRelay) && !hasScope(scopes, ScopeModelsRead) {
		return APIClientClaims{}, ErrUnauthorized
	}
	return apiClaims, nil
}

func (v *JWTCredentialVerifier) VerifyDevice(ctx context.Context, token string) (DeviceClaims, error) {
	if err := ctx.Err(); err != nil {
		return DeviceClaims{}, err
	}
	claims, err := v.parse(ctx, token)
	if err != nil {
		return DeviceClaims{}, err
	}

	scopes := toScopes(claims.Scope)
	deviceID := claims.DeviceID
	if deviceID == "" {
		var ok bool
		deviceID, ok = deviceIDFromSubject(claims.Subject)
		if !ok {
			return DeviceClaims{}, ErrUnauthorized
		}
	}
	deviceClaims := DeviceClaims{
		Subject:         claims.Subject,
		Audiences:       []string(claims.Audience),
		Version:         claims.Version,
		Scopes:          scopes,
		UserID:          claims.UserID,
		DeviceID:        deviceID,
		LocalAccountIDs: append([]string(nil), claims.LocalAccountIDs...),
	}
	if err := validateDeviceClaims(deviceClaims); err != nil {
		return DeviceClaims{}, ErrUnauthorized
	}
	if !hasScope(scopes, ScopeDaemonConnect) {
		return DeviceClaims{}, ErrUnauthorized
	}
	return deviceClaims, nil
}

func (v *JWTCredentialVerifier) parse(ctx context.Context, tokenString string) (*jwtCredentialClaims, error) {
	if tokenString == "" {
		return nil, ErrUnauthorized
	}
	claims := &jwtCredentialClaims{}
	token, err := jwt.ParseWithClaims(
		tokenString,
		claims,
		func(token *jwt.Token) (any, error) {
			switch token.Method {
			case jwt.SigningMethodHS256:
				if len(v.secret) == 0 {
					return nil, fmt.Errorf("hs256 credentials are not configured")
				}
				return v.secret, nil
			case jwt.SigningMethodRS256:
				return v.lookupRS256Key(ctx, token)
			default:
				return nil, fmt.Errorf("unexpected signing method: %v", token.Header["alg"])
			}
		},
		jwt.WithIssuer(v.issuer),
		jwt.WithAudience(v.audience),
		jwt.WithLeeway(v.leeway),
		jwt.WithIssuedAt(),
	)
	if err != nil {
		return nil, ErrUnauthorized
	}
	if !token.Valid || claims.ExpiresAt == nil || claims.Subject == "" {
		return nil, ErrUnauthorized
	}
	switch token.Method {
	case jwt.SigningMethodHS256:
		if claims.Version != CredentialVersion1 {
			return nil, ErrUnauthorized
		}
	case jwt.SigningMethodRS256:
		if claims.Version != CredentialVersion2 {
			return nil, ErrUnauthorized
		}
	default:
		return nil, ErrUnauthorized
	}
	return claims, nil
}

func (v *JWTCredentialVerifier) lookupRS256Key(ctx context.Context, token *jwt.Token) (*rsa.PublicKey, error) {
	if v.jwksURL == "" {
		return nil, fmt.Errorf("jwks url is not configured")
	}
	kid, _ := token.Header["kid"].(string)
	if kid == "" {
		return nil, fmt.Errorf("kid header is required")
	}
	key, err := v.cachedKey(ctx, kid, false)
	if err == nil {
		return key, nil
	}
	if !errors.Is(err, errJWKNotFound) {
		return nil, err
	}
	return v.cachedKey(ctx, kid, true)
}

func (v *JWTCredentialVerifier) cachedKey(ctx context.Context, kid string, forceRefresh bool) (*rsa.PublicKey, error) {
	now := time.Now()

	v.mu.Lock()
	if !forceRefresh && len(v.keys) > 0 && now.Before(v.cacheExpiresAt) {
		key, ok := v.keys[kid]
		v.mu.Unlock()
		if !ok {
			return nil, errJWKNotFound
		}
		return key, nil
	}
	if forceRefresh && now.Sub(v.lastKIDRefetch) < time.Second {
		key, ok := v.keys[kid]
		v.mu.Unlock()
		if !ok {
			return nil, errJWKNotFound
		}
		return key, nil
	}
	if forceRefresh {
		v.lastKIDRefetch = now
	}
	v.mu.Unlock()

	keys, err := v.fetchKeys(ctx)

	v.mu.Lock()
	defer v.mu.Unlock()
	if err == nil {
		v.keys = keys
		v.cacheExpiresAt = time.Now().Add(v.cacheTTL)
	}
	key, ok := v.keys[kid]
	if ok {
		return key, nil
	}
	if err != nil {
		return nil, err
	}
	return nil, errJWKNotFound
}

func (v *JWTCredentialVerifier) fetchKeys(ctx context.Context) (map[string]*rsa.PublicKey, error) {
	fetchCtx, cancel := context.WithTimeout(ctx, v.fetchTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(fetchCtx, http.MethodGet, v.jwksURL, nil)
	if err != nil {
		return nil, err
	}
	resp, err := v.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("jwks status %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, err
	}
	return parseJWKSet(body)
}

type jwkSet struct {
	Keys []jwkKey `json:"keys"`
}

type jwkKey struct {
	Kty string `json:"kty"`
	Use string `json:"use,omitempty"`
	Alg string `json:"alg,omitempty"`
	Kid string `json:"kid"`
	N   string `json:"n"`
	E   string `json:"e"`
}

func parseJWKSet(raw []byte) (map[string]*rsa.PublicKey, error) {
	var set jwkSet
	if err := json.Unmarshal(raw, &set); err != nil {
		return nil, err
	}
	keys := make(map[string]*rsa.PublicKey, len(set.Keys))
	for _, key := range set.Keys {
		if key.Kid == "" || key.Kty != "RSA" {
			continue
		}
		if key.Use != "" && key.Use != "sig" {
			continue
		}
		if key.Alg != "" && key.Alg != "RS256" {
			continue
		}
		publicKey, err := rsaPublicKeyFromJWK(key)
		if err != nil {
			return nil, err
		}
		keys[key.Kid] = publicKey
	}
	if len(keys) == 0 {
		return nil, fmt.Errorf("jwks contains no usable rsa signing keys")
	}
	return keys, nil
}

func rsaPublicKeyFromJWK(key jwkKey) (*rsa.PublicKey, error) {
	nBytes, err := base64.RawURLEncoding.DecodeString(key.N)
	if err != nil {
		return nil, fmt.Errorf("decode jwk modulus: %w", err)
	}
	eBytes, err := base64.RawURLEncoding.DecodeString(key.E)
	if err != nil {
		return nil, fmt.Errorf("decode jwk exponent: %w", err)
	}
	n := new(big.Int).SetBytes(nBytes)
	e := new(big.Int).SetBytes(eBytes)
	if n.Sign() <= 0 || e.Sign() <= 0 || !e.IsInt64() {
		return nil, fmt.Errorf("invalid rsa jwk")
	}
	return &rsa.PublicKey{N: n, E: int(e.Int64())}, nil
}

func deviceIDFromSubject(subject string) (string, bool) {
	deviceID, ok := strings.CutPrefix(subject, "device:")
	if !ok || deviceID == "" {
		return "", false
	}
	return deviceID, true
}

func toScopes(raw []string) []Scope {
	scopes := make([]Scope, 0, len(raw))
	for _, scope := range raw {
		typed := Scope(scope)
		if knownScope(typed) {
			scopes = append(scopes, typed)
		}
	}
	return scopes
}

func hasScope(scopes []Scope, want Scope) bool {
	return slices.Contains(scopes, want)
}

func validateUserScopedAPIClientClaims(claims APIClientClaims) error {
	if err := validateCommonClaims(claims.Subject, claims.Audiences, claims.Version, claims.Scopes); err != nil {
		return err
	}
	if claims.UserID == "" {
		return fmt.Errorf("user_id is required")
	}
	return nil
}
