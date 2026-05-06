package auth

import (
	"crypto/rsa"
	"encoding/json"
	"log"
	"net/http"
	"time"

	"github.com/kittypaw-app/kittyportal/internal/model"
)

const (
	AccessTokenTTL  = 15 * time.Minute
	RefreshTokenTTL = 7 * 24 * time.Hour

	// maxAuthBodyBytes caps the JSON request body for endpoints that take
	// a single short opaque token (refresh token, CLI exchange code).
	// Without this, an unauthenticated caller can stream a multi-MB body
	// before the handler rejects the lookup. Values fit comfortably:
	// refresh tokens are 43-char base64, CLI codes are 8 chars + dash.
	maxAuthBodyBytes = 1024
)

type TokenResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	TokenType    string `json:"token_type"`
	ExpiresIn    int    `json:"expires_in"`
}

type OAuthHandler struct {
	UserStore                model.UserStore
	RefreshTokenStore        model.RefreshTokenStore
	DeviceStore              model.DeviceStore // Plan 23 PR-D: device pairing/refresh/list/delete
	WebCodeStore             *WebCodeStore     // Plan 25: web OAuth one-time codes
	StateStore               *StateStore
	JWTPrivateKey            *rsa.PrivateKey // Plan 21 PR-B: HS256 secret → RS256 key
	JWTKID                   string          // RFC 7638 thumbprint, embedded in JWT header
	AdminSessionCookieSecure bool
	HTTPClient               *http.Client

	// Overridable for testing.
	GoogleAuthURL     string
	GoogleTokenURL    string
	GoogleUserInfoURL string
	GitHubTokenURL    string
	GitHubUserURL     string
}

func (h *OAuthHandler) issueTokens(w http.ResponseWriter, r *http.Request, user *model.User) {
	tokens, err := h.issueTokenPair(r.Context(), user)
	if err != nil {
		log.Printf("issue tokens: %v", err)
		http.Error(w, "token generation failed", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(tokens)
}
