package core

import (
	"context"
	"encoding/base64"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestGmailClientListAndGetMessage(t *testing.T) {
	var authHeaders []string
	bodyData := base64.RawURLEncoding.EncodeToString([]byte("hello from gmail body"))
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authHeaders = append(authHeaders, r.Header.Get("Authorization"))
		switch r.URL.Path {
		case "/gmail/v1/users/me/messages":
			if got := r.URL.Query().Get("maxResults"); got != "5" {
				t.Fatalf("maxResults = %q", got)
			}
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `{"messages":[{"id":"msg-1","threadId":"thr-1"}]}`)
		case "/gmail/v1/users/me/messages/msg-1":
			if got := r.URL.Query().Get("format"); got != "full" {
				t.Fatalf("format = %q", got)
			}
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprintf(w, `{
				"id":"msg-1",
				"threadId":"thr-1",
				"snippet":"hello snippet",
				"payload":{
					"headers":[
						{"name":"From","value":"Alice <alice@example.com>"},
						{"name":"Subject","value":"Hello"},
						{"name":"Date","value":"Mon, 4 May 2026 12:00:00 +0000"}
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

	client := NewGmailClient(ts.URL, ts.Client())
	refs, err := client.ListMessages(context.Background(), "gmail-access", 5)
	if err != nil {
		t.Fatalf("ListMessages: %v", err)
	}
	if len(refs) != 1 || refs[0].ID != "msg-1" || refs[0].ThreadID != "thr-1" {
		t.Fatalf("refs = %#v", refs)
	}
	msg, err := client.GetMessage(context.Background(), "gmail-access", "msg-1")
	if err != nil {
		t.Fatalf("GetMessage: %v", err)
	}
	if msg.ID != "msg-1" || msg.Subject != "Hello" || msg.From != "Alice <alice@example.com>" || msg.BodyText != "hello from gmail body" {
		t.Fatalf("message = %#v", msg)
	}
	for _, got := range authHeaders {
		if got != "Bearer gmail-access" {
			t.Fatalf("Authorization = %q", got)
		}
	}
}

func TestGmailClientSearchMessagesUsesQuery(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Query().Get("q"); got != "in:inbox category:primary" {
			t.Fatalf("q = %q, want inbox primary query", got)
		}
		if got := r.URL.Query().Get("maxResults"); got != "1" {
			t.Fatalf("maxResults = %q, want 1", got)
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"messages":[{"id":"msg-1","threadId":"thr-1"}]}`)
	}))
	defer ts.Close()

	client := NewGmailClient(ts.URL, ts.Client())
	refs, err := client.SearchMessages(context.Background(), "gmail-access", 1, "in:inbox category:primary")
	if err != nil {
		t.Fatalf("SearchMessages: %v", err)
	}
	if len(refs) != 1 || refs[0].ID != "msg-1" {
		t.Fatalf("refs = %#v", refs)
	}
}

func TestGmailClientUnauthorizedGuidance(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
	}))
	defer ts.Close()

	client := NewGmailClient(ts.URL, ts.Client())
	_, err := client.ListMessages(context.Background(), "bad-token", 5)
	if err == nil {
		t.Fatal("ListMessages succeeded, want error")
	}
	if !strings.Contains(err.Error(), "kittypaw connect gmail") {
		t.Fatalf("error = %v, want reconnect guidance", err)
	}
}

func TestGmailClientNestedPlainTextBody(t *testing.T) {
	bodyData := base64.RawURLEncoding.EncodeToString([]byte("nested plain text"))
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{
			"id":"msg-1",
			"payload":{
				"mimeType":"multipart/alternative",
				"parts":[
					{"mimeType":"text/html","body":{"data":%q}},
					{"mimeType":"text/plain","body":{"data":%q}}
				]
			}
		}`, base64.RawURLEncoding.EncodeToString([]byte("<p>html</p>")), bodyData)
	}))
	defer ts.Close()

	client := NewGmailClient(ts.URL, ts.Client())
	msg, err := client.GetMessage(context.Background(), "gmail-access", "msg-1")
	if err != nil {
		t.Fatalf("GetMessage: %v", err)
	}
	if msg.BodyText != "nested plain text" {
		t.Fatalf("BodyText = %q", msg.BodyText)
	}
}

func TestGmailClientHTMLOnlyBodyBecomesReadableText(t *testing.T) {
	htmlBody := `<html><head><style>.x{display:none}</style></head><body><h1>Hello</h1><p>L&#39;offre&nbsp;se termine bientôt.</p><script>ignored()</script></body></html>`
	bodyData := base64.RawURLEncoding.EncodeToString([]byte(htmlBody))
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{
			"id":"msg-1",
			"payload":{
				"mimeType":"text/html",
				"body":{"data":%q}
			}
		}`, bodyData)
	}))
	defer ts.Close()

	client := NewGmailClient(ts.URL, ts.Client())
	msg, err := client.GetMessage(context.Background(), "gmail-access", "msg-1")
	if err != nil {
		t.Fatalf("GetMessage: %v", err)
	}
	if strings.Contains(msg.BodyText, "<html") || strings.Contains(msg.BodyText, "<script") {
		t.Fatalf("BodyText still contains HTML: %q", msg.BodyText)
	}
	if !strings.Contains(msg.BodyText, "Hello") || !strings.Contains(msg.BodyText, "L'offre se termine bientôt.") {
		t.Fatalf("BodyText = %q, want readable decoded text", msg.BodyText)
	}
}
