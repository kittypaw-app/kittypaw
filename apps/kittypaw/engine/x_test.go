package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/jinto/kittypaw/core"
)

func TestBuildSkillsSection_XGuidance(t *testing.T) {
	section := buildSkillsSection("")
	for _, phrase := range []string{
		"X.searchRecent",
		"X.user",
		"X.userPosts",
		"kittypaw connect x",
	} {
		if !strings.Contains(section, phrase) {
			t.Fatalf("buildSkillsSection missing X phrase %q", phrase)
		}
	}
}

func TestExecuteXSearchRecentUsesConnectedAccountToken(t *testing.T) {
	var gotAuth, gotQuery, gotMax string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotQuery = r.URL.Query().Get("query")
		gotMax = r.URL.Query().Get("max_results")
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{
			"data":[{"id":"post-1","text":"native x","author_id":"u1","created_at":"2026-05-07T01:02:03Z"}],
			"includes":{"users":[{"id":"u1","username":"alice","name":"Alice"}]}
		}`)
	}))
	defer ts.Close()
	t.Setenv("KITTYPAW_X_BASE_URL", ts.URL)

	secrets, err := core.LoadSecretsFrom(t.TempDir() + "/secrets.json")
	if err != nil {
		t.Fatal(err)
	}
	mgr := core.NewServiceTokenManager(secrets)
	if err := mgr.Save("x", core.ServiceTokenSet{
		AccessToken: "x-access",
		ExpiresAt:   time.Now().Add(time.Hour),
	}); err != nil {
		t.Fatal(err)
	}
	sess := &Session{
		Config:          &core.Config{AutonomyLevel: core.AutonomyFull},
		ServiceTokenMgr: mgr,
		AccountID:       "jinto",
	}

	query := json.RawMessage(`"kittypaw"`)
	options := json.RawMessage(`{"limit":50}`)
	got, err := resolveSkillCall(context.Background(), core.SkillCall{
		SkillName: "X",
		Method:    "searchRecent",
		Args:      []json.RawMessage{query, options},
	}, sess, nil)
	if err != nil {
		t.Fatal(err)
	}
	if gotAuth != "Bearer x-access" {
		t.Fatalf("Authorization = %q", gotAuth)
	}
	if gotQuery != "kittypaw" || gotMax != "10" {
		t.Fatalf("query/max = %q/%q", gotQuery, gotMax)
	}
	var out struct {
		Limit int `json:"limit"`
		Posts []struct {
			ID     string `json:"id"`
			Text   string `json:"text"`
			Author struct {
				Username string `json:"username"`
			} `json:"author"`
		} `json:"posts"`
	}
	if err := json.Unmarshal([]byte(got), &out); err != nil {
		t.Fatalf("json: %v\n%s", err, got)
	}
	if out.Limit != 10 || len(out.Posts) != 1 || out.Posts[0].Author.Username != "alice" {
		t.Fatalf("out = %#v", out)
	}
}

func TestExecuteXRequiresConnection(t *testing.T) {
	sess := &Session{Config: &core.Config{AutonomyLevel: core.AutonomyFull}, AccountID: "jinto"}
	query := json.RawMessage(`"kittypaw"`)
	got, err := resolveSkillCall(context.Background(), core.SkillCall{
		SkillName: "X",
		Method:    "searchRecent",
		Args:      []json.RawMessage{query},
	}, sess, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, "kittypaw connect x --account jinto") {
		t.Fatalf("missing reconnect guidance: %s", got)
	}
}
