package server

import (
	"net/http"
	"os"
	"path/filepath"

	"github.com/go-chi/chi/v5"

	"github.com/jinto/kittypaw/core"
)

// GET /api/v1/staff - list staff with preset status.
func (s *Server) handleStaffList(w http.ResponseWriter, _ *http.Request) {
	base, err := core.ResolveBaseDir(s.defaultSession().BaseDir)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	staff, err := core.ListStaffRecords(base)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	type staffEntry struct {
		ID           string `json:"id"`
		Description  string `json:"description"`
		Active       bool   `json:"active"`
		HasSoul      bool   `json:"has_soul"`
		PresetStatus string `json:"preset_status"`
		PresetID     string `json:"preset_id,omitempty"`
	}

	var entries []staffEntry
	for _, sm := range staff {
		e := staffEntry{
			ID:          sm.ID,
			Description: sm.Description,
			Active:      true,
			HasSoul:     true,
		}
		status := core.StaffPresetStatus(base, sm.ID)
		switch status.Kind {
		case core.StatusPreset:
			e.PresetStatus = "preset"
			e.PresetID = status.PresetID
		case core.StatusCustom:
			e.PresetStatus = "custom"
			e.PresetID = status.PresetID
		default:
			e.PresetStatus = "unknown"
		}
		entries = append(entries, e)
	}
	if entries == nil {
		entries = []staffEntry{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"staff": entries})
}

// POST /api/v1/staff - create new staff.
func (s *Server) handleStaffCreate(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ID          string `json:"id"`
		Description string `json:"description"`
		PresetID    string `json:"preset_id"`
		Nick        string `json:"nick"`
	}
	if !decodeBody(w, r, &req) {
		return
	}
	if err := core.ValidateStaffID(req.ID); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	base, err := core.ResolveBaseDir(s.defaultSession().BaseDir)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if core.StaffHasSoul(base, req.ID) {
		writeError(w, http.StatusConflict, "staff already exists: "+req.ID)
		return
	}

	// Validate preset before writing files.
	if req.PresetID != "" {
		if _, ok := core.Presets[req.PresetID]; !ok {
			writeError(w, http.StatusBadRequest, "unknown preset: "+req.PresetID)
			return
		}
	}

	meta := core.StaffMetaFile{
		ID:          req.ID,
		DisplayName: req.Nick,
		Description: req.Description,
	}
	if err := core.WriteStaffMetaFile(base, meta); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	// Apply preset if specified (already validated above).
	if req.PresetID != "" {
		if err := core.ApplyStaffPreset(base, req.ID, req.PresetID); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
	} else {
		soul := "You are " + req.ID + ", a KittyPaw staff member.\n\n## Role\n" + req.Description + "\n"
		if err := os.WriteFile(filepath.Join(base, "staff", req.ID, "SOUL.md"), []byte(soul), 0o644); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
	}

	writeJSON(w, http.StatusCreated, map[string]any{"success": true, "id": req.ID})
}

// POST /api/v1/staff/{id}/activate - activate or switch to staff.
// Optional JSON body: {"preset_id": "..."} applies a preset before activating.
func (s *Server) handleStaffActivate(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if err := core.ValidateStaffID(id); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	base, err := core.ResolveBaseDir(s.defaultSession().BaseDir)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	// Optional: apply preset if specified in body.
	var body struct {
		PresetID string `json:"preset_id"`
	}
	if r.ContentLength > 0 {
		if !decodeBody(w, r, &body) {
			return
		}
	}
	if body.PresetID != "" {
		if err := core.ApplyStaffPreset(base, id, body.PresetID); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
	} else if !core.StaffHasSoul(base, id) {
		if _, err := os.Stat(filepath.Join(base, "staff", id, "meta.json")); err != nil {
			if os.IsNotExist(err) {
				writeError(w, http.StatusNotFound, "staff not found: "+id)
				return
			}
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeError(w, http.StatusBadRequest, "staff has no SOUL.md: "+id)
		return
	}

	meta, err := core.ReadStaffMetaFile(base, id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if err := core.WriteStaffMetaFile(base, meta); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "id": id})
}
