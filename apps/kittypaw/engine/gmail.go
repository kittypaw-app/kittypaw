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

const (
	defaultGmailListQuery = "in:inbox category:primary"
	gmailReadBodyLimit    = 4000
)

type gmailOptions struct {
	Query string `json:"query"`
	Limit int    `json:"limit"`
	ID    string `json:"id"`
}

func executeGmail(ctx context.Context, call core.SkillCall, s *Session) (string, error) {
	client, accessToken, errResult := gmailClientForSession(s)
	if errResult != "" {
		return errResult, nil
	}

	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	switch call.Method {
	case "list":
		query, limit := parseGmailSearchArgs(call.Args, defaultGmailListQuery)
		return executeGmailSearch(ctx, client, accessToken, query, limit)
	case "search":
		query, limit := parseGmailSearchArgs(call.Args, "")
		if strings.TrimSpace(query) == "" {
			return jsonResult(map[string]any{"error": "Gmail.search requires a query, e.g. Gmail.search(\"newer_than:1d\", {limit: 10})"})
		}
		return executeGmailSearch(ctx, client, accessToken, query, limit)
	case "read":
		id := parseGmailReadID(call.Args)
		if id == "" {
			return jsonResult(map[string]any{"error": "Gmail.read requires a message id"})
		}
		msg, err := client.GetMessage(ctx, accessToken, id)
		if err != nil {
			return jsonResult(map[string]any{"error": err.Error()})
		}
		return jsonResult(map[string]any{
			"id":        msg.ID,
			"thread_id": msg.ThreadID,
			"from":      msg.From,
			"subject":   msg.Subject,
			"date":      msg.Date,
			"snippet":   msg.Snippet,
			"body_text": truncateGmailText(msg.BodyText, gmailReadBodyLimit),
		})
	default:
		return jsonResult(map[string]any{"error": fmt.Sprintf("unknown Gmail method: %s", call.Method)})
	}
}

func gmailClientForSession(s *Session) (*core.GmailClient, string, string) {
	if s == nil || s.ServiceTokenMgr == nil {
		return nil, "", jsonResultMust(map[string]any{"error": gmailConnectGuidance(s)})
	}
	accessToken, err := s.ServiceTokenMgr.LoadAccessToken("gmail")
	if err != nil || accessToken == "" {
		msg := gmailConnectGuidance(s)
		if err != nil {
			msg = fmt.Sprintf("%s (%v)", msg, err)
		}
		return nil, "", jsonResultMust(map[string]any{"error": msg})
	}
	return core.NewGmailClient(os.Getenv("KITTYPAW_GMAIL_BASE_URL"), nil), accessToken, ""
}

func gmailConnectGuidance(s *Session) string {
	if s != nil && strings.TrimSpace(s.AccountID) != "" {
		return fmt.Sprintf("gmail not connected — run: kittypaw connect gmail --account %s", s.AccountID)
	}
	return "gmail not connected — run: kittypaw connect gmail"
}

func executeGmailSearch(ctx context.Context, client *core.GmailClient, accessToken, query string, limit int) (string, error) {
	limit = clampGmailSkillLimit(limit)
	refs, err := client.SearchMessages(ctx, accessToken, limit, query)
	if err != nil {
		return jsonResult(map[string]any{"error": err.Error()})
	}
	messages := make([]map[string]any, 0, len(refs))
	for _, ref := range refs {
		msg, err := client.GetMessage(ctx, accessToken, ref.ID)
		if err != nil {
			return jsonResult(map[string]any{"error": err.Error(), "message_id": ref.ID})
		}
		messages = append(messages, map[string]any{
			"id":        msg.ID,
			"thread_id": msg.ThreadID,
			"from":      msg.From,
			"subject":   msg.Subject,
			"date":      msg.Date,
			"snippet":   msg.Snippet,
		})
	}
	return jsonResult(map[string]any{
		"query":    query,
		"limit":    limit,
		"count":    len(messages),
		"messages": messages,
	})
}

func parseGmailSearchArgs(args []json.RawMessage, defaultQuery string) (string, int) {
	query := defaultQuery
	limit := 10
	if len(args) == 0 {
		return query, limit
	}
	var firstString string
	if json.Unmarshal(args[0], &firstString) == nil {
		if strings.TrimSpace(firstString) != "" {
			query = strings.TrimSpace(firstString)
		}
		if len(args) > 1 {
			var opts gmailOptions
			if json.Unmarshal(args[1], &opts) == nil && opts.Limit != 0 {
				limit = opts.Limit
			}
		}
		return query, limit
	}
	var opts gmailOptions
	if json.Unmarshal(args[0], &opts) == nil {
		if strings.TrimSpace(opts.Query) != "" {
			query = strings.TrimSpace(opts.Query)
		}
		if opts.Limit != 0 {
			limit = opts.Limit
		}
	}
	return query, limit
}

func parseGmailReadID(args []json.RawMessage) string {
	if len(args) == 0 {
		return ""
	}
	var id string
	if json.Unmarshal(args[0], &id) == nil {
		return strings.TrimSpace(id)
	}
	var opts gmailOptions
	if json.Unmarshal(args[0], &opts) == nil {
		return strings.TrimSpace(opts.ID)
	}
	return ""
}

func clampGmailSkillLimit(limit int) int {
	if limit <= 0 {
		return 10
	}
	if limit > 25 {
		return 25
	}
	return limit
}

func truncateGmailText(s string, max int) string {
	if max <= 0 {
		return ""
	}
	runes := []rune(strings.TrimSpace(s))
	if len(runes) <= max {
		return string(runes)
	}
	return string(runes[:max]) + "..."
}

func jsonResultMust(v map[string]any) string {
	out, _ := jsonResult(v)
	return out
}
