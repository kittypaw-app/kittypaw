package main

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jinto/kittypaw/core"
)

func TestRootCommandRegistersGmailLatest(t *testing.T) {
	root := newRootCmd()
	for _, args := range [][]string{
		{"gmail", "latest"},
		{"gmail", "list"},
		{"gmail", "search"},
		{"gmail", "read"},
	} {
		cmd, _, err := root.Find(args)
		if err != nil || cmd == nil || cmd.Name() != args[1] {
			t.Fatalf("Find(%v) = (%v, %v), want %s command", args, cmd, err, args[1])
		}
	}
}

func TestGmailLatestReadsMostRecentMessage(t *testing.T) {
	rootDir := t.TempDir()
	t.Setenv("KITTYPAW_CONFIG_DIR", rootDir)
	mustWriteTestConfig(t, filepath.Join(rootDir, "accounts", "alice", "config.toml"))

	secrets, err := core.LoadAccountSecrets("alice")
	if err != nil {
		t.Fatalf("LoadAccountSecrets: %v", err)
	}
	tokens := core.NewServiceTokenManager(secrets)
	if err := tokens.Save("gmail", core.ServiceTokenSet{
		Provider:    "gmail",
		AccessToken: "gmail-access-1",
		ExpiresAt:   time.Now().Add(time.Hour),
		Scope:       "gmail.readonly",
		Email:       "alice@example.com",
	}); err != nil {
		t.Fatalf("save gmail token: %v", err)
	}

	bodyData := base64.RawURLEncoding.EncodeToString([]byte("hello from the latest gmail body"))
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer gmail-access-1" {
			t.Fatalf("Authorization = %q", got)
		}
		switch r.URL.Path {
		case "/gmail/v1/users/me/messages":
			if got := r.URL.Query().Get("maxResults"); got != "1" {
				t.Fatalf("maxResults = %q, want 1", got)
			}
			if got := r.URL.Query().Get("q"); got != defaultGmailLatestQuery {
				t.Fatalf("q = %q, want %q", got, defaultGmailLatestQuery)
			}
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
						{"name":"Subject","value":"Hello from Gmail"},
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

	root := newRootCmd()
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&out)
	root.SetArgs([]string{"gmail", "latest", "--account", "alice"})
	if err := root.Execute(); err != nil {
		t.Fatalf("Execute: %v\noutput:\n%s", err, out.String())
	}

	text := out.String()
	for _, want := range []string{
		"From: Alice <alice@example.com>",
		"Subject: Hello from Gmail",
		"Date: Wed, 6 May 2026 10:00:00 +0000",
		"Snippet: hello snippet",
		"hello from the latest gmail body",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("output missing %q:\n%s", want, text)
		}
	}
}

func TestGmailLatestMissingConnectionGuidesConnect(t *testing.T) {
	rootDir := t.TempDir()
	t.Setenv("KITTYPAW_CONFIG_DIR", rootDir)
	mustWriteTestConfig(t, filepath.Join(rootDir, "accounts", "alice", "config.toml"))

	root := newRootCmd()
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&out)
	root.SetArgs([]string{"gmail", "latest", "--account", "alice"})
	err := root.Execute()
	if err == nil {
		t.Fatal("Execute succeeded, want missing connection error")
	}
	if !strings.Contains(err.Error(), "kittypaw connect gmail") {
		t.Fatalf("error = %v, want connect guidance", err)
	}
}

func TestGmailListShowsRecentPrimaryMessages(t *testing.T) {
	withGmailCLIFixture(t, func(ts *httptest.Server, calls *[]string) {
		t.Setenv("KITTYPAW_GMAIL_BASE_URL", ts.URL)

		root := newRootCmd()
		var out bytes.Buffer
		root.SetOut(&out)
		root.SetErr(&out)
		root.SetArgs([]string{"gmail", "list", "--account", "alice", "--limit", "2"})
		if err := root.Execute(); err != nil {
			t.Fatalf("Execute: %v\noutput:\n%s", err, out.String())
		}

		if got := (*calls)[0]; got != "list:q=in:inbox category:primary:max=2" {
			t.Fatalf("first call = %q", got)
		}
		text := out.String()
		for _, want := range []string{
			"msg-1",
			"Alice <alice@example.com>",
			"Hello from Gmail",
			"msg-2",
			"Bob <bob@example.com>",
			"Second message",
		} {
			if !strings.Contains(text, want) {
				t.Fatalf("output missing %q:\n%s", want, text)
			}
		}
	})
}

func TestGmailSearchUsesQueryArgument(t *testing.T) {
	withGmailCLIFixture(t, func(ts *httptest.Server, calls *[]string) {
		t.Setenv("KITTYPAW_GMAIL_BASE_URL", ts.URL)

		root := newRootCmd()
		var out bytes.Buffer
		root.SetOut(&out)
		root.SetErr(&out)
		root.SetArgs([]string{"gmail", "search", "from:dzone newer_than:7d", "--account", "alice", "--limit", "1"})
		if err := root.Execute(); err != nil {
			t.Fatalf("Execute: %v\noutput:\n%s", err, out.String())
		}

		if got := (*calls)[0]; got != "list:q=from:dzone newer_than:7d:max=1" {
			t.Fatalf("first call = %q", got)
		}
		if !strings.Contains(out.String(), "msg-1") {
			t.Fatalf("output = %q, want msg-1", out.String())
		}
	})
}

func TestGmailSearchJoinsUnquotedQueryParts(t *testing.T) {
	withGmailCLIFixture(t, func(ts *httptest.Server, calls *[]string) {
		t.Setenv("KITTYPAW_GMAIL_BASE_URL", ts.URL)

		root := newRootCmd()
		var out bytes.Buffer
		root.SetOut(&out)
		root.SetErr(&out)
		root.SetArgs([]string{"gmail", "search", "from:dzone", "newer_than:7d", "--account", "alice", "--limit", "1"})
		if err := root.Execute(); err != nil {
			t.Fatalf("Execute: %v\noutput:\n%s", err, out.String())
		}

		if got := (*calls)[0]; got != "list:q=from:dzone newer_than:7d:max=1" {
			t.Fatalf("first call = %q", got)
		}
	})
}

func TestGmailReadFetchesMessageByID(t *testing.T) {
	withGmailCLIFixture(t, func(ts *httptest.Server, calls *[]string) {
		t.Setenv("KITTYPAW_GMAIL_BASE_URL", ts.URL)

		root := newRootCmd()
		var out bytes.Buffer
		root.SetOut(&out)
		root.SetErr(&out)
		root.SetArgs([]string{"gmail", "read", "msg-2", "--account", "alice"})
		if err := root.Execute(); err != nil {
			t.Fatalf("Execute: %v\noutput:\n%s", err, out.String())
		}

		if len(*calls) == 0 {
			t.Fatalf("no Gmail API calls; command likely not implemented")
		}
		if got := (*calls)[0]; got != "get:msg-2" {
			t.Fatalf("first call = %q, want direct get", got)
		}
		text := out.String()
		for _, want := range []string{
			"From: Bob <bob@example.com>",
			"Subject: Second message",
			"second body",
		} {
			if !strings.Contains(text, want) {
				t.Fatalf("output missing %q:\n%s", want, text)
			}
		}
	})
}

func withGmailCLIFixture(t *testing.T, fn func(*httptest.Server, *[]string)) {
	t.Helper()

	rootDir := t.TempDir()
	t.Setenv("KITTYPAW_CONFIG_DIR", rootDir)
	mustWriteTestConfig(t, filepath.Join(rootDir, "accounts", "alice", "config.toml"))

	secrets, err := core.LoadAccountSecrets("alice")
	if err != nil {
		t.Fatalf("LoadAccountSecrets: %v", err)
	}
	tokens := core.NewServiceTokenManager(secrets)
	if err := tokens.Save("gmail", core.ServiceTokenSet{
		Provider:    "gmail",
		AccessToken: "gmail-access-1",
		ExpiresAt:   time.Now().Add(time.Hour),
		Scope:       "gmail.readonly",
		Email:       "alice@example.com",
	}); err != nil {
		t.Fatalf("save gmail token: %v", err)
	}

	messages := map[string]struct {
		From    string
		Subject string
		Date    string
		Snippet string
		Body    string
	}{
		"msg-1": {
			From:    "Alice <alice@example.com>",
			Subject: "Hello from Gmail",
			Date:    "Wed, 6 May 2026 10:00:00 +0000",
			Snippet: "hello snippet",
			Body:    "hello body",
		},
		"msg-2": {
			From:    "Bob <bob@example.com>",
			Subject: "Second message",
			Date:    "Wed, 6 May 2026 11:00:00 +0000",
			Snippet: "second snippet",
			Body:    "second body",
		},
	}
	var calls []string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer gmail-access-1" {
			t.Fatalf("Authorization = %q", got)
		}
		switch r.URL.Path {
		case "/gmail/v1/users/me/messages":
			calls = append(calls, fmt.Sprintf("list:q=%s:max=%s", r.URL.Query().Get("q"), r.URL.Query().Get("maxResults")))
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `{"messages":[{"id":"msg-1","threadId":"thr-1"},{"id":"msg-2","threadId":"thr-2"}]}`)
		case "/gmail/v1/users/me/messages/msg-1", "/gmail/v1/users/me/messages/msg-2":
			id := strings.TrimPrefix(r.URL.Path, "/gmail/v1/users/me/messages/")
			calls = append(calls, "get:"+id)
			msg := messages[id]
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprintf(w, `{
				"id":%q,
				"threadId":"thr",
				"snippet":%q,
				"payload":{
					"headers":[
						{"name":"From","value":%q},
						{"name":"Subject","value":%q},
						{"name":"Date","value":%q}
					],
					"mimeType":"text/plain",
					"body":{"data":%q}
				}
			}`, id, msg.Snippet, msg.From, msg.Subject, msg.Date, base64.RawURLEncoding.EncodeToString([]byte(msg.Body)))
		default:
			http.NotFound(w, r)
		}
	}))
	defer ts.Close()

	fn(ts, &calls)
}
