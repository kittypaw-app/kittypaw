package engine

import (
	"bytes"
	"context"
	"database/sql"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/jinto/kittypaw/store"
)

// ---------------------------------------------------------------------------
// Interface + types
// ---------------------------------------------------------------------------

// Indexer provides full-text search over workspace files.
//
// Index/Remove operate on whole workspaces (bulk walk). IndexFile/RemoveFile
// operate on single files — used by the live indexer (fsnotify) for
// incremental updates after initial Index.
type Indexer interface {
	Index(ctx context.Context, workspaceID, rootPath string) (*IndexResult, error)
	IndexFile(ctx context.Context, workspaceID, rootPath, absPath string) error
	Remove(workspaceID string) error
	RemoveFile(workspaceID, absPath string) error
	Search(ctx context.Context, query string, opts SearchOptions) (*SearchResult, error)
	Stats(ctx context.Context, opts StatsOptions) (*IndexStats, error)
	Reindex(ctx context.Context, workspaceID, rootPath string) (*IndexResult, error)
	Close() error
}

// SearchOptions controls File.search behavior.
type SearchOptions struct {
	Path         string   `json:"path"`   // relative path prefix filter
	Ext          string   `json:"ext"`    // extension filter (e.g. ".go")
	Limit        int      `json:"limit"`  // default 20, max 100
	Offset       int      `json:"offset"` // pagination
	WorkspaceIDs []string `json:"-"`      // internal project/ticket conversation scope
}

// SearchResult is the response from File.search.
type SearchResult struct {
	Files []SearchHit `json:"files"`
	Total int         `json:"total"`
}

// SearchHit is one matching file.
type SearchHit struct {
	Path     string    `json:"path"`
	Score    float64   `json:"score"`
	Snippets []Snippet `json:"snippets"`
}

// Snippet is a matching line within a file.
type Snippet struct {
	Line int    `json:"line"`
	Text string `json:"text"`
}

// StatsOptions controls File.stats behavior.
type StatsOptions struct {
	Path         string   `json:"path"` // relative path prefix filter
	WorkspaceIDs []string `json:"-"`    // internal project/ticket conversation scope
}

// IndexStats is the response from File.stats.
type IndexStats struct {
	TotalFiles   int                      `json:"total_files"`
	IndexedFiles int                      `json:"indexed_files"`
	TotalSize    int64                    `json:"total_size_bytes"`
	ByExtension  map[string]ExtensionStat `json:"by_extension"`
	IndexedAt    string                   `json:"indexed_at"`
}

// ExtensionStat aggregates file count and total size for one extension.
type ExtensionStat struct {
	Count int   `json:"count"`
	Size  int64 `json:"size"`
}

// IndexResult summarizes one indexing run.
type IndexResult struct {
	Indexed    int   `json:"indexed"`
	Skipped    int   `json:"skipped"`
	Errors     int   `json:"errors"`
	DurationMs int64 `json:"duration_ms"`
}

// ---------------------------------------------------------------------------
// FTS5Indexer
// ---------------------------------------------------------------------------

const (
	maxIndexFileSize = 1 << 20 // 1 MB content indexing limit
	binaryProbeSize  = 8192    // first 8 KB for binary detection
	txChunkSize      = 500     // files per transaction chunk
)

// excludedDirs are directories completely skipped during indexing.
var excludedDirs = map[string]bool{
	".git":         true,
	"node_modules": true,
	"vendor":       true,
	"__pycache__":  true,
	"build":        true,
	"dist":         true,
}

// excludedFiles are individual filenames completely skipped.
var excludedFiles = map[string]bool{
	".DS_Store": true,
}

// FTS5Indexer implements Indexer using SQLite FTS5.
type FTS5Indexer struct {
	store    *store.Store
	indexing sync.Map // workspace_id -> bool (concurrency guard)
}

// NewFTS5Indexer creates a new FTS5-backed indexer.
func NewFTS5Indexer(st *store.Store) *FTS5Indexer {
	return &FTS5Indexer{store: st}
}

// fileIndexOp captures data needed to update the FTS index for a single file
// after the metadata transaction commits. FTS5 DELETE+INSERT on the same
// rowid inside a transaction can produce stale tokens, so FTS upserts run
// after the metadata tx commits.
type fileIndexOp struct {
	id       int64
	filename string
	body     string
}

// processFileTx reads the file at absPath, determines content eligibility
// (size ≤ maxIndexFileSize + non-binary), and upserts metadata in tx.
// Returns the FTS payload for post-commit upsert.
//
// Shared between Index (bulk walk) and IndexFile (single file). The readErr
// return is non-fatal — content-read failure still upserts metadata with
// hasContent=false so the file remains searchable by path. Callers use it
// only to bump error counters. A non-nil error return indicates a terminal
// failure (rel-path or metadata upsert) that prevented the upsert.
func (ix *FTS5Indexer) processFileTx(tx *sql.Tx, workspaceID, rootPath, absPath string, info fs.FileInfo) (op fileIndexOp, readErr error, err error) {
	name := filepath.Base(absPath)
	relPath, relErr := filepath.Rel(rootPath, absPath)
	if relErr != nil {
		return fileIndexOp{}, nil, fmt.Errorf("rel path: %w", relErr)
	}
	ext := filepath.Ext(name)

	hasContent := false
	var body string
	if info.Size() <= maxIndexFileSize && !isBinary(absPath) {
		data, rErr := readBounded(absPath, maxIndexFileSize)
		if rErr != nil {
			readErr = rErr
		} else {
			hasContent = true
			body = string(data)
		}
	}

	wf := &store.WorkspaceFile{
		WorkspaceID: workspaceID,
		AbsPath:     absPath,
		RelPath:     relPath,
		Filename:    name,
		Extension:   ext,
		Size:        info.Size(),
		ModifiedAt:  info.ModTime().UTC().Format(time.RFC3339),
		HasContent:  hasContent,
	}

	id, upErr := ix.store.UpsertWorkspaceFileTx(tx, wf)
	if upErr != nil {
		return fileIndexOp{}, readErr, fmt.Errorf("upsert metadata: %w", upErr)
	}
	return fileIndexOp{id: id, filename: name, body: body}, readErr, nil
}

// Index walks rootPath and indexes all eligible files for the given workspace.
// If the workspace is already being indexed, returns immediately with nil.
func (ix *FTS5Indexer) Index(ctx context.Context, workspaceID, rootPath string) (*IndexResult, error) {
	// Concurrency guard: skip if already indexing this workspace.
	if _, loaded := ix.indexing.LoadOrStore(workspaceID, true); loaded {
		return &IndexResult{}, nil
	}
	defer ix.indexing.Delete(workspaceID)

	start := time.Now()

	// Verify root path exists.
	info, err := os.Stat(rootPath)
	if err != nil {
		return nil, fmt.Errorf("workspace root: %w", err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("workspace root is not a directory: %s", rootPath)
	}

	result := &IndexResult{}

	// Collect files to index in batches. workspace_files metadata is batched
	// in a transaction for performance; FTS5 updates are applied individually
	// after each batch commit (FTS5 DELETE+INSERT with the same rowid inside
	// a transaction can cause stale token issues). fileIndexOp is defined at
	// package level so processFileTx / IndexFile can share the same struct.
	var ftsBatch []fileIndexOp
	var tx *sql.Tx
	pending := 0

	commitChunk := func() error {
		if tx != nil {
			if err := tx.Commit(); err != nil {
				tx.Rollback() // explicit cleanup (SQLite may auto-rollback)
				tx = nil
				pending = 0
				ftsBatch = ftsBatch[:0] // metadata not committed — drop batch
				return fmt.Errorf("commit chunk: %w", err)
			}
			tx = nil
			pending = 0
		}
		// Apply FTS updates after the metadata tx commits.
		for _, f := range ftsBatch {
			if err := ix.store.UpsertWorkspaceFTS(f.id, f.filename, f.body); err != nil {
				result.Errors++
				slog.Debug("index: fts update failed", "id", f.id, "error", err)
			}
		}
		ftsBatch = ftsBatch[:0]
		return nil
	}

	walkErr := filepath.WalkDir(rootPath, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			result.Errors++
			return nil // continue walking
		}

		// Check context cancellation.
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		name := d.Name()

		// Skip excluded directories.
		if d.IsDir() {
			if excludedDirs[name] {
				return filepath.SkipDir
			}
			return nil
		}

		// Skip excluded files.
		if excludedFiles[name] {
			result.Skipped++
			return nil
		}

		fi, fiErr := d.Info()
		if fiErr != nil {
			result.Errors++
			return nil
		}

		absPath, _ := filepath.Abs(path)

		// Start a new transaction chunk if needed.
		if tx == nil {
			tx, err = ix.store.BeginTx()
			if err != nil {
				result.Errors++
				return nil
			}
		}

		op, readErr, pErr := ix.processFileTx(tx, workspaceID, rootPath, absPath, fi)
		if readErr != nil {
			result.Errors++
		}
		if pErr != nil {
			result.Errors++
			slog.Debug("index: process file failed", "path", absPath, "error", pErr)
			return nil
		}

		ftsBatch = append(ftsBatch, op)
		result.Indexed++
		pending++

		// Commit chunk when threshold reached.
		if pending >= txChunkSize {
			if cErr := commitChunk(); cErr != nil {
				result.Errors++
				slog.Warn("index: chunk commit failed", "error", cErr)
			}
		}

		return nil
	})

	// Handle remaining transaction: rollback on error, commit otherwise.
	if tx != nil {
		if walkErr != nil {
			tx.Rollback()
			tx = nil
			ftsBatch = ftsBatch[:0]
		} else if cErr := commitChunk(); cErr != nil {
			result.Errors++
		}
	}

	if walkErr != nil && walkErr != context.Canceled && walkErr != context.DeadlineExceeded {
		return result, walkErr
	}

	result.DurationMs = time.Since(start).Milliseconds()
	slog.Info("workspace indexed",
		"workspace_id", workspaceID,
		"indexed", result.Indexed,
		"skipped", result.Skipped,
		"errors", result.Errors,
		"duration_ms", result.DurationMs,
	)
	return result, nil
}

// Remove deletes all index data for a workspace.
func (ix *FTS5Indexer) Remove(workspaceID string) error {
	return ix.store.DeleteWorkspaceIndex(workspaceID)
}

// IndexFile indexes a single file at absPath into the workspace index.
// Used by the live indexer on fsnotify Create/Write events. Safe to call
// repeatedly — upsert semantics mirror Index().
func (ix *FTS5Indexer) IndexFile(ctx context.Context, workspaceID, rootPath, absPath string) error {
	if excludedFiles[filepath.Base(absPath)] {
		return nil
	}

	// Defense in depth: reject symlinks before following them. An account can
	// plant a symlink inside its workspace pointing at /etc/passwd or another
	// account's BaseDir; os.Stat would follow and leak external content into
	// FTS. Lstat + mode check blocks the escape without resolving the target.
	li, err := os.Lstat(absPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("lstat: %w", err)
	}
	if li.Mode()&os.ModeSymlink != 0 {
		return nil
	}

	info, err := os.Stat(absPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("stat: %w", err)
	}
	if info.IsDir() {
		return nil
	}

	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}

	tx, err := ix.store.BeginTx()
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}

	op, _, pErr := ix.processFileTx(tx, workspaceID, rootPath, absPath, info)
	if pErr != nil {
		tx.Rollback()
		return pErr
	}
	if err := tx.Commit(); err != nil {
		tx.Rollback()
		return fmt.Errorf("commit: %w", err)
	}

	// FTS upsert runs post-commit (same reason as bulk Index path).
	if err := ix.store.UpsertWorkspaceFTS(op.id, op.filename, op.body); err != nil {
		return fmt.Errorf("fts upsert: %w", err)
	}
	return nil
}

// RemoveFile deletes the workspace_files row for absPath (or the subtree
// when absPath is a vanished directory) and best-effort purges any
// File.summary cache row for the exact path. Cache GC failures are logged
// but must not fail the primary FTS delete.
func (ix *FTS5Indexer) RemoveFile(workspaceID, absPath string) error {
	if err := ix.store.DeleteWorkspaceFilesByPrefix(workspaceID, absPath); err != nil {
		return err
	}
	keys := computeSummaryKeys(workspaceID, absPath, nil)
	if err := ix.store.DeleteLLMCacheByKeyHash(summaryCacheKind, keys.keyHash); err != nil {
		slog.Warn("summary cache gc failed",
			"workspace_id", workspaceID,
			"abs_path", absPath,
			"err", err,
		)
	}
	return nil
}

// Close is a no-op for FTS5Indexer (the store owns the DB connection).
func (ix *FTS5Indexer) Close() error {
	return nil
}

// ---------------------------------------------------------------------------
// Content policy helpers
// ---------------------------------------------------------------------------

// isBinary checks if a file appears to be binary by looking for null bytes
// in the first 8 KB.
func isBinary(path string) bool {
	f, err := os.Open(path)
	if err != nil {
		return true // assume binary on error
	}
	defer f.Close()

	buf := make([]byte, binaryProbeSize)
	n, err := f.Read(buf)
	if err != nil && err != io.EOF {
		return true
	}
	return bytes.ContainsRune(buf[:n], 0)
}

// readBounded reads up to maxBytes from a file.
func readBounded(path string, maxBytes int64) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return io.ReadAll(io.LimitReader(f, maxBytes))
}

// ---------------------------------------------------------------------------
// Search, Stats, Reindex (stubs — implemented in T3)
// ---------------------------------------------------------------------------

// Search performs a full-text search across indexed workspace files.
func (ix *FTS5Indexer) Search(ctx context.Context, query string, opts SearchOptions) (*SearchResult, error) {
	if query == "" {
		return nil, fmt.Errorf("empty search query")
	}

	limit := opts.Limit
	if limit <= 0 {
		limit = 20
	}
	if limit > 100 {
		limit = 100
	}

	rows, total, err := ix.store.SearchWorkspaceFTSScoped(query, opts.Path, opts.Ext, opts.WorkspaceIDs, limit, opts.Offset)
	if err != nil {
		return nil, err
	}

	files := make([]SearchHit, 0, len(rows))
	for _, r := range rows {
		snippets := extractSnippets(r.AbsPath, query)
		files = append(files, SearchHit{
			Path:     r.AbsPath,
			Score:    r.Score,
			Snippets: snippets,
		})
	}

	return &SearchResult{Files: files, Total: total}, nil
}

// Stats returns aggregate statistics about indexed workspace files.
func (ix *FTS5Indexer) Stats(ctx context.Context, opts StatsOptions) (*IndexStats, error) {
	total, indexed, totalSize, byExtRaw, latestAt, err := ix.store.AggregateWorkspaceFilesScoped(opts.Path, opts.WorkspaceIDs)
	if err != nil {
		return nil, err
	}

	byExt := make(map[string]ExtensionStat, len(byExtRaw))
	for ext, v := range byExtRaw {
		byExt[ext] = ExtensionStat{Count: int(v[0]), Size: v[1]}
	}

	return &IndexStats{
		TotalFiles:   total,
		IndexedFiles: indexed,
		TotalSize:    totalSize,
		ByExtension:  byExt,
		IndexedAt:    latestAt,
	}, nil
}

// Reindex rebuilds the index for a workspace. Uses upsert-based approach:
// walk and upsert all files, then delete entries older than the walk start
// time (stale cleanup). If the walk fails mid-way, existing entries remain.
func (ix *FTS5Indexer) Reindex(ctx context.Context, workspaceID, rootPath string) (*IndexResult, error) {
	// Use SQLite's clock (same source as indexed_at = datetime('now'))
	// to prevent clock-skew between Go time.Now() and SQLite datetime().
	walkStart, err := ix.store.SQLiteNow()
	if err != nil {
		return nil, fmt.Errorf("reindex: get sqlite time: %w", err)
	}

	result, err := ix.Index(ctx, workspaceID, rootPath)
	if err != nil {
		return result, err
	}

	// Clean up files that were not touched during this walk.
	if cleanErr := ix.store.DeleteStaleWorkspaceFiles(workspaceID, walkStart); cleanErr != nil {
		slog.Warn("reindex: stale cleanup failed", "workspace_id", workspaceID, "error", cleanErr)
	}

	return result, nil
}

// extractSnippets reads a file from disk and finds lines matching query terms.
// Returns up to 3 snippets with line numbers. If the file can't be read or
// no matches are found, returns an empty slice.
func extractSnippets(absPath string, query string) []Snippet {
	terms := splitQueryTerms(query)
	if len(terms) == 0 {
		return nil
	}

	data, err := readBounded(absPath, maxIndexFileSize)
	if err != nil {
		return nil
	}

	lines := strings.Split(string(data), "\n")
	var snippets []Snippet
	for i, line := range lines {
		lower := strings.ToLower(line)
		for _, term := range terms {
			if strings.Contains(lower, term) {
				text := strings.TrimSpace(line)
				if len(text) > 200 {
					text = text[:200]
				}
				snippets = append(snippets, Snippet{
					Line: i + 1,
					Text: text,
				})
				break
			}
		}
		if len(snippets) >= 3 {
			break
		}
	}
	return snippets
}

// splitQueryTerms extracts individual search terms from a query string.
// Lowercases all terms for case-insensitive matching.
func splitQueryTerms(query string) []string {
	parts := strings.Fields(query)
	terms := make([]string, 0, len(parts))
	for _, p := range parts {
		// Strip FTS5 operators.
		p = strings.TrimLeft(p, "+-")
		p = strings.Trim(p, "\"")
		if p != "" && p != "AND" && p != "OR" && p != "NOT" && p != "NEAR" {
			terms = append(terms, strings.ToLower(p))
		}
	}
	return terms
}
