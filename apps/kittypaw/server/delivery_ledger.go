package server

import (
	"context"
	"log/slog"

	"github.com/jinto/kittypaw/engine"
	"github.com/jinto/kittypaw/store"
)

func (s *Server) deliveryStore(accountID string) *store.Store {
	if s == nil {
		return nil
	}
	if td := s.accountDepsForID(accountID); td != nil && td.Store != nil {
		return td.Store
	}
	return s.store
}

func (s *Server) recordOutboundDelivery(req store.OutboundDeliveryWrite) int64 {
	st := s.deliveryStore(req.AccountID)
	if st == nil {
		return 0
	}
	id, err := st.CreateOutboundDelivery(req)
	if err != nil {
		slog.Warn("delivery ledger: create failed",
			"account", req.AccountID, "channel", req.EventType, "source", req.Source, "error", err)
		return 0
	}
	return id
}

func outboundDeliveryWithOrigin(ctx context.Context, req store.OutboundDeliveryWrite) store.OutboundDeliveryWrite {
	origin, ok := engine.DeliveryOriginFromContext(ctx)
	if !ok {
		return req
	}
	if req.OriginType == "" {
		req.OriginType = origin.Type
	}
	if req.OriginID == "" {
		req.OriginID = origin.ID
	}
	if req.OriginName == "" {
		req.OriginName = origin.Name
	}
	if req.ScheduledRunID <= 0 {
		req.ScheduledRunID = origin.ScheduledRunID
	}
	return req
}

func (s *Server) markOutboundDelivery(id int64, accountID string, status store.OutboundDeliveryStatus, errorClass, errorMessage string) {
	if id <= 0 {
		return
	}
	st := s.deliveryStore(accountID)
	if st == nil {
		return
	}
	if err := st.UpdateOutboundDeliveryStatus(id, status, errorClass, errorMessage); err != nil {
		slog.Warn("delivery ledger: update failed",
			"id", id, "account", accountID, "status", status, "error", err)
	}
}

func (s *Server) markPendingOutboundDelivery(accountID string, pendingResponseID int64, status store.OutboundDeliveryStatus, retryCount int, errorClass, errorMessage string) {
	if pendingResponseID <= 0 {
		return
	}
	st := s.deliveryStore(accountID)
	if st == nil {
		return
	}
	if err := st.UpdateOutboundDeliveryForPendingResponse(pendingResponseID, status, retryCount, errorClass, errorMessage); err != nil {
		slog.Warn("delivery ledger: pending update failed",
			"pending_response_id", pendingResponseID, "account", accountID, "status", status, "error", err)
	}
}
