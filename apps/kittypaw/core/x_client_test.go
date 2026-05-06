package core

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestXClientSearchRecentParsesPostsAndAuthors(t *testing.T) {
	var gotAuth, gotPath, gotQuery, gotMax string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotPath = r.URL.Path
		gotQuery = r.URL.Query().Get("query")
		gotMax = r.URL.Query().Get("max_results")
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{
			"data":[{"id":"post-1","text":"hello x","author_id":"u1","created_at":"2026-05-07T01:02:03Z","public_metrics":{"like_count":2,"reply_count":1}}],
			"includes":{"users":[{"id":"u1","username":"alice","name":"Alice","verified":true}]}
		}`)
	}))
	defer ts.Close()

	client := NewXClient(ts.URL, ts.Client())
	result, err := client.SearchRecent(context.Background(), "x-access", "kittypaw", 3)
	if err != nil {
		t.Fatalf("SearchRecent: %v", err)
	}
	if gotAuth != "Bearer x-access" {
		t.Fatalf("Authorization = %q", gotAuth)
	}
	if gotPath != "/tweets/search/recent" {
		t.Fatalf("path = %q", gotPath)
	}
	if gotQuery != "kittypaw" {
		t.Fatalf("query = %q", gotQuery)
	}
	if gotMax != "10" {
		t.Fatalf("max_results = %q, want X API minimum/default 10", gotMax)
	}
	if len(result.Posts) != 1 || result.Posts[0].ID != "post-1" || result.Posts[0].Author.Username != "alice" {
		t.Fatalf("posts = %#v", result.Posts)
	}
}

func TestXClientUserByUsernameAndUserPosts(t *testing.T) {
	var sawUser, sawPosts bool
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer x-access" {
			t.Fatalf("Authorization = %q", r.Header.Get("Authorization"))
		}
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/users/by/username/jaypark":
			sawUser = true
			fmt.Fprint(w, `{"data":{"id":"u1","username":"jaypark","name":"Jay Park","verified":false}}`)
		case "/users/u1/tweets":
			sawPosts = true
			if got := r.URL.Query().Get("max_results"); got != "10" {
				t.Fatalf("max_results = %q", got)
			}
			fmt.Fprint(w, `{"data":[{"id":"post-1","text":"from jay","author_id":"u1"}],"includes":{"users":[{"id":"u1","username":"jaypark","name":"Jay Park"}]}}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer ts.Close()

	client := NewXClient(ts.URL, ts.Client())
	user, err := client.UserByUsername(context.Background(), "x-access", "@jaypark")
	if err != nil {
		t.Fatalf("UserByUsername: %v", err)
	}
	if user.ID != "u1" || user.Username != "jaypark" {
		t.Fatalf("user = %#v", user)
	}
	result, err := client.UserPosts(context.Background(), "x-access", user.ID, 5)
	if err != nil {
		t.Fatalf("UserPosts: %v", err)
	}
	if !sawUser || !sawPosts {
		t.Fatalf("saw user/posts = %v/%v", sawUser, sawPosts)
	}
	if len(result.Posts) != 1 || result.Posts[0].Text != "from jay" {
		t.Fatalf("posts = %#v", result.Posts)
	}
}

func TestXClientUnauthorizedGuidance(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "nope", http.StatusUnauthorized)
	}))
	defer ts.Close()

	client := NewXClient(ts.URL, ts.Client())
	_, err := client.SearchRecent(context.Background(), "bad-token", "kittypaw", 10)
	if err == nil {
		t.Fatal("SearchRecent succeeded, want authorization error")
	}
	if !strings.Contains(err.Error(), "kittypaw connect x") {
		t.Fatalf("error = %v, want reconnect guidance", err)
	}
}
