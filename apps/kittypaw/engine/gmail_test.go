package engine

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/jinto/kittypaw/core"
)

func TestBuildSkillsSection_GmailGuidance(t *testing.T) {
	section := buildSkillsSection("")
	for _, phrase := range []string{
		"Gmail.list",
		"Gmail.search",
		"Gmail.read",
		"newer_than:1d",
		"kittypaw connect gmail",
	} {
		if !strings.Contains(section, phrase) {
			t.Fatalf("buildSkillsSection missing Gmail phrase %q", phrase)
		}
	}
}

func TestExecuteGmailSearchUsesConnectedAccountToken(t *testing.T) {
	var gotAuth, gotQuery, gotLimit string
	bodyData := base64.RawURLEncoding.EncodeToString([]byte("body from gmail"))
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		switch r.URL.Path {
		case "/gmail/v1/users/me/messages":
			gotQuery = r.URL.Query().Get("q")
			gotLimit = r.URL.Query().Get("maxResults")
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `{"messages":[{"id":"msg-1","threadId":"thr-1"}]}`)
		case "/gmail/v1/users/me/messages/msg-1":
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprintf(w, `{
				"id":"msg-1",
				"threadId":"thr-1",
				"snippet":"hello snippet",
				"payload":{
					"headers":[
						{"name":"From","value":"Alice <alice@example.com>"},
						{"name":"Subject","value":"Today report"},
						{"name":"Date","value":"Wed, 6 May 2026 10:00:00 +0000"}
					],
					"mimeType":"text/plain",
					"body":{"data":%q}
				}
			}`, bodyData)
		default:
			http.NotFound(w, r)
		}
	}))
	defer ts.Close()
	t.Setenv("KITTYPAW_GMAIL_BASE_URL", ts.URL)

	secrets, err := core.LoadSecretsFrom(t.TempDir() + "/secrets.json")
	if err != nil {
		t.Fatal(err)
	}
	mgr := core.NewServiceTokenManager(secrets)
	if err := mgr.Save("gmail", core.ServiceTokenSet{
		AccessToken: "gmail-access",
		ExpiresAt:   time.Now().Add(time.Hour),
	}); err != nil {
		t.Fatal(err)
	}
	sess := &Session{
		Config:          &core.Config{AutonomyLevel: core.AutonomyFull},
		ServiceTokenMgr: mgr,
		AccountID:       "jinto",
	}

	options := json.RawMessage(`{"query":"newer_than:1d","limit":1}`)
	got, err := resolveSkillCall(context.Background(), core.SkillCall{
		SkillName: "Gmail",
		Method:    "search",
		Args:      []json.RawMessage{options},
	}, sess, nil)
	if err != nil {
		t.Fatal(err)
	}
	if gotAuth != "Bearer gmail-access" {
		t.Fatalf("Authorization = %q", gotAuth)
	}
	if gotQuery != "newer_than:1d" || gotLimit != "1" {
		t.Fatalf("query/limit = %q/%q", gotQuery, gotLimit)
	}
	var out struct {
		Messages []struct {
			ID      string `json:"id"`
			From    string `json:"from"`
			Subject string `json:"subject"`
			Snippet string `json:"snippet"`
		} `json:"messages"`
	}
	if err := json.Unmarshal([]byte(got), &out); err != nil {
		t.Fatalf("json: %v\n%s", err, got)
	}
	if len(out.Messages) != 1 || out.Messages[0].ID != "msg-1" || out.Messages[0].Subject != "Today report" {
		t.Fatalf("messages = %#v", out.Messages)
	}
}

func TestExecuteGmailReadRequiresConnection(t *testing.T) {
	sess := &Session{Config: &core.Config{AutonomyLevel: core.AutonomyFull}, AccountID: "jinto"}
	id := json.RawMessage(`"msg-1"`)
	got, err := resolveSkillCall(context.Background(), core.SkillCall{
		SkillName: "Gmail",
		Method:    "read",
		Args:      []json.RawMessage{id},
	}, sess, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, "kittypaw connect gmail --account jinto") {
		t.Fatalf("missing reconnect guidance: %s", got)
	}
}
