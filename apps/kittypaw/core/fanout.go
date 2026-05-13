package core

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
)

// Fanout-specific sentinels — the sandbox surfaces these as JS exceptions
// so skill authors see "fanout: self target" instead of a generic error
// that lumps every failure together.
var (
	ErrFanoutSelfTarget         = errors.New("fanout: cannot send to source account")
	ErrFanoutUnknownAccount     = errors.New("fanout: unknown target account")
	ErrFanoutUnauthorizedTarget = errors.New("fanout: target is not a team-space member")
)

// FanoutPayload is the message body a team-space skill passes to
// Fanout.send/broadcast. The JS binding marshals skill arguments into
// this struct, so additive fields are safe; renaming a tag breaks every
// existing fanout skill.
type FanoutPayload struct {
	// Text is the message body to deliver to the target account. Required.
	Text string `json:"text"`
	// ChannelHint asks the target's AccountRuntime to prefer a specific channel
	// ("telegram", "kakao_talk"). Empty = the runtime picks. Advisory:
	// if the target has no matching channel, delivery falls back to
	// whichever channel the target has available.
	ChannelHint string `json:"channel_hint,omitempty"`
}

// Fanout is the cross-account push abstraction. Only a team-space AccountRuntime gets
// a non-nil implementation; personal accounts cannot reach other personal
// accounts because the sandbox binding is gated on a non-nil field.
type Fanout interface {
	// Send delivers payload to one target account. Returns immediately
	// after the event enqueues — the target runtime runs asynchronously.
	Send(ctx context.Context, accountID string, p FanoutPayload) error
	// Broadcast delivers payload to each configured team-space member.
	// Delivery preserves config order after deduplicating member IDs.
	Broadcast(ctx context.Context, p FanoutPayload) error
}

// ChannelFanout is the default Fanout implementation. It emits
// EventTeamSpacePush events onto the Server's eventCh, and AccountRouter
// dispatches each one to the target AccountRuntime. Decoupling the fanout
// publisher from the runtime map means a future scheduler-driven fanout
// (cron → Fanout) reuses the exact same path.
type ChannelFanout struct {
	eventCh  chan<- Event
	registry *AccountRegistry
	source   string
}

// NewChannelFanout creates a Fanout scoped to a source account. source is
// the ID of the account whose skills will call Send/Broadcast — the
// implementation rejects that ID as a target to prevent self-loops.
//
// Panics if eventCh or registry is nil: a misconfigured Fanout would
// nil-deref on the first Send and crash the shared server, so fail at
// construction instead.
func NewChannelFanout(eventCh chan<- Event, registry *AccountRegistry, source string) *ChannelFanout {
	if eventCh == nil {
		panic("fanout: eventCh is required")
	}
	if registry == nil {
		panic("fanout: registry is required")
	}
	return &ChannelFanout{eventCh: eventCh, registry: registry, source: source}
}

// Send validates the target, marshals the payload, and posts an Event to
// eventCh. If the caller's ctx cancels before the event enqueues (e.g.
// sandbox timeout), Send returns ctx.Err() so the skill unwinds promptly.
func (f *ChannelFanout) Send(ctx context.Context, accountID string, p FanoutPayload) error {
	if err := ValidateAccountID(accountID); err != nil {
		return err
	}
	if accountID == f.source {
		return ErrFanoutSelfTarget
	}
	if f.registry.Get(accountID) == nil {
		return fmt.Errorf("%w: %q", ErrFanoutUnknownAccount, accountID)
	}
	source := f.registry.Get(f.source)
	if source == nil || source.Config == nil || !source.Config.IsTeamSpaceAccount() {
		return ErrFanoutUnauthorizedTarget
	}
	if !source.Config.TeamSpaceHasMember(accountID) {
		return ErrFanoutUnauthorizedTarget
	}

	body, err := json.Marshal(p)
	if err != nil {
		return fmt.Errorf("fanout: marshal payload: %w", err)
	}
	ev := Event{Type: EventTeamSpacePush, AccountID: accountID, Payload: body}

	select {
	case f.eventCh <- ev:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Broadcast sends payload to each configured team-space member. The member list
// is validated before any event is emitted, so a bad config entry fails the
// entire broadcast without partial delivery.
func (f *ChannelFanout) Broadcast(ctx context.Context, p FanoutPayload) error {
	source := f.registry.Get(f.source)
	if source == nil || source.Config == nil || !source.Config.IsTeamSpaceAccount() {
		return ErrFanoutUnauthorizedTarget
	}
	seen := map[string]bool{}
	targets := make([]string, 0, len(source.Config.TeamSpace.Members))
	for _, id := range source.Config.TeamSpace.Members {
		if err := ValidateAccountID(id); err != nil {
			return fmt.Errorf("broadcast to %q: %w", id, err)
		}
		if id == f.source {
			return fmt.Errorf("broadcast to %q: %w", id, ErrFanoutSelfTarget)
		}
		if seen[id] {
			continue
		}
		if f.registry.Get(id) == nil {
			return fmt.Errorf("%w: %q", ErrFanoutUnknownAccount, id)
		}
		if !source.Config.TeamSpaceHasMember(id) {
			return fmt.Errorf("broadcast to %q: %w", id, ErrFanoutUnauthorizedTarget)
		}
		seen[id] = true
		targets = append(targets, id)
	}
	for _, id := range targets {
		if err := f.Send(ctx, id, p); err != nil {
			return fmt.Errorf("broadcast to %q: %w", id, err)
		}
	}
	return nil
}
