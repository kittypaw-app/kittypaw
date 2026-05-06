package connect

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func TestXProviderAuthURL(t *testing.T) {
	provider := NewXProvider(XConfig{
		ClientID: "x-client-id",
		BaseURL:  "https://connect.kittypaw.app",
		AuthURL:  "https://x.example/auth",
	}, nil)

	raw := provider.AuthURL("state-1", "verifier-1")
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	q := u.Query()
	if got := u.String(); !strings.HasPrefix(got, "https://x.example/auth?") {
		t.Fatalf("auth URL = %q", got)
	}
	if q.Get("client_id") != "x-client-id" {
		t.Fatalf("client_id = %q", q.Get("client_id"))
	}
	if q.Get("redirect_uri") != "https://connect.kittypaw.app/connect/x/callback" {
		t.Fatalf("redirect_uri = %q", q.Get("redirect_uri"))
	}
	if q.Get("scope") != XReadOnlyScope {
		t.Fatalf("scope = %q", q.Get("scope"))
	}
	if q.Get("code_challenge_method") != "S256" || q.Get("code_challenge") == "" {
		t.Fatalf("PKCE params missing: %s", raw)
	}
}

func TestXProviderExchangeAndRefresh(t *testing.T) {
	var tokenForms []url.Values
	var authHeaders []string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/token":
			if err := r.ParseForm(); err != nil {
				t.Fatalf("ParseForm: %v", err)
			}
			authHeaders = append(authHeaders, r.Header.Get("Authorization"))
			copied := make(url.Values, len(r.Form))
			for k, v := range r.Form {
				copied[k] = append([]string(nil), v...)
			}
			tokenForms = append(tokenForms, copied)
			w.Header().Set("Content-Type", "application/json")
			switch r.Form.Get("grant_type") {
			case "authorization_code":
				fmt.Fprint(w, `{"access_token":"x-access-1","refresh_token":"x-refresh-1","token_type":"bearer","expires_in":7200,"scope":"`+XReadOnlyScope+`"}`)
			case "refresh_token":
				fmt.Fprint(w, `{"access_token":"x-access-2","refresh_token":"x-refresh-2","token_type":"bearer","expires_in":7200,"scope":"`+XReadOnlyScope+`"}`)
			default:
				t.Fatalf("grant_type = %q", r.Form.Get("grant_type"))
			}
		case "/users/me":
			if got := r.Header.Get("Authorization"); got != "Bearer x-access-1" {
				t.Fatalf("userinfo Authorization = %q", got)
			}
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `{"data":{"id":"123","name":"Jay Park","username":"jaypark"}}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer ts.Close()

	provider := NewXProvider(XConfig{
		ClientID:     "x-client-id",
		ClientSecret: "x-secret",
		BaseURL:      "https://connect.kittypaw.app",
		TokenURL:     ts.URL + "/token",
		UserInfoURL:  ts.URL + "/users/me",
	}, ts.Client())

	tokens, err := provider.ExchangeCode(t.Context(), "x-code", "verifier-1")
	if err != nil {
		t.Fatalf("ExchangeCode: %v", err)
	}
	if tokens.Provider != "x" || tokens.AccessToken != "x-access-1" || tokens.RefreshToken != "x-refresh-1" || tokens.Username != "jaypark" {
		b, _ := json.Marshal(tokens)
		t.Fatalf("tokens = %s", b)
	}
	if tokenForms[0].Get("client_id") != "x-client-id" {
		t.Fatalf("exchange client_id = %q", tokenForms[0].Get("client_id"))
	}
	if tokenForms[0].Get("redirect_uri") != "https://connect.kittypaw.app/connect/x/callback" {
		t.Fatalf("exchange redirect_uri = %q", tokenForms[0].Get("redirect_uri"))
	}
	if tokenForms[0].Get("code_verifier") != "verifier-1" {
		t.Fatalf("exchange code_verifier = %q", tokenForms[0].Get("code_verifier"))
	}
	if authHeaders[0] == "" {
		t.Fatal("exchange request should authenticate confidential X client")
	}

	refreshed, err := provider.Refresh(t.Context(), "x-refresh-1")
	if err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	if refreshed.Provider != "x" || refreshed.AccessToken != "x-access-2" || refreshed.RefreshToken != "x-refresh-2" {
		b, _ := json.Marshal(refreshed)
		t.Fatalf("refreshed = %s", b)
	}
	if tokenForms[1].Get("grant_type") != "refresh_token" || tokenForms[1].Get("refresh_token") != "x-refresh-1" {
		t.Fatalf("refresh form = %v", tokenForms[1])
	}
	if authHeaders[1] == "" {
		t.Fatal("refresh request should authenticate confidential X client")
	}
}
