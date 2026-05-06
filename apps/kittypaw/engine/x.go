package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/jinto/kittypaw/core"
)

type xOptions struct {
	Query    string `json:"query"`
	Username string `json:"username"`
	Limit    int    `json:"limit"`
}

func executeX(ctx context.Context, call core.SkillCall, s *Session) (string, error) {
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
			return jsonResult(map[string]any{"error": err.Error()})
		}
		return jsonResult(map[string]any{
			"query": query,
			"limit": limit,
			"count": len(result.Posts),
			"posts": result.Posts,
		})
	case "user":
		username := parseXUsernameArgs(call.Args)
		if username == "" {
			return jsonResult(map[string]any{"error": `X.user requires a username, e.g. X.user("XDevelopers")`})
		}
		user, err := client.UserByUsername(ctx, accessToken, username)
		if err != nil {
			return jsonResult(map[string]any{"error": err.Error()})
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
			return jsonResult(map[string]any{"error": err.Error()})
		}
		result, err := client.UserPosts(ctx, accessToken, user.ID, limit)
		if err != nil {
			return jsonResult(map[string]any{"error": err.Error()})
		}
		return jsonResult(map[string]any{
			"username": strings.TrimPrefix(strings.TrimSpace(username), "@"),
			"user":     user,
			"limit":    limit,
			"count":    len(result.Posts),
			"posts":    result.Posts,
		})
	default:
		return jsonResult(map[string]any{"error": fmt.Sprintf("unknown X method: %s", call.Method)})
	}
}

func xClientForSession(s *Session) (*core.XClient, string, string) {
	if s == nil || s.ServiceTokenMgr == nil {
		return nil, "", jsonResultMust(map[string]any{"error": xConnectGuidance(s)})
	}
	accessToken, err := s.ServiceTokenMgr.LoadAccessToken("x")
	if err != nil || accessToken == "" {
		msg := xConnectGuidance(s)
		if err != nil {
			msg = fmt.Sprintf("%s (%v)", msg, err)
		}
		return nil, "", jsonResultMust(map[string]any{"error": msg})
	}
	return core.NewXClient(os.Getenv("KITTYPAW_X_BASE_URL"), nil), accessToken, ""
}

func xConnectGuidance(s *Session) string {
	if s != nil && strings.TrimSpace(s.AccountID) != "" {
		return fmt.Sprintf("x not connected — run: kittypaw connect x --account %s", s.AccountID)
	}
	return "x not connected — run: kittypaw connect x"
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

func clampXSkillLimit(limit int) int {
	if limit <= 0 {
		return 10
	}
	if limit > 10 {
		return 10
	}
	return limit
}
