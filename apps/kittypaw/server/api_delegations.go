package server

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/jinto/kittypaw/store"
)

func (s *Server) handleDelegationsList(w http.ResponseWriter, r *http.Request) {
	acct, err := s.requestAccount(r)
	if err != nil {
		writeError(w, http.StatusUnauthorized, err.Error())
		return
	}
	limit := memoryLimitFromRequest(r, 50, 500)
	rows, err := acct.Deps.Store.ListDelegationJobs(store.DelegationJobListFilter{
		AccountID:            acct.ID,
		Status:               strings.TrimSpace(r.URL.Query().Get("status")),
		ParentConversationID: strings.TrimSpace(r.URL.Query().Get("conversation_id")),
		Limit:                limit,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if rows == nil {
		rows = []store.DelegationJob{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"delegations": rows})
}

func (s *Server) handleDelegationGet(w http.ResponseWriter, r *http.Request) {
	acct, err := s.requestAccount(r)
	if err != nil {
		writeError(w, http.StatusUnauthorized, err.Error())
		return
	}
	jobID := strings.TrimSpace(chi.URLParam(r, "id"))
	job, ok, err := acct.Deps.Store.GetDelegationJob(jobID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if !ok || job.AccountID != acct.ID {
		writeError(w, http.StatusNotFound, "delegation job not found")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"delegation": job})
}

func (s *Server) handleDelegationCancel(w http.ResponseWriter, r *http.Request) {
	acct, err := s.requestAccount(r)
	if err != nil {
		writeError(w, http.StatusUnauthorized, err.Error())
		return
	}
	jobID := strings.TrimSpace(chi.URLParam(r, "id"))
	reason := strings.TrimSpace(r.URL.Query().Get("reason"))
	if r.Body != nil && r.ContentLength != 0 {
		var body struct {
			Reason string `json:"reason"`
		}
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16*1024)).Decode(&body); err != nil {
			writeError(w, http.StatusBadRequest, "invalid request body")
			return
		}
		if strings.TrimSpace(body.Reason) != "" {
			reason = strings.TrimSpace(body.Reason)
		}
	}
	if reason == "" {
		reason = "canceled by API"
	}

	var job *store.DelegationJob
	if acct.Runtime != nil && acct.Runtime.DelegationJobs != nil {
		job, err = acct.Runtime.DelegationJobs.CancelJob(r.Context(), jobID, acct.ID, reason)
	} else {
		job, err = acct.Deps.Store.CancelDelegationJobForAccount(acct.ID, jobID, acct.ID, reason)
	}
	if errors.Is(err, sql.ErrNoRows) {
		writeError(w, http.StatusNotFound, "delegation job not found")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "delegation": job})
}
