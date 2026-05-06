package auth

import (
	"crypto/rsa"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/golang-jwt/jwt/v5"

	"github.com/kittypaw-app/kittyportal/internal/model"
)

const (
	// DeviceAccessTokenTTL — daemon access JWT lifetime. Matches user
	// AccessTokenTTL by design (single TTL across the cutover).
	DeviceAccessTokenTTL = 15 * time.Minute

	// DeviceRefreshTokenTTL — opaque refresh token lifetime. Longer
	// than user refresh because daemons run continuously.
	DeviceRefreshTokenTTL = 30 * 24 * time.Hour

	// maxDevicePairBodyBytes caps the pair request body. capabilities
	// is a nested object (daemon_version, supported_protocols, etc.) —
	// 4 KiB is enough for legitimate payloads while still bounding abuse.
	maxDevicePairBodyBytes = 4 * 1024

	// maxDeviceNameBytes caps the device name length to prevent storage
	// amplification — body cap alone wouldn't stop an attacker from
	// stuffing the entire 4 KiB into the name field.
	maxDeviceNameBytes = 100

	// maxDeviceCapabilitiesBytes caps the JSON-encoded capabilities
	// object size. Daemons advertise feature flags in capabilities;
	// 2 KiB accommodates current daemon contracts (daemon_version,
	// supported_protocols, hostname, OS info) with room to spare.
	maxDeviceCapabilitiesBytes = 2 * 1024
)

// deviceClaimsPayload is the wire shape of a device JWT. Mirrors
// testfixture.DeviceClaims's payload struct so the cross-team
// contract test (TestSignDeviceJWT_WireFormatMatchesIssueDeviceJWT)
// pins both ends to the same structure. Drift here = silent verifier
// breakage on kittychat side.
//
// docs/specs/kittychat-credential-foundation.md D5.
type deviceClaimsPayload struct {
	UserID string   `json:"user_id"`
	Scope  []string `json:"scope"`
	V      int      `json:"v"`
	jwt.RegisteredClaims
}

// SignDeviceJWT issues an RS256 device JWT.
//
// Wire format (Plan 23 PR-D + spec D5):
//   - alg=RS256, kid in header
//   - sub=device:<deviceID>, user_id=<userID> as separate claim
//   - aud=[AudienceChat, AudienceHome], scope=[ScopeDaemonConnect], v=ClaimsVersion
//   - iss=Issuer, iat/exp set
//
// The user_id is a separate claim (not embedded in sub) so kittychat's
// verifier can extract user scope without parsing the sub prefix —
// matches IssueDeviceJWT (testfixture/jwt.go).
func SignDeviceJWT(userID, deviceID string, key *rsa.PrivateKey, kid string, ttl time.Duration) (string, error) {
	if key == nil {
		return "", fmt.Errorf("private key is nil")
	}
	if kid == "" {
		return "", fmt.Errorf("kid is empty")
	}
	now := time.Now()
	payload := deviceClaimsPayload{
		UserID: userID,
		Scope:  []string{ScopeDaemonConnect},
		V:      ClaimsVersion,
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    Issuer,
			Subject:   "device:" + deviceID,
			Audience:  jwt.ClaimStrings{AudienceChat, AudienceHome},
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(now.Add(ttl)),
		},
	}
	token := jwt.NewWithClaims(jwt.SigningMethodRS256, payload)
	token.Header["kid"] = kid
	return token.SignedString(key)
}

// pairRequest is the JSON body for POST /auth/devices/pair. Both
// fields are optional from the daemon's perspective: name defaults
// to a daemon-supplied label, capabilities defaults to {}.
type pairRequest struct {
	Name         string         `json:"name"`
	Capabilities map[string]any `json:"capabilities"`
}

// pairResponse is the JSON shape returned by /auth/devices/pair and
// /auth/devices/refresh. Plan 23 contract.
type pairResponse struct {
	DeviceID           string `json:"device_id"`
	DeviceAccessToken  string `json:"device_access_token"`
	DeviceRefreshToken string `json:"device_refresh_token"`
	ExpiresIn          int    `json:"expires_in"`
}

// writeJSONError emits an error response in the {"error": "..."}
// envelope used elsewhere in the API. Replaces http.Error which
// would have served text/plain — daemons and the chat-team verifier
// expect JSON for every API response. Plan 23 PR-D follow-up
// (Round 3 review LOW 0.80). Scope: device handlers only;
// cross-codebase JSON-envelope cleanup is tracked separately.
func writeJSONError(w http.ResponseWriter, msg string, status int) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": msg})
}

// logStoreErr emits a slog Error WITHOUT the raw error message —
// pgx errors can include offending parameter values (user-supplied
// name, capabilities, etc.) which would land verbatim in JSON-
// formatted prod logs (systemd journal). Only the Go error type
// is emitted; the original error message itself is dropped.
//
// Plan 23 PR-D follow-up (Round 2 review MED 0.75). A systemic
// slog ReplaceAttr handler that scrubs every "err" attr across the
// codebase is tracked as Plan 19 follow-up; this is the cheap fix
// for the user-controlled-data path that PR-D introduced.
func logStoreErr(msg string, err error, attrs ...any) {
	attrs = append(attrs, "err_type", fmt.Sprintf("%T", err))
	slog.Error(msg, attrs...)
}

// HandlePair issues a fresh device JWT + refresh token after the
// authenticated user pairs a daemon. The handler uses sequential
// explicit revoke (Plan 23 결정 2) — if any post-Create step fails,
// we revoke the just-created device row to prevent ghost listings.
//
// Rationale: pgx transaction wrapping would be cleaner but DeviceStore
// + RefreshTokenStore (PR-C) don't expose tx semantics; the soft-delete
// (revoked_at) column makes compensating-revoke a first-class option.
func (h *OAuthHandler) HandlePair() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user := UserFromContext(r.Context())
		if user == nil {
			writeJSONError(w, "unauthorized", http.StatusUnauthorized)
			return
		}

		r.Body = http.MaxBytesReader(w, r.Body, maxDevicePairBodyBytes)
		// DisallowUnknownFields intentionally NOT used — spec D4
		// contract is forward-compat (unknown fields are ignored).
		// Daemons may add new pair-request fields ahead of server bumps.
		var req pairRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSONError(w, "invalid request body", http.StatusBadRequest)
			return
		}
		// `name` is optional per spec (silly-wiggling-balloon.md L222).
		// An unnamed device is valid — daemon may label later via a
		// future PATCH endpoint. Length-capped to prevent storage
		// amplification (review HIGH 0.85 follow-up).
		if len(req.Name) > maxDeviceNameBytes {
			writeJSONError(w, "name too long", http.StatusBadRequest)
			return
		}
		// Cap capabilities by JSON-encoded size — body cap is
		// per-request, not per-field. A malicious daemon could stuff
		// the entire 4 KiB into capabilities and amplify storage.
		if req.Capabilities != nil {
			capsCheck, err := json.Marshal(req.Capabilities)
			if err != nil {
				writeJSONError(w, "invalid capabilities", http.StatusBadRequest)
				return
			}
			if len(capsCheck) > maxDeviceCapabilitiesBytes {
				writeJSONError(w, "capabilities too large", http.StatusBadRequest)
				return
			}
		}

		ctx := r.Context()
		dev, err := h.DeviceStore.Create(ctx, user.ID, req.Name, req.Capabilities)
		if err != nil {
			logStoreErr("device create failed", err, "user_id", user.ID)
			writeJSONError(w, "failed to pair device", http.StatusInternalServerError)
			return
		}

		// From here on, any failure must compensate — sequential explicit
		// revoke (Plan 23 결정 2). defer+bool would be footgun-prone.
		accessToken, err := SignDeviceJWT(user.ID, dev.ID, h.JWTPrivateKey, h.JWTKID, DeviceAccessTokenTTL)
		if err != nil {
			_ = h.DeviceStore.Revoke(ctx, dev.ID)
			logStoreErr("device JWT sign failed", err, "user_id", user.ID, "device_id", dev.ID)
			writeJSONError(w, "failed to issue token", http.StatusInternalServerError)
			return
		}

		rawRefresh, err := GenerateRefreshToken()
		if err != nil {
			_ = h.DeviceStore.Revoke(ctx, dev.ID)
			logStoreErr("refresh token generate failed", err, "user_id", user.ID, "device_id", dev.ID)
			writeJSONError(w, "failed to issue token", http.StatusInternalServerError)
			return
		}
		hash := HashRefreshToken(rawRefresh)
		if err := h.RefreshTokenStore.CreateForDevice(ctx, user.ID, dev.ID, hash, time.Now().Add(DeviceRefreshTokenTTL)); err != nil {
			_ = h.DeviceStore.Revoke(ctx, dev.ID)
			logStoreErr("refresh token store failed", err, "user_id", user.ID, "device_id", dev.ID)
			writeJSONError(w, "failed to issue token", http.StatusInternalServerError)
			return
		}

		writeTokenResponse(w, pairResponse{
			DeviceID:           dev.ID,
			DeviceAccessToken:  accessToken,
			DeviceRefreshToken: rawRefresh,
			ExpiresIn:          int(DeviceAccessTokenTTL.Seconds()),
		})
	}
}

// writeTokenResponse encodes a pairResponse with RFC 6749 §5.1
// Cache-Control headers — token responses must NEVER be cached by
// intermediate proxies or browsers (refresh token leak).
func writeTokenResponse(w http.ResponseWriter, resp pairResponse) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Pragma", "no-cache")
	_ = json.NewEncoder(w).Encode(resp)
}

// HandleDeviceRefresh rotates a device-scoped opaque refresh token.
//
// Authentication: opaque token in body is the only credential. This
// route is wired OUTSIDE the user-aud middleware (Plan 23 결정 3) so a
// daemon's stale Authorization header can't trip the user-aud check
// before this handler runs.
//
// Reuse detection: presenting an already-revoked refresh token revokes
// every active refresh for the same device (Plan 23 결정 3 — RevokeAllForDevice).
// User-scoped refresh (device_id NULL) is rejected — that's /auth/token/refresh's
// job, not this device-only endpoint.
func (h *OAuthHandler) HandleDeviceRefresh() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		r.Body = http.MaxBytesReader(w, r.Body, maxAuthBodyBytes)
		var req struct {
			RefreshToken string `json:"refresh_token"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.RefreshToken == "" {
			writeJSONError(w, "refresh_token required", http.StatusBadRequest)
			return
		}

		ctx := r.Context()
		hash := HashRefreshToken(req.RefreshToken)
		rt, err := h.RefreshTokenStore.FindByHash(ctx, hash)
		if err != nil {
			// Unknown hash → silent 401 (don't disclose existence).
			writeJSONError(w, "invalid refresh token", http.StatusUnauthorized)
			return
		}
		// Device-only endpoint guard.
		if rt.DeviceID == nil {
			writeJSONError(w, "invalid refresh token", http.StatusUnauthorized)
			return
		}
		// Reuse detection — already-revoked refresh signals a leaked token.
		// Revoke every active device refresh on the same device.
		if rt.RevokedAt != nil {
			if rerr := h.RefreshTokenStore.RevokeAllForDevice(ctx, *rt.DeviceID); rerr != nil {
				logStoreErr("RevokeAllForDevice failed during reuse-detect", rerr, "device_id", *rt.DeviceID)
			}
			writeJSONError(w, "invalid refresh token", http.StatusUnauthorized)
			return
		}
		if rt.ExpiresAt.Before(time.Now()) {
			writeJSONError(w, "invalid refresh token", http.StatusUnauthorized)
			return
		}

		// Verify device is still active (not deleted) BEFORE rotation —
		// no point burning a new refresh row if the device is gone.
		dev, err := h.DeviceStore.FindByID(ctx, *rt.DeviceID)
		if err != nil {
			writeJSONError(w, "invalid refresh token", http.StatusUnauthorized)
			return
		}
		if dev.RevokedAt != nil {
			writeJSONError(w, "invalid refresh token", http.StatusUnauthorized)
			return
		}

		// Issue new pair. Generate token bytes BEFORE rotation so a
		// crypto/rand failure can't leave the rotation half-done.
		accessToken, err := SignDeviceJWT(rt.UserID, dev.ID, h.JWTPrivateKey, h.JWTKID, DeviceAccessTokenTTL)
		if err != nil {
			logStoreErr("SignDeviceJWT failed", err, "device_id", dev.ID)
			writeJSONError(w, "failed to issue token", http.StatusInternalServerError)
			return
		}
		rawRefresh, err := GenerateRefreshToken()
		if err != nil {
			logStoreErr("GenerateRefreshToken failed", err, "device_id", dev.ID)
			writeJSONError(w, "failed to issue token", http.StatusInternalServerError)
			return
		}
		newHash := HashRefreshToken(rawRefresh)

		// Atomic rotation — revoke old + insert new in a single
		// transaction. Replaces the pre-fix two-step (RevokeIfActive
		// + CreateForDevice as separate pool ops) which had a self-
		// lockout race window if the new INSERT failed after the old
		// revoke committed (Plan 23 follow-up review HIGH 0.85 fix).
		if err := h.RefreshTokenStore.RotateForDevice(ctx, rt.ID, rt.UserID, dev.ID, newHash, time.Now().Add(DeviceRefreshTokenTTL)); err != nil {
			// Race-loser: another request rotated this row first.
			// Silent 401 (don't disclose which path failed).
			if errors.Is(err, model.ErrRotationAborted) {
				writeJSONError(w, "invalid refresh token", http.StatusUnauthorized)
				return
			}
			logStoreErr("RotateForDevice failed", err, "device_id", dev.ID)
			writeJSONError(w, "failed to issue token", http.StatusInternalServerError)
			return
		}

		// Best-effort idle signal for the lifecycle janitor (Plan 24 T1).
		// Outside the rotation transaction by design — a Touch failure
		// (DB blip, etc.) must not roll back a successful refresh, and
		// the next refresh (within DeviceAccessTokenTTL=15min) will set
		// last_used_at then. Worst case: 15 extra minutes of staleness
		// before idle-reaping starts the 60-day clock.
		if err := h.DeviceStore.Touch(ctx, dev.ID); err != nil {
			logStoreErr("DeviceStore.Touch failed (best-effort, ignored)", err, "device_id", dev.ID)
		}

		writeTokenResponse(w, pairResponse{
			DeviceID:           dev.ID,
			DeviceAccessToken:  accessToken,
			DeviceRefreshToken: rawRefresh,
			ExpiresIn:          int(DeviceAccessTokenTTL.Seconds()),
		})
	}
}

// HandleDevicesList returns the authenticated user's active devices,
// sorted by paired_at DESC (PR-C ListActiveForUser contract).
//
// Empty result MUST encode as `[]`, not `null` — Go's nil slice
// marshaling default would surface as null and break clients that
// type the field as `Array<Device>`.
func (h *OAuthHandler) HandleDevicesList() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user := UserFromContext(r.Context())
		if user == nil {
			writeJSONError(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		list, err := h.DeviceStore.ListActiveForUser(r.Context(), user.ID)
		if err != nil {
			logStoreErr("ListActiveForUser failed", err, "user_id", user.ID)
			writeJSONError(w, "internal error", http.StatusInternalServerError)
			return
		}
		if list == nil {
			list = []*model.Device{}
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(list)
	}
}

// HandleDeviceDelete soft-deletes a device the authenticated user owns,
// then revokes every active device-scoped refresh token for that device.
//
// All "not your device" cases (missing, invalid UUID, owned by another
// user, already revoked) collapse to 404 — non-disclosure (Plan 23 결정 5).
// Refresh revoke runs first; a failure there leaves the device active
// rather than orphaning live tokens after a half-completed delete.
func (h *OAuthHandler) HandleDeviceDelete() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user := UserFromContext(r.Context())
		if user == nil {
			writeJSONError(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		deviceID := chi.URLParam(r, "id")
		if deviceID == "" {
			writeJSONError(w, "device not found", http.StatusNotFound)
			return
		}

		ctx := r.Context()
		dev, err := h.DeviceStore.FindByID(ctx, deviceID)
		if err != nil {
			// ErrNotFound, invalid UUID (pgx 22P02), or any other lookup
			// error — collapse to 404 for non-disclosure consistency.
			writeJSONError(w, "device not found", http.StatusNotFound)
			return
		}
		if dev.UserID != user.ID || dev.RevokedAt != nil {
			writeJSONError(w, "device not found", http.StatusNotFound)
			return
		}

		// Refresh revoke first — if it fails, device stays alive (no
		// half-deleted state with orphan refresh).
		if err := h.RefreshTokenStore.RevokeAllForDevice(ctx, deviceID); err != nil {
			logStoreErr("RevokeAllForDevice failed", err, "device_id", deviceID)
			writeJSONError(w, "internal error", http.StatusInternalServerError)
			return
		}
		if err := h.DeviceStore.Revoke(ctx, deviceID); err != nil {
			logStoreErr("DeviceStore.Revoke failed", err, "device_id", deviceID)
			writeJSONError(w, "internal error", http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte("{}"))
	}
}
