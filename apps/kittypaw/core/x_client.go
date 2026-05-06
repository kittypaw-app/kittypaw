package core

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
)

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

type XClient struct {
	baseURL string
	client  *http.Client
}

func NewXClient(baseURL string, client *http.Client) *XClient {
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}
	if baseURL == "" {
		baseURL = "https://api.x.com/2"
	}
	return &XClient{baseURL: strings.TrimRight(baseURL, "/"), client: client}
}

func (c *XClient) SearchRecent(ctx context.Context, accessToken, query string, maxResults int) (XPostsResult, error) {
	req, err := c.newRequest(ctx, accessToken, http.MethodGet, "/tweets/search/recent")
	if err != nil {
		return XPostsResult{}, err
	}
	q := req.URL.Query()
	q.Set("query", strings.TrimSpace(query))
	q.Set("max_results", strconv.Itoa(normalizeXMaxResults(maxResults)))
	addXPostFields(q)
	req.URL.RawQuery = q.Encode()
	return c.doPosts(req)
}

func (c *XClient) UserByUsername(ctx context.Context, accessToken, username string) (XUser, error) {
	clean := strings.TrimPrefix(strings.TrimSpace(username), "@")
	req, err := c.newRequest(ctx, accessToken, http.MethodGet, "/users/by/username/"+url.PathEscape(clean))
	if err != nil {
		return XUser{}, err
	}
	q := req.URL.Query()
	q.Set("user.fields", "username,name,verified")
	req.URL.RawQuery = q.Encode()

	resp, err := c.client.Do(req)
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

func (c *XClient) UserPosts(ctx context.Context, accessToken, userID string, maxResults int) (XPostsResult, error) {
	req, err := c.newRequest(ctx, accessToken, http.MethodGet, "/users/"+url.PathEscape(strings.TrimSpace(userID))+"/tweets")
	if err != nil {
		return XPostsResult{}, err
	}
	q := req.URL.Query()
	q.Set("max_results", strconv.Itoa(normalizeXMaxResults(maxResults)))
	addXPostFields(q)
	req.URL.RawQuery = q.Encode()
	return c.doPosts(req)
}

func (c *XClient) newRequest(ctx context.Context, accessToken, method, path string) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	return req, nil
}

func (c *XClient) doPosts(req *http.Request) (XPostsResult, error) {
	resp, err := c.client.Do(req)
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
	users := make(map[string]XUser, len(body.Includes.Users))
	for _, user := range body.Includes.Users {
		users[user.ID] = user
	}
	posts := make([]XPost, 0, len(body.Data))
	for _, post := range body.Data {
		if user, ok := users[post.AuthorID]; ok {
			u := user
			post.Author = &u
		}
		posts = append(posts, post)
	}
	return XPostsResult{Posts: posts}, nil
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

func xStatusError(resp *http.Response) error {
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return nil
	}
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		return fmt.Errorf("x authorization failed (%d); run: kittypaw connect x", resp.StatusCode)
	}
	return fmt.Errorf("x response %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
}
