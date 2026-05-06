package testfixture

import (
	"context"
	"crypto/rsa"
	"fmt"
	"testing"
	"time"

	"github.com/kittypaw-app/kittyportal/internal/auth"
	"github.com/kittypaw-app/kittyportal/internal/model"
)

const defaultTTL = 15 * time.Minute

// IssueTestJWT signs a user JWT in production wire format (RS256, kid
// header, v=2, default API client aud/scope). The caller injects key+kid
// to keep this package config-free — testfixture must not import config
// (cycle: config → auth → testfixture would loop). Tests fetch the
// process-cached fixture key via config.LoadForTest() and pass it here.
//
// Plan 21 PR-B: HS256 secret signature dropped — RS256 + JWKS path only.
func IssueTestJWT(t *testing.T, key *rsa.PrivateKey, kid, userID string, ttl time.Duration) string {
	t.Helper()
	if ttl == 0 {
		ttl = defaultTTL
	}
	token, err := auth.SignForAudiences(userID, auth.DefaultAPIClientAudiences, auth.DefaultAPIClientScopes, key, kid, ttl)
	if err != nil {
		t.Fatalf("testfixture.IssueTestJWT: %v", err)
	}
	return token
}

func IssueTestJWTForAudience(t *testing.T, key *rsa.PrivateKey, kid, userID, audience string, scopes []string, ttl time.Duration) string {
	t.Helper()
	if ttl == 0 {
		ttl = defaultTTL
	}
	token, err := auth.SignForAudiences(userID, []string{audience}, scopes, key, kid, ttl)
	if err != nil {
		t.Fatalf("testfixture.IssueTestJWTForAudience: %v", err)
	}
	return token
}

func SeedTestUser(t *testing.T, store model.UserStore) *model.User {
	t.Helper()
	providerID := fmt.Sprintf("test-%d", time.Now().UnixNano())
	email := providerID + "@example.com"
	user, err := store.CreateOrUpdate(context.Background(), "google", providerID, email, "Test User", "")
	if err != nil {
		t.Fatalf("testfixture.SeedTestUser: %v", err)
	}
	return user
}
