package connect

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
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
	APIBaseURL   string
	AuthURL      string
	TokenURL     string
	UserInfoURL  string
}

type XProvider struct {
	cfg    XConfig
	client *http.Client
}

type XStatusError struct {
	StatusCode int
	Body       string
	Type       string
	Title      string
	Detail     string
}

func (e *XStatusError) Error() string {
	if e == nil {
		return ""
	}
	if e.Body != "" {
		return fmt.Sprintf("x response %d: %s", e.StatusCode, e.Body)
	}
	return fmt.Sprintf("x response %d", e.StatusCode)
}

func (e *XStatusError) CreditsDepleted() bool {
	if e == nil || e.StatusCode != http.StatusPaymentRequired {
		return false
	}
	title := strings.ToLower(e.Title)
	typ := strings.ToLower(e.Type)
	body := strings.ToLower(e.Body)
	return strings.Contains(title, "creditsdepleted") ||
		strings.Contains(typ, "/credits") ||
		strings.Contains(body, "creditsdepleted")
}

func NewXProvider(cfg XConfig, client *http.Client) *XProvider {
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}
	cfg.BaseURL = strings.TrimRight(cfg.BaseURL, "/")
	cfg.APIBaseURL = strings.TrimRight(cfg.APIBaseURL, "/")
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

type XUser struct {
	ID       string `json:"id"`
	Username string `json:"username"`
	Name     string `json:"name"`
	Verified bool   `json:"verified,omitempty"`
}

type XPost struct {
	ID            string           `json:"id"`
	Text          string           `json:"text"`
	AuthorID      string           `json:"author_id,omitempty"`
	CreatedAt     string           `json:"created_at,omitempty"`
	Lang          string           `json:"lang,omitempty"`
	PublicMetrics map[string]int64 `json:"public_metrics,omitempty"`
	Author        *XUser           `json:"author,omitempty"`
}

type XPostsResult struct {
	Posts []XPost `json:"posts"`
}

func (p *XProvider) Me(ctx context.Context, accessToken string) (XUser, error) {
	req, err := p.newAPIRequest(ctx, accessToken, http.MethodGet, "/users/me")
	if err != nil {
		return XUser{}, err
	}
	q := req.URL.Query()
	q.Set("user.fields", "username,name,verified")
	req.URL.RawQuery = q.Encode()
	resp, err := p.client.Do(req)
	if err != nil {
		return XUser{}, fmt.Errorf("x me request: %w", err)
	}
	defer resp.Body.Close()
	if err := xStatusError(resp); err != nil {
		return XUser{}, err
	}
	var body struct {
		Data XUser `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return XUser{}, fmt.Errorf("decode x me: %w", err)
	}
	return body.Data, nil
}

func (p *XProvider) SearchRecent(ctx context.Context, accessToken, query string, maxResults int) (XPostsResult, error) {
	req, err := p.newAPIRequest(ctx, accessToken, http.MethodGet, "/tweets/search/recent")
	if err != nil {
		return XPostsResult{}, err
	}
	q := req.URL.Query()
	q.Set("query", strings.TrimSpace(query))
	q.Set("max_results", strconv.Itoa(normalizeXMaxResults(maxResults)))
	addXPostFields(q)
	req.URL.RawQuery = q.Encode()
	return p.doPosts(req)
}

func (p *XProvider) UserByUsername(ctx context.Context, accessToken, username string) (XUser, error) {
	clean := strings.TrimPrefix(strings.TrimSpace(username), "@")
	req, err := p.newAPIRequest(ctx, accessToken, http.MethodGet, "/users/by/username/"+url.PathEscape(clean))
	if err != nil {
		return XUser{}, err
	}
	q := req.URL.Query()
	q.Set("user.fields", "username,name,verified")
	req.URL.RawQuery = q.Encode()
	resp, err := p.client.Do(req)
	if err != nil {
		return XUser{}, fmt.Errorf("x user request: %w", err)
	}
	defer resp.Body.Close()
	if err := xStatusError(resp); err != nil {
		return XUser{}, err
	}
	var body struct {
		Data XUser `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return XUser{}, fmt.Errorf("decode x user: %w", err)
	}
	return body.Data, nil
}

func (p *XProvider) UserPosts(ctx context.Context, accessToken, userID string, maxResults int) (XPostsResult, error) {
	req, err := p.newAPIRequest(ctx, accessToken, http.MethodGet, "/users/"+url.PathEscape(strings.TrimSpace(userID))+"/tweets")
	if err != nil {
		return XPostsResult{}, err
	}
	q := req.URL.Query()
	q.Set("max_results", strconv.Itoa(normalizeXMaxResults(maxResults)))
	addXPostFields(q)
	req.URL.RawQuery = q.Encode()
	return p.doPosts(req)
}

func (p *XProvider) HomeTimeline(ctx context.Context, accessToken, userID string, maxResults int) (XPostsResult, error) {
	req, err := p.newAPIRequest(ctx, accessToken, http.MethodGet, "/users/"+url.PathEscape(strings.TrimSpace(userID))+"/timelines/reverse_chronological")
	if err != nil {
		return XPostsResult{}, err
	}
	q := req.URL.Query()
	q.Set("max_results", strconv.Itoa(normalizeXHomeTimelineMaxResults(maxResults)))
	addXPostFields(q)
	req.URL.RawQuery = q.Encode()
	return p.doPosts(req)
}

func (p *XProvider) TweetByID(ctx context.Context, accessToken, id string) (XPost, error) {
	req, err := p.newAPIRequest(ctx, accessToken, http.MethodGet, "/tweets/"+url.PathEscape(strings.TrimSpace(id)))
	if err != nil {
		return XPost{}, err
	}
	q := req.URL.Query()
	addXPostFields(q)
	req.URL.RawQuery = q.Encode()
	resp, err := p.client.Do(req)
	if err != nil {
		return XPost{}, fmt.Errorf("x tweet request: %w", err)
	}
	defer resp.Body.Close()
	if err := xStatusError(resp); err != nil {
		return XPost{}, err
	}
	var body struct {
		Data     XPost `json:"data"`
		Includes struct {
			Users []XUser `json:"users"`
		} `json:"includes"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return XPost{}, fmt.Errorf("decode x tweet: %w", err)
	}
	post := body.Data
	for _, user := range body.Includes.Users {
		if user.ID == post.AuthorID {
			u := user
			post.Author = &u
			break
		}
	}
	return post, nil
}

func (p *XProvider) newAPIRequest(ctx context.Context, accessToken, method, path string) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, method, p.apiBaseURL()+path, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	return req, nil
}

func (p *XProvider) doPosts(req *http.Request) (XPostsResult, error) {
	resp, err := p.client.Do(req)
	if err != nil {
		return XPostsResult{}, fmt.Errorf("x posts request: %w", err)
	}
	defer resp.Body.Close()
	if err := xStatusError(resp); err != nil {
		return XPostsResult{}, err
	}
	var body struct {
		Data     []XPost `json:"data"`
		Includes struct {
			Users []XUser `json:"users"`
		} `json:"includes"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return XPostsResult{}, fmt.Errorf("decode x posts: %w", err)
	}
	attachXAuthors(body.Data, body.Includes.Users)
	return XPostsResult{Posts: body.Data}, nil
}

func attachXAuthors(posts []XPost, users []XUser) {
	byID := make(map[string]XUser, len(users))
	for _, user := range users {
		byID[user.ID] = user
	}
	for i := range posts {
		if user, ok := byID[posts[i].AuthorID]; ok {
			u := user
			posts[i].Author = &u
		}
	}
}

func addXPostFields(q url.Values) {
	q.Set("tweet.fields", "created_at,author_id,public_metrics,lang")
	q.Set("expansions", "author_id")
	q.Set("user.fields", "username,name,verified")
}

func normalizeXMaxResults(maxResults int) int {
	if maxResults < 10 {
		return 10
	}
	if maxResults > 100 {
		return 100
	}
	return maxResults
}

func normalizeXHomeTimelineMaxResults(maxResults int) int {
	if maxResults <= 0 {
		return 10
	}
	if maxResults > 100 {
		return 100
	}
	return maxResults
}

func xStatusError(resp *http.Response) error {
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return nil
	}
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
	raw := strings.TrimSpace(string(body))
	err := &XStatusError{StatusCode: resp.StatusCode, Body: raw}
	var parsed struct {
		Type   string `json:"type"`
		Title  string `json:"title"`
		Detail string `json:"detail"`
	}
	if json.Unmarshal(body, &parsed) == nil {
		err.Type = parsed.Type
		err.Title = parsed.Title
		err.Detail = parsed.Detail
	}
	return err
}

func (p *XProvider) redirectURL() string {
	return strings.TrimRight(p.cfg.BaseURL, "/") + "/connect/x/callback"
}

func (p *XProvider) apiBaseURL() string {
	if p.cfg.APIBaseURL != "" {
		return p.cfg.APIBaseURL
	}
	return "https://api.x.com/2"
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
