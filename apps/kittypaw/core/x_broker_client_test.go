package core

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestXBrokerClientSearchRecentUsesBrokerEndpoint(t *testing.T) {
	var gotAuth, gotPath, gotQuery, gotLimit string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotPath = r.URL.Path
		gotQuery = r.URL.Query().Get("query")
		gotLimit = r.URL.Query().Get("limit")
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"posts":[{"id":"post-1","text":"broker x","author_id":"u1","author":{"id":"u1","username":"alice","name":"Alice"}}]}`)
	}))
	defer ts.Close()

	client := NewXBrokerClient(ts.URL, ts.Client())
	result, err := client.SearchRecent(context.Background(), "kitty-token", "kittypaw", 50)
	if err != nil {
		t.Fatalf("SearchRecent: %v", err)
	}
	if gotAuth != "Bearer kitty-token" {
		t.Fatalf("Authorization = %q", gotAuth)
	}
	if gotPath != "/connect/x/broker/search/recent" || gotQuery != "kittypaw" || gotLimit != "10" {
		t.Fatalf("path/query/limit = %q/%q/%q", gotPath, gotQuery, gotLimit)
	}
	if len(result.Posts) != 1 || result.Posts[0].Author == nil || result.Posts[0].Author.Username != "alice" {
		b, _ := json.Marshal(result)
		t.Fatalf("result = %s", b)
	}
}

func TestXBrokerClientStatusError(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusTooManyRequests)
		fmt.Fprint(w, `{"error":{"code":"quota_exceeded","message":"monthly post read quota exceeded"}}`)
	}))
	defer ts.Close()

	client := NewXBrokerClient(ts.URL, ts.Client())
	_, err := client.SearchRecent(context.Background(), "kitty-token", "kittypaw", 10)
	if err == nil {
		t.Fatal("SearchRecent succeeded, want status error")
	}
	statusErr, ok := err.(*XBrokerStatusError)
	if !ok {
		t.Fatalf("err type = %T, want *XBrokerStatusError", err)
	}
	if statusErr.StatusCode != http.StatusTooManyRequests || statusErr.Code != "quota_exceeded" {
		t.Fatalf("status error = %#v", statusErr)
	}
}
