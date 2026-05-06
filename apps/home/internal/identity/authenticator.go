package identity

import (
	"net/http"
	"strings"

	"github.com/kittypaw-app/kittyhome/internal/broker"
	"github.com/kittypaw-app/kittyhome/internal/openai"
)

type APIAuthenticator struct {
	Verifier CredentialVerifier
}

func (a APIAuthenticator) Authenticate(r *http.Request) (openai.Principal, error) {
	if a.Verifier == nil {
		return openai.Principal{}, ErrUnauthorized
	}
	claims, err := a.Verifier.VerifyAPIClient(r.Context(), requestToken(r, "x-api-key"))
	if err != nil {
		return openai.Principal{}, err
	}
	return claims.Principal(), nil
}

type DeviceAuthenticator struct {
	Verifier CredentialVerifier
}

func (a DeviceAuthenticator) Authenticate(r *http.Request) (broker.DevicePrincipal, error) {
	if a.Verifier == nil {
		return broker.DevicePrincipal{}, ErrUnauthorized
	}
	claims, err := a.Verifier.VerifyDevice(r.Context(), requestToken(r, "x-device-token"))
	if err != nil {
		return broker.DevicePrincipal{}, err
	}
	return claims.Principal(), nil
}

func requestToken(r *http.Request, fallbackHeader string) string {
	auth := r.Header.Get("Authorization")
	if strings.HasPrefix(auth, "Bearer ") {
		return strings.TrimPrefix(auth, "Bearer ")
	}
	if token := r.Header.Get(fallbackHeader); token != "" {
		return token
	}
	return ""
}
