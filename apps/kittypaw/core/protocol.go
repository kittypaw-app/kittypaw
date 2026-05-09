package core

// WsClientMsg represents messages from the WebSocket client.
// Discriminated by the "type" field.
type WsClientMsg struct {
	Type           string `json:"type"`
	Text           string `json:"text,omitempty"` // for "chat"
	OK             *bool  `json:"ok,omitempty"`   // for "permit"
	ConversationID string `json:"conversation_id,omitempty"`
	// TurnID is a client-allocated UUID identifying a single user input.
	// A retry of the same input must reuse the same TurnID — the server
	// then dedupes via Session.RunTurn so the LLM is not invoked twice
	// and the user is not double-charged. Empty TurnID falls back to the
	// legacy Run path (no idempotency).
	TurnID string `json:"turn_id,omitempty"` // for "chat"
}

// WsServerMsg represents messages from the server to WebSocket clients.
// Discriminated by the "type" field.
type WsServerMsg struct {
	Type        string           `json:"type"`
	ID          string           `json:"id,omitempty"`          // for "session"
	FullText    string           `json:"full_text,omitempty"`   // for "done"
	TokensUsed  *int64           `json:"tokens_used,omitempty"` // for "done"
	Message     string           `json:"message,omitempty"`     // for "error"
	Description string           `json:"description,omitempty"` // for "permission"
	Resource    string           `json:"resource,omitempty"`    // for "permission"
	Image       *ImageAttachment `json:"image,omitempty"`       // for "done"
	// TurnID echoes back the client's WsClientMsg.TurnID on "done" /
	// "error" so a future parallel-turn protocol can route responses;
	// today's serial WS path doesn't strictly need it but the field is
	// load-bearing for retry visibility (the client can verify the
	// response is for the turn it asked).
	TurnID string `json:"turn_id,omitempty"` // for "done"/"error"
}

// WsServerMsg type constants
const (
	WsMsgSession    = "session"
	WsMsgDone       = "done"
	WsMsgError      = "error"
	WsMsgPermission = "permission"
)

// WsClientMsg type constants
const (
	WsMsgChat   = "chat"
	WsMsgPermit = "permit"
)

// NewSessionMsg creates a session initialization message.
func NewSessionMsg(id string) WsServerMsg {
	return WsServerMsg{Type: WsMsgSession, ID: id}
}

// NewDoneMsg creates a completion message.
func NewDoneMsg(fullText string, tokensUsed *int64) WsServerMsg {
	return WsServerMsg{Type: WsMsgDone, FullText: fullText, TokensUsed: tokensUsed}
}

// NewDoneMsgFromOutbound creates a completion message from a rich outbound response.
func NewDoneMsgFromOutbound(out OutboundResponse, tokensUsed *int64) WsServerMsg {
	return WsServerMsg{Type: WsMsgDone, FullText: out.Text, TokensUsed: tokensUsed, Image: out.Image}
}

// NewDoneMsgForTurn creates a completion message that echoes the
// turn_id back to the client.
func NewDoneMsgForTurn(turnID, fullText string, tokensUsed *int64) WsServerMsg {
	return WsServerMsg{Type: WsMsgDone, FullText: fullText, TokensUsed: tokensUsed, TurnID: turnID}
}

// NewDoneMsgForTurnWithOutbound creates a rich completion message tagged with turn_id.
func NewDoneMsgForTurnWithOutbound(turnID string, out OutboundResponse, tokensUsed *int64) WsServerMsg {
	return WsServerMsg{Type: WsMsgDone, FullText: out.Text, TokensUsed: tokensUsed, TurnID: turnID, Image: out.Image}
}

// NewErrorMsg creates an error message.
func NewErrorMsg(message string) WsServerMsg {
	return WsServerMsg{Type: WsMsgError, Message: message}
}

// NewErrorMsgForTurn creates an error message tagged with the turn_id.
func NewErrorMsgForTurn(turnID, message string) WsServerMsg {
	return WsServerMsg{Type: WsMsgError, Message: message, TurnID: turnID}
}

// NewPermissionMsg creates a permission request message.
func NewPermissionMsg(description, resource string) WsServerMsg {
	return WsServerMsg{Type: WsMsgPermission, Description: description, Resource: resource}
}
