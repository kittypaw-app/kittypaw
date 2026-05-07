package connect

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/kittypaw-app/kittyportal/internal/auth"
	"github.com/kittypaw-app/kittyportal/internal/model"
)

const xBrokerRefreshSkew = 5 * time.Minute

type EntitlementQuotaReader interface {
	UserQuotaJSON(context.Context, string, string) (map[string]any, error)
}

func (h *Handler) HandleXBrokerSearchRecent() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user, token, quota, ok := h.prepareXBrokerRequest(w, r)
		if !ok {
			return
		}
		query := strings.TrimSpace(r.URL.Query().Get("query"))
		if query == "" {
			writeBrokerError(w, http.StatusBadRequest, "bad_request", "query required")
			return
		}

		limit := parseBrokerLimit(r, 10)
		result, err := h.X.SearchRecent(r.Context(), token.AccessToken, query, limit)
		if err != nil {
			slog.Error("x broker search failed", "user_id", user.ID, "err", err)
			writeBrokerError(w, http.StatusBadGateway, "x_failed", "x search failed")
			return
		}
		allowed, err := h.recordXPostReads(r.Context(), user.ID, "search_recent", len(result.Posts), quota, map[string]any{
			"query": query,
			"limit": limit,
		})
		if err != nil {
			slog.Error("x broker usage record failed", "user_id", user.ID, "err", err)
			writeBrokerError(w, http.StatusInternalServerError, "usage_failed", "usage record failed")
			return
		}
		if !allowed {
			writeBrokerError(w, http.StatusTooManyRequests, "quota_exceeded", "monthly post read quota exceeded")
			return
		}
		writeBrokerJSON(w, result)
	}
}

func (h *Handler) HandleXBrokerUserByUsername() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		_, token, _, ok := h.prepareXBrokerRequest(w, r)
		if !ok {
			return
		}
		username := strings.TrimPrefix(strings.TrimSpace(chi.URLParam(r, "username")), "@")
		if username == "" {
			writeBrokerError(w, http.StatusBadRequest, "bad_request", "username required")
			return
		}
		user, err := h.X.UserByUsername(r.Context(), token.AccessToken, username)
		if err != nil {
			slog.Error("x broker user lookup failed", "username", username, "err", err)
			writeBrokerError(w, http.StatusBadGateway, "x_failed", "x user lookup failed")
			return
		}
		writeBrokerJSON(w, user)
	}
}

func (h *Handler) HandleXBrokerUserPostsByUsername() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user, token, quota, ok := h.prepareXBrokerRequest(w, r)
		if !ok {
			return
		}
		username := strings.TrimPrefix(strings.TrimSpace(chi.URLParam(r, "username")), "@")
		if username == "" {
			writeBrokerError(w, http.StatusBadRequest, "bad_request", "username required")
			return
		}
		xUser, err := h.X.UserByUsername(r.Context(), token.AccessToken, username)
		if err != nil {
			slog.Error("x broker user lookup failed", "username", username, "err", err)
			writeBrokerError(w, http.StatusBadGateway, "x_failed", "x user lookup failed")
			return
		}
		limit := parseBrokerLimit(r, 10)
		result, err := h.X.UserPosts(r.Context(), token.AccessToken, xUser.ID, limit)
		if err != nil {
			slog.Error("x broker user posts failed", "user_id", user.ID, "username", username, "err", err)
			writeBrokerError(w, http.StatusBadGateway, "x_failed", "x user posts failed")
			return
		}
		allowed, err := h.recordXPostReads(r.Context(), user.ID, "user_posts", len(result.Posts), quota, map[string]any{
			"username":  username,
			"x_user_id": xUser.ID,
			"limit":     limit,
		})
		if err != nil {
			slog.Error("x broker usage record failed", "user_id", user.ID, "err", err)
			writeBrokerError(w, http.StatusInternalServerError, "usage_failed", "usage record failed")
			return
		}
		if !allowed {
			writeBrokerError(w, http.StatusTooManyRequests, "quota_exceeded", "monthly post read quota exceeded")
			return
		}
		writeBrokerJSON(w, result)
	}
}

func (h *Handler) HandleXBrokerTweetByID() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user, token, quota, ok := h.prepareXBrokerRequest(w, r)
		if !ok {
			return
		}
		tweetID := strings.TrimSpace(chi.URLParam(r, "id"))
		if tweetID == "" {
			writeBrokerError(w, http.StatusBadRequest, "bad_request", "tweet id required")
			return
		}
		post, err := h.X.TweetByID(r.Context(), token.AccessToken, tweetID)
		if err != nil {
			slog.Error("x broker tweet lookup failed", "user_id", user.ID, "tweet_id", tweetID, "err", err)
			writeBrokerError(w, http.StatusBadGateway, "x_failed", "x tweet lookup failed")
			return
		}
		quantity := 0
		if post.ID != "" {
			quantity = 1
		}
		allowed, err := h.recordXPostReads(r.Context(), user.ID, "tweet_lookup", quantity, quota, map[string]any{
			"tweet_id": tweetID,
		})
		if err != nil {
			slog.Error("x broker usage record failed", "user_id", user.ID, "err", err)
			writeBrokerError(w, http.StatusInternalServerError, "usage_failed", "usage record failed")
			return
		}
		if !allowed {
			writeBrokerError(w, http.StatusTooManyRequests, "quota_exceeded", "monthly post read quota exceeded")
			return
		}
		writeBrokerJSON(w, post)
	}
}

func (h *Handler) prepareXBrokerRequest(w http.ResponseWriter, r *http.Request) (*model.User, ProviderTokenRecord, int, bool) {
	setSensitiveResponseHeaders(w.Header())
	user := auth.UserFromContext(r.Context())
	if user == nil || user.ID == "" {
		writeBrokerError(w, http.StatusUnauthorized, "unauthorized", "authentication required")
		return nil, ProviderTokenRecord{}, 0, false
	}
	if h == nil || h.X == nil || h.TokenStore == nil || h.Entitlements == nil {
		writeBrokerError(w, http.StatusInternalServerError, "unavailable", "x broker unavailable")
		return nil, ProviderTokenRecord{}, 0, false
	}
	allowed, err := h.Entitlements.UserAllowed(r.Context(), user.ID, XProviderID)
	if err != nil {
		slog.Error("x broker entitlement check failed", "user_id", user.ID, "err", err)
		writeBrokerError(w, http.StatusInternalServerError, "entitlement_failed", "entitlement check failed")
		return nil, ProviderTokenRecord{}, 0, false
	}
	if !allowed {
		writeBrokerError(w, http.StatusForbidden, "forbidden", "x access not allowed")
		return nil, ProviderTokenRecord{}, 0, false
	}
	quota, err := h.xMonthlyPostReadLimit(r.Context(), user.ID)
	if err != nil {
		slog.Error("x broker quota check failed", "user_id", user.ID, "err", err)
		writeBrokerError(w, http.StatusInternalServerError, "quota_failed", "quota check failed")
		return nil, ProviderTokenRecord{}, 0, false
	}
	token, err := h.loadXBrokerToken(r.Context(), user.ID)
	if err != nil {
		if errors.Is(err, ErrProviderTokenNotFound) {
			writeBrokerError(w, http.StatusForbidden, "x_not_connected", "x account not connected")
			return nil, ProviderTokenRecord{}, 0, false
		}
		slog.Error("x broker token load failed", "user_id", user.ID, "err", err)
		writeBrokerError(w, http.StatusInternalServerError, "token_failed", "x token unavailable")
		return nil, ProviderTokenRecord{}, 0, false
	}
	return user, token, quota, true
}

func (h *Handler) loadXBrokerToken(ctx context.Context, userID string) (ProviderTokenRecord, error) {
	token, err := h.TokenStore.LoadProviderToken(ctx, userID, XProviderID)
	if err != nil {
		return ProviderTokenRecord{}, err
	}
	if token.ExpiresAt == nil || token.ExpiresAt.After(time.Now().Add(xBrokerRefreshSkew)) {
		return token, nil
	}
	if token.RefreshToken == "" {
		return ProviderTokenRecord{}, fmt.Errorf("x token expired without refresh token")
	}
	refreshed, err := h.X.Refresh(ctx, token.RefreshToken)
	if err != nil {
		return ProviderTokenRecord{}, fmt.Errorf("refresh x token: %w", err)
	}
	if refreshed.RefreshToken == "" {
		refreshed.RefreshToken = token.RefreshToken
	}
	if refreshed.Username == "" {
		refreshed.Username = token.Username
	}
	expiresAt := tokenExpiresAt(refreshed)
	next := ProviderTokenRecord{
		UserID:       userID,
		ProviderID:   XProviderID,
		AccessToken:  refreshed.AccessToken,
		RefreshToken: refreshed.RefreshToken,
		TokenType:    refreshed.TokenType,
		Scope:        refreshed.Scope,
		Username:     refreshed.Username,
		ExpiresAt:    expiresAt,
	}
	if err := h.TokenStore.SaveProviderToken(ctx, next); err != nil {
		return ProviderTokenRecord{}, err
	}
	return next, nil
}

func (h *Handler) xMonthlyPostReadLimit(ctx context.Context, userID string) (int, error) {
	reader, ok := h.Entitlements.(EntitlementQuotaReader)
	if !ok {
		return 0, nil
	}
	quota, err := reader.UserQuotaJSON(ctx, userID, XProviderID)
	if err != nil {
		return 0, err
	}
	return parseMonthlyPostReadLimit(quota), nil
}

func (h *Handler) recordXPostReads(ctx context.Context, userID, operation string, quantity, monthlyLimit int, metadata map[string]any) (bool, error) {
	if quantity == 0 {
		return true, nil
	}
	return h.TokenStore.RecordUsage(ctx, UsageRecord{
		UserID:       userID,
		ProviderID:   XProviderID,
		Operation:    operation,
		Quantity:     quantity,
		MonthlyLimit: monthlyLimit,
		Now:          time.Now().UTC(),
		Metadata:     metadata,
	})
}

func parseMonthlyPostReadLimit(quota map[string]any) int {
	if quota == nil {
		return 0
	}
	return parsePositiveInt(quota["monthly_post_reads"])
}

func parsePositiveInt(value any) int {
	switch v := value.(type) {
	case nil:
		return 0
	case int:
		if v > 0 {
			return v
		}
	case int64:
		if v > 0 && v <= int64(int(^uint(0)>>1)) {
			return int(v)
		}
	case float64:
		if v > 0 {
			return int(v)
		}
	case json.Number:
		n, err := v.Int64()
		if err == nil && n > 0 && n <= int64(int(^uint(0)>>1)) {
			return int(n)
		}
	case string:
		n, err := strconv.Atoi(strings.TrimSpace(v))
		if err == nil && n > 0 {
			return n
		}
	}
	return 0
}

func parseBrokerLimit(r *http.Request, fallback int) int {
	raw := strings.TrimSpace(r.URL.Query().Get("limit"))
	if raw == "" {
		raw = strings.TrimSpace(r.URL.Query().Get("max_results"))
	}
	if raw == "" {
		return fallback
	}
	limit, err := strconv.Atoi(raw)
	if err != nil || limit <= 0 {
		return fallback
	}
	if limit > 100 {
		return 100
	}
	return limit
}

func writeBrokerJSON(w http.ResponseWriter, value any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(value)
}

func writeBrokerError(w http.ResponseWriter, status int, code, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"error": map[string]string{
			"code":    code,
			"message": message,
		},
	})
}
