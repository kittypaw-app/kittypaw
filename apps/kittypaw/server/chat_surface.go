package server

import (
	"fmt"
	"net/http"
)

// handleChatBootstrap returns only the data required to open a browser chat
// session. It deliberately does not expose the control API key used by
// /api/v1, so this route can become the narrow local target for a relay.
func (s *Server) handleChatBootstrap(w http.ResponseWriter, r *http.Request) {
	acct, status, err := s.chatSurfaceAccount(r)
	if err != nil {
		writeError(w, status, err.Error())
		return
	}

	setupCompleted := false
	if acct != nil && acct.Deps != nil && acct.Deps.Store != nil {
		cfg := acct.Runtime.Config
		if cfg == nil && acct.Deps.Account != nil {
			cfg = acct.Deps.Account.Config
		}
		if cfg != nil {
			setupCompleted = s.isOnboardingCompletedFor(acct.Deps.Store, cfg)
		}
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"surface":         "chat",
		"account_id":      acct.ID,
		"is_default":      acct.ID == s.defaultAccountID(),
		"setup_completed": setupCompleted,
		"ws_url":          websocketURL(r, "/chat/ws"),
	})
}

func (s *Server) requestChatSurfaceAccount(r *http.Request) (*requestAccount, error) {
	acct, _, err := s.chatSurfaceAccount(r)
	return acct, err
}

func (s *Server) chatSurfaceAccount(r *http.Request) (*requestAccount, int, error) {
	if accountID, ok := s.webSessionAccountID(r); ok {
		acct, err := s.requestAccountByID(accountID)
		if err != nil {
			return nil, http.StatusUnauthorized, err
		}
		return acct, http.StatusOK, nil
	}

	token := requestAuthToken(r)
	if token != "" {
		if accountID, ok := s.webSessionTokenAccountID(token); ok {
			acct, err := s.requestAccountByID(accountID)
			if err != nil {
				return nil, http.StatusUnauthorized, err
			}
			return acct, http.StatusOK, nil
		}
		acct, ok, err := s.requestAccountByAPIKey(token)
		if err != nil {
			return nil, http.StatusUnauthorized, err
		}
		if ok {
			return acct, http.StatusOK, nil
		}
	}

	required, err := s.localAuthRequired()
	if err != nil {
		return nil, http.StatusInternalServerError, fmt.Errorf("read local auth store")
	}
	if required {
		return nil, http.StatusUnauthorized, fmt.Errorf("unauthorized")
	}
	if !isLocalhost(r) {
		return nil, http.StatusForbidden, fmt.Errorf("chat bootstrap requires a local request or a web session")
	}

	deps := s.activeAccountDeps()
	if len(deps) != 1 {
		return nil, http.StatusUnauthorized, fmt.Errorf("unauthorized")
	}
	acct, err := s.requestAccountFromDeps(deps[0])
	if err != nil {
		return nil, http.StatusUnauthorized, err
	}
	return acct, http.StatusOK, nil
}

func websocketURL(r *http.Request, path string) string {
	scheme := "ws"
	if r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https" {
		scheme = "wss"
	}
	return fmt.Sprintf("%s://%s%s", scheme, r.Host, path)
}
