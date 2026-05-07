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

type XBrokerClient struct {
	baseURL string
	client  *http.Client
}

type XBrokerStatusError struct {
	StatusCode int
	Code       string
	Message    string
}

func (e *XBrokerStatusError) Error() string {
	if e == nil {
		return ""
	}
	if e.Code != "" && e.Message != "" {
		return fmt.Sprintf("x broker %d %s: %s", e.StatusCode, e.Code, e.Message)
	}
	if e.Message != "" {
		return fmt.Sprintf("x broker %d: %s", e.StatusCode, e.Message)
	}
	return fmt.Sprintf("x broker response %d", e.StatusCode)
}

func NewXBrokerClient(baseURL string, client *http.Client) *XBrokerClient {
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}
	if baseURL == "" {
		baseURL = "https://connect.kittypaw.app"
	}
	return &XBrokerClient{baseURL: strings.TrimRight(baseURL, "/"), client: client}
}

func (c *XBrokerClient) SearchRecent(ctx context.Context, accessToken, query string, maxResults int) (XPostsResult, error) {
	req, err := c.newRequest(ctx, accessToken, http.MethodGet, "/connect/x/broker/search/recent")
	if err != nil {
		return XPostsResult{}, err
	}
	q := req.URL.Query()
	q.Set("query", strings.TrimSpace(query))
	q.Set("limit", strconv.Itoa(brokerSkillLimit(maxResults)))
	req.URL.RawQuery = q.Encode()
	return c.doPosts(req)
}

func (c *XBrokerClient) UserByUsername(ctx context.Context, accessToken, username string) (XUser, error) {
	clean := strings.TrimPrefix(strings.TrimSpace(username), "@")
	req, err := c.newRequest(ctx, accessToken, http.MethodGet, "/connect/x/broker/users/by/username/"+url.PathEscape(clean))
	if err != nil {
		return XUser{}, err
	}
	resp, err := c.client.Do(req)
	if err != nil {
		return XUser{}, fmt.Errorf("x broker user request: %w", err)
	}
	defer resp.Body.Close()
	if err := brokerStatusError(resp); err != nil {
		return XUser{}, err
	}
	var user XUser
	if err := json.NewDecoder(resp.Body).Decode(&user); err != nil {
		return XUser{}, fmt.Errorf("decode x broker user: %w", err)
	}
	return user, nil
}

func (c *XBrokerClient) UserPostsByUsername(ctx context.Context, accessToken, username string, maxResults int) (XPostsResult, error) {
	clean := strings.TrimPrefix(strings.TrimSpace(username), "@")
	req, err := c.newRequest(ctx, accessToken, http.MethodGet, "/connect/x/broker/users/by/username/"+url.PathEscape(clean)+"/tweets")
	if err != nil {
		return XPostsResult{}, err
	}
	q := req.URL.Query()
	q.Set("limit", strconv.Itoa(brokerSkillLimit(maxResults)))
	req.URL.RawQuery = q.Encode()
	return c.doPosts(req)
}

func (c *XBrokerClient) HomeTimeline(ctx context.Context, accessToken string, maxResults int) (XPostsResult, error) {
	req, err := c.newRequest(ctx, accessToken, http.MethodGet, "/connect/x/broker/users/me/timelines/reverse_chronological")
	if err != nil {
		return XPostsResult{}, err
	}
	q := req.URL.Query()
	q.Set("limit", strconv.Itoa(brokerSkillLimit(maxResults)))
	req.URL.RawQuery = q.Encode()
	return c.doPosts(req)
}

func (c *XBrokerClient) TweetByID(ctx context.Context, accessToken, id string) (XPost, error) {
	req, err := c.newRequest(ctx, accessToken, http.MethodGet, "/connect/x/broker/tweets/"+url.PathEscape(strings.TrimSpace(id)))
	if err != nil {
		return XPost{}, err
	}
	resp, err := c.client.Do(req)
	if err != nil {
		return XPost{}, fmt.Errorf("x broker tweet request: %w", err)
	}
	defer resp.Body.Close()
	if err := brokerStatusError(resp); err != nil {
		return XPost{}, err
	}
	var post XPost
	if err := json.NewDecoder(resp.Body).Decode(&post); err != nil {
		return XPost{}, fmt.Errorf("decode x broker tweet: %w", err)
	}
	return post, nil
}

func (c *XBrokerClient) newRequest(ctx context.Context, accessToken, method, path string) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("Accept", "application/json")
	return req, nil
}

func (c *XBrokerClient) doPosts(req *http.Request) (XPostsResult, error) {
	resp, err := c.client.Do(req)
	if err != nil {
		return XPostsResult{}, fmt.Errorf("x broker posts request: %w", err)
	}
	defer resp.Body.Close()
	if err := brokerStatusError(resp); err != nil {
		return XPostsResult{}, err
	}
	var result XPostsResult
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return XPostsResult{}, fmt.Errorf("decode x broker posts: %w", err)
	}
	return result, nil
}

func brokerSkillLimit(maxResults int) int {
	if maxResults < 1 {
		return 10
	}
	if maxResults > 10 {
		return 10
	}
	return maxResults
}

func brokerStatusError(resp *http.Response) error {
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return nil
	}
	var body struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
	_ = json.Unmarshal(raw, &body)
	message := body.Error.Message
	if message == "" {
		message = strings.TrimSpace(string(raw))
	}
	return &XBrokerStatusError{
		StatusCode: resp.StatusCode,
		Code:       body.Error.Code,
		Message:    message,
	}
}
