package auth

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
)

const (
	defaultGoogleAuthURL     = "https://accounts.google.com/o/oauth2/v2/auth"
	defaultGoogleTokenURL    = "https://oauth2.googleapis.com/token"
	defaultGoogleUserInfoURL = "https://www.googleapis.com/oauth2/v2/userinfo"
)

type GoogleConfig struct {
	ClientID     string
	ClientSecret string
	RedirectURL  string
}

type googleUserInfo struct {
	ID      string `json:"id"`
	Email   string `json:"email"`
	Name    string `json:"name"`
	Picture string `json:"picture"`
}

func (h *OAuthHandler) googleAuthURL() string {
	if h.GoogleAuthURL != "" {
		return h.GoogleAuthURL
	}
	return defaultGoogleAuthURL
}

func (h *OAuthHandler) googleTokenURL() string {
	if h.GoogleTokenURL != "" {
		return h.GoogleTokenURL
	}
	return defaultGoogleTokenURL
}

func (h *OAuthHandler) googleUserInfoURL() string {
	if h.GoogleUserInfoURL != "" {
		return h.GoogleUserInfoURL
	}
	return defaultGoogleUserInfoURL
}

func (h *OAuthHandler) HandleGoogleLogin(cfg GoogleConfig) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		verifier, err := GenerateVerifier()
		if err != nil {
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}

		state, err := h.StateStore.Create(verifier)
		if err != nil {
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}

		params := url.Values{
			"client_id":             {cfg.ClientID},
			"redirect_uri":          {cfg.RedirectURL},
			"response_type":         {"code"},
			"scope":                 {"openid email profile"},
			"state":                 {state},
			"code_challenge":        {ChallengeS256(verifier)},
			"code_challenge_method": {"S256"},
			"access_type":           {"offline"},
		}

		http.Redirect(w, r, h.googleAuthURL()+"?"+params.Encode(), http.StatusFound)
	}
}

func (h *OAuthHandler) HandleGoogleCallback(cfg GoogleConfig) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		code := r.URL.Query().Get("code")
		state := r.URL.Query().Get("state")

		if code == "" || state == "" {
			http.Error(w, "missing code or state", http.StatusBadRequest)
			return
		}

		verifier, meta, err := h.StateStore.ConsumeMeta(state)
		if err != nil {
			http.Error(w, "invalid state", http.StatusBadRequest)
			return
		}

		token, err := h.exchangeGoogleCode(cfg, code, verifier)
		if err != nil {
			log.Printf("google code exchange: %v", err)
			http.Error(w, "authentication failed", http.StatusBadGateway)
			return
		}

		info, err := h.fetchGoogleUserInfo(token)
		if err != nil {
			log.Printf("google userinfo: %v", err)
			http.Error(w, "authentication failed", http.StatusBadGateway)
			return
		}

		user, err := h.UserStore.CreateOrUpdate(r.Context(), "google", info.ID, info.Email, info.Name, info.Picture)
		if err != nil {
			log.Printf("user upsert: %v", err)
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}

		// Flow-specific dispatch. Standard browser flow leaves meta nil
		// (StateStore.Create) and falls through to issueTokens.
		if meta != nil {
			switch meta[stateMetaKeyMode] {
			case stateMetaModeWeb:
				h.emitWebCallback(w, r, user, meta)
				return
			case stateMetaModeAdmin:
				h.emitAdminCallback(w, r, user, meta)
				return
			}
		}

		h.issueTokens(w, r, user)
	}
}

func (h *OAuthHandler) exchangeGoogleCode(cfg GoogleConfig, code, verifier string) (string, error) {
	resp, err := h.HTTPClient.PostForm(h.googleTokenURL(), url.Values{
		"code":          {code},
		"client_id":     {cfg.ClientID},
		"client_secret": {cfg.ClientSecret},
		"redirect_uri":  {cfg.RedirectURL},
		"grant_type":    {"authorization_code"},
		"code_verifier": {verifier},
	})
	if err != nil {
		return "", fmt.Errorf("token request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("token response %d: %s", resp.StatusCode, body)
	}

	var result struct {
		AccessToken string `json:"access_token"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", fmt.Errorf("decode token: %w", err)
	}
	return result.AccessToken, nil
}

func (h *OAuthHandler) fetchGoogleUserInfo(accessToken string) (*googleUserInfo, error) {
	req, err := http.NewRequest(http.MethodGet, h.googleUserInfoURL(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)

	resp, err := h.HTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("userinfo request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("userinfo response %d: %s", resp.StatusCode, body)
	}

	var info googleUserInfo
	if err := json.NewDecoder(resp.Body).Decode(&info); err != nil {
		return nil, fmt.Errorf("decode userinfo: %w", err)
	}
	return &info, nil
}
