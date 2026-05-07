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

func TestBuildSkillsSection_XGuidance(t *testing.T) {
	section := buildSkillsSection("")
	for _, phrase := range []string{
		"X.searchRecent",
		"X.homeTimeline",
		"X.user",
		"X.userPosts",
		"x_credits_depleted",
		"kittypaw connect x",
	} {
		if !strings.Contains(section, phrase) {
			t.Fatalf("buildSkillsSection missing X phrase %q", phrase)
		}
	}
}

func TestExecuteXSearchRecentUsesBroker(t *testing.T) {
	var gotAuth, gotPath, gotQuery, gotLimit string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotPath = r.URL.Path
		gotQuery = r.URL.Query().Get("query")
		gotLimit = r.URL.Query().Get("limit")
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"posts":[{"id":"post-1","text":"broker x","author_id":"u1","created_at":"2026-05-07T01:02:03Z","author":{"id":"u1","username":"alice","name":"Alice"}}]}`)
	}))
	defer ts.Close()

	secrets, err := core.LoadSecretsFrom(t.TempDir() + "/secrets.json")
	if err != nil {
		t.Fatal(err)
	}
	apiMgr := core.NewAPITokenManager("", secrets)
	if err := apiMgr.SaveTokens(core.DefaultAPIServerURL, testJWT(time.Now().Add(time.Hour)), "refresh-token"); err != nil {
		t.Fatal(err)
	}
	if err := apiMgr.SaveConnectBaseURL(core.DefaultAPIServerURL, ts.URL); err != nil {
		t.Fatal(err)
	}
	sess := &Session{
		Config:      &core.Config{AutonomyLevel: core.AutonomyFull},
		APITokenMgr: apiMgr,
		AccountID:   "jinto",
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
	if gotAuth == "" || !strings.HasPrefix(gotAuth, "Bearer ") {
		t.Fatalf("Authorization = %q", gotAuth)
	}
	if gotPath != "/connect/x/broker/search/recent" || gotQuery != "kittypaw" || gotLimit != "10" {
		t.Fatalf("path/query/limit = %q/%q/%q", gotPath, gotQuery, gotLimit)
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

func TestExecuteXHomeTimelineUsesBroker(t *testing.T) {
	var gotAuth, gotPath, gotLimit string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotPath = r.URL.Path
		gotLimit = r.URL.Query().Get("limit")
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"posts":[{"id":"post-1","text":"home x","author_id":"u1","created_at":"2026-05-07T01:02:03Z","author":{"id":"u1","username":"alice","name":"Alice"}}]}`)
	}))
	defer ts.Close()

	secrets, err := core.LoadSecretsFrom(t.TempDir() + "/secrets.json")
	if err != nil {
		t.Fatal(err)
	}
	apiMgr := core.NewAPITokenManager("", secrets)
	if err := apiMgr.SaveTokens(core.DefaultAPIServerURL, testJWT(time.Now().Add(time.Hour)), "refresh-token"); err != nil {
		t.Fatal(err)
	}
	if err := apiMgr.SaveConnectBaseURL(core.DefaultAPIServerURL, ts.URL); err != nil {
		t.Fatal(err)
	}
	sess := &Session{
		Config:      &core.Config{AutonomyLevel: core.AutonomyFull},
		APITokenMgr: apiMgr,
		AccountID:   "jinto",
	}

	options := json.RawMessage(`{"limit":50}`)
	got, err := resolveSkillCall(context.Background(), core.SkillCall{
		SkillName: "X",
		Method:    "homeTimeline",
		Args:      []json.RawMessage{options},
	}, sess, nil)
	if err != nil {
		t.Fatal(err)
	}
	if gotAuth == "" || !strings.HasPrefix(gotAuth, "Bearer ") {
		t.Fatalf("Authorization = %q", gotAuth)
	}
	if gotPath != "/connect/x/broker/users/me/timelines/reverse_chronological" || gotLimit != "10" {
		t.Fatalf("path/limit = %q/%q", gotPath, gotLimit)
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

func TestExecuteXRequiresLogin(t *testing.T) {
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
	if !strings.Contains(got, "kittypaw login --account jinto") {
		t.Fatalf("missing login guidance: %s", got)
	}
}

func TestExecuteXBrokerForbiddenShowsConnectGuidance(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusForbidden)
		fmt.Fprint(w, `{"error":{"code":"x_not_connected","message":"x account not connected"}}`)
	}))
	defer ts.Close()

	secrets, err := core.LoadSecretsFrom(t.TempDir() + "/secrets.json")
	if err != nil {
		t.Fatal(err)
	}
	apiMgr := core.NewAPITokenManager("", secrets)
	if err := apiMgr.SaveTokens(core.DefaultAPIServerURL, testJWT(time.Now().Add(time.Hour)), "refresh-token"); err != nil {
		t.Fatal(err)
	}
	if err := apiMgr.SaveConnectBaseURL(core.DefaultAPIServerURL, ts.URL); err != nil {
		t.Fatal(err)
	}
	sess := &Session{Config: &core.Config{AutonomyLevel: core.AutonomyFull}, APITokenMgr: apiMgr, AccountID: "jinto"}

	got, err := resolveSkillCall(context.Background(), core.SkillCall{
		SkillName: "X",
		Method:    "searchRecent",
		Args:      []json.RawMessage{json.RawMessage(`"kittypaw"`)},
	}, sess, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, "kittypaw connect x --account jinto") {
		t.Fatalf("missing connect guidance: %s", got)
	}
}

func TestExecuteXBrokerCreditsDepletedIsNotServerGuidance(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusPaymentRequired)
		fmt.Fprint(w, `{"error":{"code":"x_credits_depleted","message":"X API credits depleted. Refill the X developer account credits or wait until credits are available again."}}`)
	}))
	defer ts.Close()

	secrets, err := core.LoadSecretsFrom(t.TempDir() + "/secrets.json")
	if err != nil {
		t.Fatal(err)
	}
	apiMgr := core.NewAPITokenManager("", secrets)
	if err := apiMgr.SaveTokens(core.DefaultAPIServerURL, testJWT(time.Now().Add(time.Hour)), "refresh-token"); err != nil {
		t.Fatal(err)
	}
	if err := apiMgr.SaveConnectBaseURL(core.DefaultAPIServerURL, ts.URL); err != nil {
		t.Fatal(err)
	}
	sess := &Session{Config: &core.Config{AutonomyLevel: core.AutonomyFull}, APITokenMgr: apiMgr, AccountID: "jinto"}

	got, err := resolveSkillCall(context.Background(), core.SkillCall{
		SkillName: "X",
		Method:    "homeTimeline",
		Args:      []json.RawMessage{json.RawMessage(`{"limit":10}`)},
	}, sess, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"x_credits_depleted",
		"X API credits are depleted",
		"not a login, connection, or server outage",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("credit depletion guidance missing %q:\n%s", want, got)
		}
	}
}

func testJWT(exp time.Time) string {
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"none"}`))
	payload := base64.RawURLEncoding.EncodeToString([]byte(fmt.Sprintf(`{"exp":%d}`, exp.Unix())))
	return header + "." + payload + ".sig"
}
