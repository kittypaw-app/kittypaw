package engine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/jinto/kittypaw/core"
)

type xOptions struct {
	Query    string `json:"query"`
	Username string `json:"username"`
	ID       string `json:"id"`
	URL      string `json:"url"`
	Limit    int    `json:"limit"`
}

func executeX(ctx context.Context, call core.SkillCall, s *AccountRuntime) (string, error) {
	client, accessToken, errResult := xClientForSession(s)
	if errResult != "" {
		return errResult, nil
	}

	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	switch call.Method {
	case "searchRecent":
		query, limit := parseXQueryArgs(call.Args)
		if strings.TrimSpace(query) == "" {
			return jsonResult(map[string]any{"error": `X.searchRecent requires a query, e.g. X.searchRecent("kittypaw", {limit: 10})`})
		}
		limit = clampXSkillLimit(limit)
		result, err := client.SearchRecent(ctx, accessToken, query, limit)
		if err != nil {
			return jsonResult(map[string]any{"error": xBrokerErrorMessage(err, s)})
		}
		return jsonResult(map[string]any{
			"query": query,
			"limit": limit,
			"count": len(result.Posts),
			"posts": result.Posts,
		})
	case "homeTimeline":
		limit := clampXSkillLimit(parseXHomeTimelineArgs(call.Args))
		result, err := client.HomeTimeline(ctx, accessToken, limit)
		if err != nil {
			return jsonResult(map[string]any{"error": xBrokerErrorMessage(err, s)})
		}
		return jsonResult(map[string]any{
			"timeline": "home",
			"limit":    limit,
			"count":    len(result.Posts),
			"posts":    result.Posts,
		})
	case "user":
		username := parseXUsernameArgs(call.Args)
		if username == "" {
			return jsonResult(map[string]any{"error": `X.user requires a username, e.g. X.user("XDevelopers")`})
		}
		user, err := client.UserByUsername(ctx, accessToken, username)
		if err != nil {
			return jsonResult(map[string]any{"error": xBrokerErrorMessage(err, s)})
		}
		return jsonResult(map[string]any{"user": user})
	case "userPosts":
		username, limit := parseXUserPostsArgs(call.Args)
		if username == "" {
			return jsonResult(map[string]any{"error": `X.userPosts requires a username, e.g. X.userPosts("XDevelopers", {limit: 10})`})
		}
		limit = clampXSkillLimit(limit)
		user, err := client.UserByUsername(ctx, accessToken, username)
		if err != nil {
			return jsonResult(map[string]any{"error": xBrokerErrorMessage(err, s)})
		}
		result, err := client.UserPostsByUsername(ctx, accessToken, username, limit)
		if err != nil {
			return jsonResult(map[string]any{"error": xBrokerErrorMessage(err, s)})
		}
		return jsonResult(map[string]any{
			"username": strings.TrimPrefix(strings.TrimSpace(username), "@"),
			"user":     user,
			"limit":    limit,
			"count":    len(result.Posts),
			"posts":    result.Posts,
		})
	case "post", "tweet":
		id := parseXPostArgs(call.Args)
		if id == "" {
			return jsonResult(map[string]any{"error": `X.post requires a tweet/post ID or URL, e.g. X.post("2051255574848016682")`})
		}
		post, err := client.TweetByID(ctx, accessToken, id)
		if err != nil {
			return jsonResult(map[string]any{"error": xBrokerErrorMessage(err, s)})
		}
		return jsonResult(map[string]any{"id": id, "post": post})
	default:
		return jsonResult(map[string]any{"error": fmt.Sprintf("unknown X method: %s", call.Method)})
	}
}

func xClientForSession(s *AccountRuntime) (*core.XBrokerClient, string, string) {
	if s == nil || s.APITokenMgr == nil {
		return nil, "", jsonResultMust(map[string]any{"error": xLoginGuidance(s)})
	}
	apiURL := s.APITokenMgr.ResolveAPIURL()
	accessToken, err := s.APITokenMgr.LoadAccessToken(apiURL)
	if err != nil || accessToken == "" {
		msg := xLoginGuidance(s)
		if err != nil {
			msg = fmt.Sprintf("%s (%v)", msg, err)
		}
		return nil, "", jsonResultMust(map[string]any{"error": msg})
	}
	return core.NewXBrokerClient(s.APITokenMgr.ResolveConnectBaseURL(apiURL), nil), accessToken, ""
}

func xBrokerErrorMessage(err error, s *AccountRuntime) string {
	var statusErr *core.XBrokerStatusError
	if errors.As(err, &statusErr) {
		switch statusErr.StatusCode {
		case 401:
			return xLoginGuidance(s)
		case 403:
			return xConnectGuidance(s)
		case http.StatusPaymentRequired:
			if statusErr.Code == "x_credits_depleted" {
				return "x_credits_depleted: X API credits are depleted on KittyPaw's X developer account. This is not a login, connection, or server outage. Tell the user X lookup is unavailable until credits are refilled or available again; do not suggest immediate retry."
			}
		case 429:
			return "x monthly post read quota exceeded"
		}
	}
	return err.Error()
}

func xLoginGuidance(s *AccountRuntime) string {
	if s != nil && strings.TrimSpace(s.AccountID) != "" {
		return fmt.Sprintf("not logged in - run: kittypaw login --account %s", s.AccountID)
	}
	return "not logged in - run: kittypaw login"
}

func xConnectGuidance(s *AccountRuntime) string {
	if s != nil && strings.TrimSpace(s.AccountID) != "" {
		return fmt.Sprintf("x not connected - run: kittypaw connect x --account %s", s.AccountID)
	}
	return "x not connected - run: kittypaw connect x"
}

func parseXQueryArgs(args []json.RawMessage) (string, int) {
	query := ""
	limit := 10
	if len(args) == 0 {
		return query, limit
	}
	var firstString string
	if json.Unmarshal(args[0], &firstString) == nil {
		query = strings.TrimSpace(firstString)
		if len(args) > 1 {
			var opts xOptions
			if json.Unmarshal(args[1], &opts) == nil && opts.Limit != 0 {
				limit = opts.Limit
			}
		}
		return query, limit
	}
	var opts xOptions
	if json.Unmarshal(args[0], &opts) == nil {
		query = strings.TrimSpace(opts.Query)
		if opts.Limit != 0 {
			limit = opts.Limit
		}
	}
	return query, limit
}

func parseXUsernameArgs(args []json.RawMessage) string {
	if len(args) == 0 {
		return ""
	}
	var username string
	if json.Unmarshal(args[0], &username) == nil {
		return strings.TrimSpace(username)
	}
	var opts xOptions
	if json.Unmarshal(args[0], &opts) == nil {
		return strings.TrimSpace(opts.Username)
	}
	return ""
}

func parseXUserPostsArgs(args []json.RawMessage) (string, int) {
	username := parseXUsernameArgs(args)
	limit := 10
	if len(args) > 1 {
		var opts xOptions
		if json.Unmarshal(args[1], &opts) == nil && opts.Limit != 0 {
			limit = opts.Limit
		}
		return username, limit
	}
	if len(args) == 1 {
		var opts xOptions
		if json.Unmarshal(args[0], &opts) == nil {
			if opts.Limit != 0 {
				limit = opts.Limit
			}
			if opts.Username != "" {
				username = opts.Username
			}
		}
	}
	return username, limit
}

func parseXHomeTimelineArgs(args []json.RawMessage) int {
	limit := 10
	if len(args) == 0 {
		return limit
	}
	var rawLimit int
	if json.Unmarshal(args[0], &rawLimit) == nil && rawLimit != 0 {
		return rawLimit
	}
	var opts xOptions
	if json.Unmarshal(args[0], &opts) == nil && opts.Limit != 0 {
		return opts.Limit
	}
	return limit
}

func parseXPostArgs(args []json.RawMessage) string {
	if len(args) == 0 {
		return ""
	}
	var raw string
	if json.Unmarshal(args[0], &raw) == nil {
		return normalizeXTweetID(raw)
	}
	var opts xOptions
	if json.Unmarshal(args[0], &opts) == nil {
		if opts.ID != "" {
			return normalizeXTweetID(opts.ID)
		}
		return normalizeXTweetID(opts.URL)
	}
	return ""
}

func normalizeXTweetID(raw string) string {
	value := strings.TrimSpace(raw)
	if value == "" {
		return ""
	}
	if strings.Contains(value, "x.com/") && !strings.Contains(value, "://") {
		value = "https://" + value
	}
	if u, err := url.Parse(value); err == nil && u.Host != "" {
		parts := strings.Split(strings.Trim(u.Path, "/"), "/")
		for i, part := range parts {
			if (part == "status" || part == "statuses") && i+1 < len(parts) {
				return strings.TrimSpace(parts[i+1])
			}
		}
	}
	if before, _, ok := strings.Cut(value, "?"); ok {
		value = before
	}
	parts := strings.Split(strings.Trim(value, "/"), "/")
	for i, part := range parts {
		if (part == "status" || part == "statuses") && i+1 < len(parts) {
			return strings.TrimSpace(parts[i+1])
		}
	}
	if len(parts) > 0 {
		return strings.TrimSpace(parts[len(parts)-1])
	}
	return value
}

func clampXSkillLimit(limit int) int {
	if limit <= 0 {
		return 10
	}
	if limit > 10 {
		return 10
	}
	return limit
}
