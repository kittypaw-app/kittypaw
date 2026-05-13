package engine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"path/filepath"

	"github.com/jinto/kittypaw/core"
)

const crossAccountTeamSpaceDenied = "cross-account read: target is not the team space"

// executeShare implements Share.read — the sole cross-account file read path.
// Access control lives in core.ValidateSharedReadPath; this layer plumbs the
// reader identity and emits the cross_account_read audit record unconditionally
// so data-flow auditing never relies on callers remembering to log.
func executeShare(_ context.Context, call core.SkillCall, s *AccountRuntime) (string, error) {
	if call.Method != "read" {
		return jsonResult(map[string]any{"error": fmt.Sprintf("unknown Share method: %s", call.Method)})
	}

	if s.AccountRegistry == nil || s.AccountID == "" {
		return jsonResult(map[string]any{"error": "Share.read unavailable: account context not configured"})
	}

	if len(call.Args) < 2 {
		return jsonResult(map[string]any{"error": "Share.read(accountID, path) requires two arguments"})
	}
	var targetID string
	if err := json.Unmarshal(call.Args[0], &targetID); err != nil {
		return jsonResult(map[string]any{"error": "accountID must be a string"})
	}
	if err := core.ValidateAccountID(targetID); err != nil {
		return jsonResult(map[string]any{"error": err.Error()})
	}
	var reqPath string
	if err := json.Unmarshal(call.Args[1], &reqPath); err != nil {
		return jsonResult(map[string]any{"error": "path must be a string"})
	}

	// Team-space-only target gate. Closes the case where a personal account's
	// config contains legacy [share.<peer>] entries or other stale sharing
	// knobs. Reachability is a property of the target account's role; member
	// authorization is enforced by core.ValidateSharedReadPath below.
	//
	// "unknown account" and "not team space" collapse into one externally-visible
	// outcome so a caller cannot enumerate account IDs by probing for which
	// error string comes back; the audit log keeps the distinction internally
	// for forensics. Same reason both branches share the rejection message.
	owner := s.AccountRegistry.Get(targetID)
	if owner == nil || owner.Config == nil || !owner.Config.IsTeamSpaceAccount() {
		reason := "target_not_team_space"
		if owner == nil {
			reason = "unknown_account"
		}
		slog.Warn("cross_account_read_rejected",
			"from", s.AccountID, "to", targetID, "path", filepath.Clean(reqPath), "reason", reason)
		return jsonResult(map[string]any{"error": crossAccountTeamSpaceDenied})
	}

	realPath, err := core.ValidateSharedReadPath(owner.Config, owner.BaseDir, s.AccountID, reqPath)
	if err != nil {
		// Clean the logged path so newlines in reqPath can't forge fake audit lines.
		slog.Warn("cross_account_read_rejected",
			"from", s.AccountID, "to", targetID, "path", filepath.Clean(reqPath), "error", err.Error())
		if errors.Is(err, core.ErrCrossAccountUnauthorized) {
			return jsonResult(map[string]any{"error": crossAccountTeamSpaceDenied})
		}
		return jsonResult(map[string]any{"error": err.Error()})
	}

	// O_NOFOLLOW closes the TOCTOU window between EvalSymlinks validation and
	// the actual open — if realPath's last component is swapped to a symlink
	// after validation, the open fails rather than following it. Windows
	// degrades to a plain O_RDONLY (see openNoFollow_windows.go).
	f, err := openNoFollow(realPath)
	if err != nil {
		return jsonResult(map[string]any{"error": fmt.Sprintf("read: %v", err)})
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return jsonResult(map[string]any{"error": fmt.Sprintf("read: %v", err)})
	}
	if info.Size() > maxFileReadSize {
		return jsonResult(map[string]any{"error": fmt.Sprintf("file too large: %d bytes (max %d)", info.Size(), maxFileReadSize)})
	}
	data, err := io.ReadAll(io.LimitReader(f, maxFileReadSize+1))
	if err != nil {
		return jsonResult(map[string]any{"error": fmt.Sprintf("read: %v", err)})
	}

	slog.Info("cross_account_read",
		"from", s.AccountID, "to", targetID, "path", filepath.Clean(reqPath), "bytes", len(data))

	return jsonResult(map[string]any{"content": string(data)})
}
