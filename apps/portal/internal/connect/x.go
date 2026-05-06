package connect

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/kittypaw-app/kittyportal/internal/auth"
)

const (
	XProviderID    = "x"
	XReadOnlyScope = "tweet.read users.read offline.access"

	defaultXAuthURL     = "https://x.com/i/oauth2/authorize"
	defaultXTokenURL    = "https://api.x.com/2/oauth2/token"
	defaultXUserInfoURL = "https://api.x.com/2/users/me"
)

type XConfig struct {
	ClientID     string
	ClientSecret string
	BaseURL      string
	AuthURL      string
	TokenURL     string
	UserInfoURL  string
}

type XProvider struct {
	cfg    XConfig
	client *http.Client
}

func NewXProvider(cfg XConfig, client *http.Client) *XProvider {
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}
	cfg.BaseURL = strings.TrimRight(cfg.BaseURL, "/")
	return &XProvider{cfg: cfg, client: client}
}

func (p *XProvider) AuthURL(state, verifier string) string {
	params := url.Values{
		"response_type":         {"code"},
		"client_id":             {p.cfg.ClientID},
		"redirect_uri":          {p.redirectURL()},
		"scope":                 {XReadOnlyScope},
		"state":                 {state},
		"code_challenge":        {auth.ChallengeS256(verifier)},
		"code_challenge_method": {"S256"},
	}
	return p.authURL() + "?" + params.Encode()
}

func (p *XProvider) ExchangeCode(ctx context.Context, code, verifier string) (TokenSet, error) {
	values := url.Values{
		"code":          {code},
		"client_id":     {p.cfg.ClientID},
		"redirect_uri":  {p.redirectURL()},
		"grant_type":    {"authorization_code"},
		"code_verifier": {verifier},
	}
	tokens, err := p.postToken(ctx, values)
	if err != nil {
		return TokenSet{}, err
	}
	username, err := p.fetchUsername(ctx, tokens.AccessToken)
	if err != nil {
		return TokenSet{}, err
	}
	tokens.Username = username
	return tokens, nil
}

func (p *XProvider) Refresh(ctx context.Context, refreshToken string) (TokenSet, error) {
	values := url.Values{
		"client_id":     {p.cfg.ClientID},
		"refresh_token": {refreshToken},
		"grant_type":    {"refresh_token"},
	}
	return p.postToken(ctx, values)
}

func (p *XProvider) postToken(ctx context.Context, values url.Values) (TokenSet, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.tokenURL(), strings.NewReader(values.Encode()))
	if err != nil {
		return TokenSet{}, fmt.Errorf("build token request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if p.cfg.ClientSecret != "" {
		req.SetBasicAuth(p.cfg.ClientID, p.cfg.ClientSecret)
	}
	resp, err := p.client.Do(req)
	if err != nil {
		return TokenSet{}, fmt.Errorf("token request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return TokenSet{}, fmt.Errorf("token response %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	var body struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		TokenType    string `json:"token_type"`
		ExpiresIn    int    `json:"expires_in"`
		Scope        string `json:"scope"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return TokenSet{}, fmt.Errorf("decode token: %w", err)
	}
	if body.TokenType == "" {
		body.TokenType = "Bearer"
	}
	return TokenSet{
		Provider:     XProviderID,
		AccessToken:  body.AccessToken,
		RefreshToken: body.RefreshToken,
		TokenType:    body.TokenType,
		ExpiresIn:    body.ExpiresIn,
		Scope:        body.Scope,
		IssuedAt:     time.Now().UTC(),
	}, nil
}

func (p *XProvider) fetchUsername(ctx context.Context, accessToken string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, p.userInfoURL(), nil)
	if err != nil {
		return "", fmt.Errorf("build userinfo request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	resp, err := p.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("userinfo request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("userinfo response %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	var body struct {
		Data struct {
			Username string `json:"username"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return "", fmt.Errorf("decode userinfo: %w", err)
	}
	return body.Data.Username, nil
}

func (p *XProvider) redirectURL() string {
	return strings.TrimRight(p.cfg.BaseURL, "/") + "/connect/x/callback"
}

func (p *XProvider) authURL() string {
	if p.cfg.AuthURL != "" {
		return p.cfg.AuthURL
	}
	return defaultXAuthURL
}

func (p *XProvider) tokenURL() string {
	if p.cfg.TokenURL != "" {
		return p.cfg.TokenURL
	}
	return defaultXTokenURL
}

func (p *XProvider) userInfoURL() string {
	if p.cfg.UserInfoURL != "" {
		return p.cfg.UserInfoURL
	}
	return defaultXUserInfoURL
}
