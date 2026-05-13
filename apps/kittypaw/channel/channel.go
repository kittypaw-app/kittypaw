// Package channel provides messaging channel backends for kittypaw.
//
// Each channel is an event producer: it listens for inbound messages
// from a specific platform (Telegram, Slack, Discord, etc.) and emits
// them as core.Event values. Channels also handle sending responses
// back to the originating platform.
package channel

import (
	"context"

	"github.com/jinto/kittypaw/core"
)

// Channel is the interface for all messaging channel backends.
// Channels are event producers that emit Events, and can send responses back.
type Channel interface {
	// Start begins listening for messages. Received messages are sent to eventCh.
	// Blocks until ctx is canceled.
	Start(ctx context.Context, eventCh chan<- core.Event) error

	// SendResponse sends a text response back to the channel.
	// chatID identifies the destination (e.g., Telegram chat ID, Slack channel ID).
	// replyToMessageID is optional — when non-empty, the channel quotes the
	// original message (Telegram: reply_to_message_id). Empty string = plain send.
	SendResponse(ctx context.Context, chatID, response, replyToMessageID string) error

	// Name returns the channel identifier (e.g., "telegram", "slack").
	Name() string
}

// RichResponder is an optional capability for channels that can render
// structured response metadata such as image attachments.
type RichResponder interface {
	SendRichResponse(ctx context.Context, chatID string, response core.OutboundResponse, replyToMessageID string) error
}

// ResponseLimiter is an optional capability for channels with platform text
// length limits. Server dispatchers use it to split long outbound text before
// calling SendResponse.
type ResponseLimiter interface {
	MaxResponseLength() int
}

// Confirmer is an optional capability for channels that support interactive
// permission dialogs. Channels implement this to enable approval prompts
// for destructive operations (e.g., shell commands, git push).
//
// Use a type assertion to check at runtime:
//
//	if confirmer, ok := ch.(channel.Confirmer); ok { ... }
type Confirmer interface {
	AskConfirmation(ctx context.Context, chatID, description, resource string) (bool, error)
}

// RequesterConfirmer is an optional stricter confirmation capability for group
// chats. requesterID is the inbound ChatPayload.SourceSessionID, usually the platform
// user ID, and callbacks from other users must not resolve the prompt.
type RequesterConfirmer interface {
	AskConfirmationForRequester(ctx context.Context, chatID, requesterID, description, resource string) (bool, error)
}
