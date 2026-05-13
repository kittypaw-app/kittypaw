package core

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// StaffMetaFile is the file-backed metadata stored at staff/<id>/meta.json.
// SOUL.md remains the source of truth for whether the staff can be invoked.
type StaffMetaFile struct {
	ID                      string   `json:"id"`
	DisplayName             string   `json:"display_name,omitempty"`
	Aliases                 []string `json:"aliases,omitempty"`
	Description             string   `json:"description,omitempty"`
	Model                   string   `json:"model,omitempty"`
	AllowedSkills           []string `json:"allowed_skills,omitempty"`
	CreatedFromConversation string   `json:"created_from_conversation,omitempty"`
	CreatedAt               string   `json:"created_at,omitempty"`
	ActivatedAt             string   `json:"activated_at,omitempty"`
	DraftSource             string   `json:"draft_source,omitempty"`
	DraftExpiresAt          string   `json:"draft_expires_at,omitempty"`
}

// StaffRecord is a resolved staff directory with derived file state.
type StaffRecord struct {
	StaffMetaFile
	HasSoul  bool `json:"has_soul"`
	HasDraft bool `json:"has_draft"`
}

func staffRoot(base string) string {
	return filepath.Join(base, "staff")
}

func staffDir(base, id string) string {
	return filepath.Join(staffRoot(base), id)
}

// StaffHasSoul reports whether staff/<id>/SOUL.md exists.
func StaffHasSoul(base, id string) bool {
	if err := ValidateStaffID(id); err != nil {
		return false
	}
	info, err := os.Stat(filepath.Join(staffDir(base, id), "SOUL.md"))
	return err == nil && !info.IsDir()
}

// StaffHasDraft reports whether staff/<id>/SOUL.draft.md exists.
func StaffHasDraft(base, id string) bool {
	if err := ValidateStaffID(id); err != nil {
		return false
	}
	info, err := os.Stat(filepath.Join(staffDir(base, id), "SOUL.draft.md"))
	return err == nil && !info.IsDir()
}

// ReadStaffMetaFile reads staff/<id>/meta.json. Missing files return default metadata.
func ReadStaffMetaFile(base, id string) (StaffMetaFile, error) {
	if err := ValidateStaffID(id); err != nil {
		return StaffMetaFile{}, err
	}
	meta := StaffMetaFile{ID: id}
	data, err := os.ReadFile(filepath.Join(staffDir(base, id), "meta.json"))
	if errors.Is(err, os.ErrNotExist) {
		return meta, nil
	}
	if err != nil {
		return StaffMetaFile{}, err
	}
	if err := json.Unmarshal(data, &meta); err != nil {
		return StaffMetaFile{}, fmt.Errorf("read staff meta %q: %w", id, err)
	}
	if meta.ID == "" {
		meta.ID = id
	}
	if meta.ID != id {
		return StaffMetaFile{}, fmt.Errorf("staff meta id mismatch: path %q contains %q", id, meta.ID)
	}
	return meta, nil
}

// WriteStaffMetaFile writes staff/<id>/meta.json atomically.
func WriteStaffMetaFile(base string, meta StaffMetaFile) error {
	if err := ValidateStaffID(meta.ID); err != nil {
		return err
	}
	if meta.CreatedAt == "" {
		meta.CreatedAt = time.Now().UTC().Format(time.RFC3339)
	}
	dir := staffDir(base, meta.ID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(meta, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return writeFileAtomic(filepath.Join(dir, "meta.json"), data, 0o644)
}

// WriteStaffDraft writes metadata and SOUL.draft.md for an unapproved staff.
func WriteStaffDraft(base string, meta StaffMetaFile, soul string) error {
	if err := WriteStaffMetaFile(base, meta); err != nil {
		return err
	}
	return writeFileAtomic(filepath.Join(staffDir(base, meta.ID), "SOUL.draft.md"), []byte(soul), 0o644)
}

// ActivateStaffDraft promotes SOUL.draft.md to SOUL.md and updates activated_at.
func ActivateStaffDraft(base, id string) error {
	if err := ValidateStaffID(id); err != nil {
		return err
	}
	dir := staffDir(base, id)
	draftPath := filepath.Join(dir, "SOUL.draft.md")
	soulPath := filepath.Join(dir, "SOUL.md")
	if !StaffHasDraft(base, id) {
		return fmt.Errorf("staff %q has no draft SOUL", id)
	}
	if StaffHasSoul(base, id) {
		return fmt.Errorf("staff %q already has SOUL.md", id)
	}
	if err := os.Rename(draftPath, soulPath); err != nil {
		return err
	}
	meta, err := ReadStaffMetaFile(base, id)
	if err != nil {
		return err
	}
	meta.ActivatedAt = time.Now().UTC().Format(time.RFC3339)
	return WriteStaffMetaFile(base, meta)
}

// ListStaffRecords returns active staff only: directories backed by SOUL.md.
func ListStaffRecords(base string) ([]StaffRecord, error) {
	entries, err := os.ReadDir(staffRoot(base))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var records []StaffRecord
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		id := entry.Name()
		if err := ValidateStaffID(id); err != nil {
			continue
		}
		meta, err := ReadStaffMetaFile(base, id)
		if err != nil {
			return nil, err
		}
		record := StaffRecord{
			StaffMetaFile: meta,
			HasSoul:       StaffHasSoul(base, id),
			HasDraft:      StaffHasDraft(base, id),
		}
		if !record.HasSoul {
			continue
		}
		records = append(records, record)
	}
	sort.Slice(records, func(i, j int) bool { return records[i].ID < records[j].ID })
	return records, nil
}

// ListStaffDraftRecords returns staff directories that have a draft SOUL but no active SOUL.
func ListStaffDraftRecords(base string) ([]StaffRecord, error) {
	entries, err := os.ReadDir(staffRoot(base))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var records []StaffRecord
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		id := entry.Name()
		if err := ValidateStaffID(id); err != nil {
			continue
		}
		hasSoul := StaffHasSoul(base, id)
		hasDraft := StaffHasDraft(base, id)
		if !hasDraft || hasSoul {
			continue
		}
		meta, err := ReadStaffMetaFile(base, id)
		if err != nil {
			return nil, err
		}
		records = append(records, StaffRecord{StaffMetaFile: meta, HasSoul: hasSoul, HasDraft: hasDraft})
	}
	sort.Slice(records, func(i, j int) bool { return records[i].ID < records[j].ID })
	return records, nil
}

// ResolveStaffReference resolves a canonical ID or alias to an active SOUL-backed staff ID.
func ResolveStaffReference(base, ref string) (string, bool, error) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return "", false, nil
	}
	if err := ValidateStaffID(ref); err == nil && StaffHasSoul(base, ref) {
		return ref, true, nil
	}
	records, err := ListStaffRecords(base)
	if err != nil {
		return "", false, err
	}
	var matched string
	for _, record := range records {
		if ref == record.ID || ref == record.DisplayName {
			if matched != "" && matched != record.ID {
				return "", false, fmt.Errorf("staff reference %q is ambiguous", ref)
			}
			matched = record.ID
			continue
		}
		for _, alias := range record.Aliases {
			if ref == alias {
				if matched != "" && matched != record.ID {
					return "", false, fmt.Errorf("staff reference %q is ambiguous", ref)
				}
				matched = record.ID
			}
		}
	}
	if matched == "" {
		return "", false, nil
	}
	return matched, true, nil
}

func writeFileAtomic(path string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".tmp-*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmpPath, perm); err != nil {
		return err
	}
	return os.Rename(tmpPath, path)
}
