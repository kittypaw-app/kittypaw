package channel

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"mime/multipart"
	"net/http"
	"os"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/jinto/kittypaw/core"
)

const (
	telegramAPI      = "https://api.telegram.org/bot"
	telegramFileAPI  = "https://api.telegram.org/file/bot"
	telegramMaxChunk = 4096
	telegramPollSecs = 30
	whisperAPI       = "https://api.openai.com/v1/audio/transcriptions"
	maxBackoff       = 60 * time.Second
	initialBackoff   = 1 * time.Second
)

// isDuplicateBotError checks if the Telegram error indicates another bot instance is running.
func isDuplicateBotError(err error) bool {
	return err != nil && strings.Contains(err.Error(), "terminated by other getUpdates request")
}

// duplicateBotMessage returns a user-friendly message based on system locale.
func duplicateBotMessage() string {
	lang := os.Getenv("LANG")
	if lang == "" {
		lang = os.Getenv("LC_ALL")
	}
	lang = strings.ToLower(lang)

	switch {
	case strings.HasPrefix(lang, "ko"):
		return "\n  ⚠ 같은 봇 토큰으로 다른 인스턴스가 실행 중입니다.\n" +
			"    기존 프로세스를 종료한 뒤 다시 실행하세요.\n\n" +
			"    pkill -f kittypaw\n    kittypaw server start\n"
	case strings.HasPrefix(lang, "ja"):
		return "\n  ⚠ 同じボットトークンで別のインスタンスが実行中です。\n" +
			"    既存のプロセスを終了してから再実行してください。\n\n" +
			"    pkill -f kittypaw\n    kittypaw server start\n"
	default:
		return "\n  ⚠ Another instance is already running with the same bot token.\n" +
			"    Stop the existing process and try again.\n\n" +
			"    pkill -f kittypaw\n    kittypaw server start\n"
	}
}

// --- Telegram API DTOs ---

// telegramResponse wraps all Telegram Bot API responses.
type telegramResponse[T any] struct {
	OK          bool   `json:"ok"`
	Result      *T     `json:"result,omitempty"`
	Description string `json:"description,omitempty"`
}

// telegramUpdate is a single update from getUpdates.
type telegramUpdate struct {
	UpdateID      int64                  `json:"update_id"`
	Message       *telegramMessage       `json:"message,omitempty"`
	CallbackQuery *telegramCallbackQuery `json:"callback_query,omitempty"`
}

// telegramCallbackQuery is the callback from an inline keyboard button press.
type telegramCallbackQuery struct {
	ID   string        `json:"id"`
	From *telegramUser `json:"from,omitempty"`
	Data string        `json:"data"`
}

// telegramInlineKeyboardButton is a single button in an inline keyboard.
type telegramInlineKeyboardButton struct {
	Text         string `json:"text"`
	CallbackData string `json:"callback_data"`
}

// telegramInlineKeyboardMarkup is the reply_markup for inline keyboards.
type telegramInlineKeyboardMarkup struct {
	InlineKeyboard [][]telegramInlineKeyboardButton `json:"inline_keyboard"`
}

// telegramMessage is the message object inside an update.
type telegramMessage struct {
	MessageID int64               `json:"message_id"`
	Chat      telegramChat        `json:"chat"`
	Text      string              `json:"text"`
	Caption   string              `json:"caption,omitempty"`
	From      *telegramUser       `json:"from,omitempty"`
	Voice     *telegramVoice      `json:"voice,omitempty"`
	Photo     []telegramPhotoSize `json:"photo,omitempty"`
	Document  *telegramDocument   `json:"document,omitempty"`
}

// telegramChat identifies a Telegram chat.
type telegramChat struct {
	ID int64 `json:"id"`
}

// telegramUser represents the sender of a message.
type telegramUser struct {
	ID        int64  `json:"id"`
	FirstName string `json:"first_name"`
	LastName  string `json:"last_name"`
	Username  string `json:"username"`
}

// displayName returns a human-readable name for the user.
func (u *telegramUser) displayName() string {
	if u == nil {
		return "unknown"
	}
	if u.FirstName != "" && u.LastName != "" {
		return u.FirstName + " " + u.LastName
	}
	if u.FirstName != "" {
		return u.FirstName
	}
	if u.Username != "" {
		return u.Username
	}
	return "unknown"
}

// telegramVoice holds voice message metadata.
type telegramVoice struct {
	FileID string `json:"file_id"`
}

type telegramPhotoSize struct {
	FileID       string `json:"file_id"`
	FileUniqueID string `json:"file_unique_id,omitempty"`
	FileSize     int64  `json:"file_size,omitempty"`
	Width        int    `json:"width"`
	Height       int    `json:"height"`
}

type telegramDocument struct {
	FileID       string `json:"file_id"`
	FileUniqueID string `json:"file_unique_id,omitempty"`
	FileName     string `json:"file_name,omitempty"`
	MimeType     string `json:"mime_type,omitempty"`
	FileSize     int64  `json:"file_size,omitempty"`
}

// telegramFile is the response from getFile.
type telegramFile struct {
	FilePath string `json:"file_path"`
}

// --- TelegramChannel ---

type telegramPendingConfirmation struct {
	decision    chan bool
	requesterID string
}

// TelegramChannel implements Channel and Confirmer using the Telegram Bot API.
// It uses long polling via getUpdates and raw HTTP (no SDK).
type TelegramChannel struct {
	accountID string
	botToken  string
	apiBase   string
	client    *http.Client
	chatID    int64 // last chat_id for responses
	offset    int64 // next update_id to request
	mu        sync.Mutex
	pending   sync.Map // requestID -> telegramPendingConfirmation

	// typing indicator state — typingCancels[chatID] cancels the in-flight
	// typingLoop for that chat. Started at message-receive (Start loop) and
	// canceled at SendResponse so the "..." dots stay visible across the
	// entire runner-loop window, not just the post-LLM send window.
	typingMu      sync.Mutex
	typingCancels map[int64]context.CancelFunc
}

// NewTelegram creates a TelegramChannel with the given bot token, tagging
// every emitted Event with accountID so the AccountRouter can dispatch.
func NewTelegram(accountID, botToken string) *TelegramChannel {
	return &TelegramChannel{
		accountID: accountID,
		botToken:  botToken,
		apiBase:   telegramAPI,
		client: &http.Client{
			Timeout: time.Duration(telegramPollSecs+10) * time.Second,
		},
		typingCancels: make(map[int64]context.CancelFunc),
	}
}

func (t *TelegramChannel) Name() string { return "telegram" }

func (t *TelegramChannel) MaxResponseLength() int { return telegramMaxChunk }

// LastChatID reports the most recent Telegram chat_id observed by this
// channel. Setup/account pairing uses this when the daemon is already polling
// the bot token, avoiding a second competing getUpdates consumer.
func (t *TelegramChannel) LastChatID() (string, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.chatID == 0 {
		return "", false
	}
	return strconv.FormatInt(t.chatID, 10), true
}

// Start long-polls the Telegram Bot API for updates. It blocks until
// ctx is canceled, emitting core.Event values on eventCh.
func (t *TelegramChannel) Start(ctx context.Context, eventCh chan<- core.Event) error {
	slog.Info("telegram: starting long-poll loop")
	backoff := initialBackoff

	for {
		select {
		case <-ctx.Done():
			slog.Info("telegram: shutting down")
			return ctx.Err()
		default:
		}

		updates, err := t.getUpdates(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			if isDuplicateBotError(err) {
				fmt.Fprint(os.Stderr, duplicateBotMessage())
				return fmt.Errorf("telegram: duplicate bot instance")
			}
			slog.Warn("telegram: getUpdates failed, backing off",
				"error", err, "backoff", backoff)
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(backoff):
			}
			backoff = min(backoff*2, maxBackoff)
			continue
		}
		backoff = initialBackoff

		for _, upd := range updates {
			t.mu.Lock()
			if upd.UpdateID >= t.offset {
				t.offset = upd.UpdateID + 1
			}
			t.mu.Unlock()

			// Route callback queries to the pending permission map
			// (bypasses eventCh to prevent deadlock with dispatchLoop).
			if upd.CallbackQuery != nil {
				t.resolveCallback(ctx, upd.CallbackQuery)
				continue
			}

			if upd.Message == nil {
				continue
			}

			msg := upd.Message
			t.mu.Lock()
			t.chatID = msg.Chat.ID
			t.mu.Unlock()

			text := msg.Text
			if text == "" {
				text = msg.Caption
			}
			attachments := t.telegramAttachments(ctx, msg, text)

			// Voice message: download and transcribe via Whisper.
			if msg.Voice != nil && text == "" && len(attachments) == 0 {
				transcribed, err := t.transcribeVoice(ctx, msg.Voice.FileID)
				if err != nil {
					slog.Warn("telegram: voice transcription failed", "error", err)
					continue
				}
				text = transcribed
			}

			if text == "" && len(attachments) == 0 {
				continue
			}

			event, chatID, ok := telegramMessageEvent(t.accountID, msg, text, attachments)
			if !ok {
				continue
			}

			select {
			case eventCh <- event:
				// Start typing as early as possible — before the runner loop
				// even begins. SendResponse will cancel this when it's time
				// to deliver the actual reply.
				t.startTyping(ctx, chatID)
			case <-ctx.Done():
				return ctx.Err()
			}
		}
	}
}

func telegramMessageEvent(accountID string, msg *telegramMessage, text string, attachmentGroups ...[]core.ChatAttachment) (core.Event, int64, bool) {
	if msg == nil {
		return core.Event{}, 0, false
	}
	var attachments []core.ChatAttachment
	if len(attachmentGroups) > 0 {
		attachments = attachmentGroups[0]
	}
	if text == "" && len(attachments) == 0 {
		return core.Event{}, 0, false
	}
	chatIDStr := strconv.FormatInt(msg.Chat.ID, 10)

	fromName := ""
	sessionID := chatIDStr
	if msg.From != nil {
		fromName = msg.From.displayName()
		if msg.From.ID != 0 {
			sessionID = strconv.FormatInt(msg.From.ID, 10)
		}
	}

	payload := core.ChatPayload{
		ChatID:           chatIDStr,
		Text:             text,
		FromName:         fromName,
		SessionID:        sessionID,
		Attachments:      attachments,
		ReplyToMessageID: strconv.FormatInt(msg.MessageID, 10),
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		slog.Error("telegram: marshal payload", "error", err)
		return core.Event{}, 0, false
	}

	return core.Event{
		Type:      core.EventTelegram,
		AccountID: accountID,
		Payload:   raw,
	}, msg.Chat.ID, true
}

func (t *TelegramChannel) telegramAttachments(ctx context.Context, msg *telegramMessage, caption string) []core.ChatAttachment {
	if msg == nil {
		return nil
	}
	var attachments []core.ChatAttachment
	if len(msg.Photo) > 0 {
		photo := largestTelegramPhoto(msg.Photo)
		if photo.FileID != "" {
			filePath, err := t.getFilePath(ctx, photo.FileID)
			if err != nil {
				slog.Warn("telegram: getFile failed for photo", "error", err)
			} else {
				attachments = append(attachments, core.ChatAttachment{
					ID:        fmt.Sprintf("tg_%d_%d", msg.MessageID, len(attachments)),
					Type:      "image",
					Source:    "telegram",
					URL:       telegramFileURL(t.botToken, filePath),
					SizeBytes: photo.FileSize,
					Width:     photo.Width,
					Height:    photo.Height,
					Caption:   caption,
				})
			}
		}
	}
	if msg.Document != nil && msg.Document.FileID != "" {
		filePath, err := t.getFilePath(ctx, msg.Document.FileID)
		if err != nil {
			slog.Warn("telegram: getFile failed for document", "error", err)
		} else {
			attachments = append(attachments, core.ChatAttachment{
				ID:        fmt.Sprintf("tg_%d_%d", msg.MessageID, len(attachments)),
				Type:      telegramDocumentAttachmentType(msg.Document),
				Source:    "telegram",
				URL:       telegramFileURL(t.botToken, filePath),
				MimeType:  msg.Document.MimeType,
				FileName:  msg.Document.FileName,
				SizeBytes: msg.Document.FileSize,
				Caption:   caption,
			})
		}
	}
	return attachments
}

func largestTelegramPhoto(photos []telegramPhotoSize) telegramPhotoSize {
	best := photos[0]
	bestScore := telegramPhotoScore(best)
	for _, photo := range photos[1:] {
		score := telegramPhotoScore(photo)
		if score > bestScore {
			best = photo
			bestScore = score
		}
	}
	return best
}

func telegramPhotoScore(photo telegramPhotoSize) int64 {
	if photo.FileSize > 0 {
		return photo.FileSize
	}
	return int64(photo.Width) * int64(photo.Height)
}

func isTelegramImageDocument(doc *telegramDocument) bool {
	if doc == nil {
		return false
	}
	mimeType := strings.ToLower(doc.MimeType)
	if strings.HasPrefix(mimeType, "image/") {
		return true
	}
	name := strings.ToLower(doc.FileName)
	for _, suffix := range []string{".jpg", ".jpeg", ".png", ".gif", ".webp", ".bmp"} {
		if strings.HasSuffix(name, suffix) {
			return true
		}
	}
	return false
}

func telegramDocumentAttachmentType(doc *telegramDocument) string {
	if isTelegramImageDocument(doc) {
		return "image"
	}
	return "file"
}

func telegramFileURL(botToken, filePath string) string {
	return telegramFileAPI + botToken + "/" + filePath
}

// startTyping spawns (or replaces) a typingLoop for chatID. Subsequent calls
// for the same chat cancel the prior loop first so a quick second message
// doesn't end up with two tickers double-pinging the API.
func (t *TelegramChannel) startTyping(parent context.Context, chatID int64) {
	typingCtx, cancel := context.WithCancel(parent)

	t.typingMu.Lock()
	if old, ok := t.typingCancels[chatID]; ok {
		old()
	}
	t.typingCancels[chatID] = cancel
	t.typingMu.Unlock()

	go func() {
		t.typingLoop(typingCtx, chatID)
		t.typingMu.Lock()
		// Only delete the map entry if we still own it — otherwise a
		// concurrent startTyping has already replaced us.
		if cur, ok := t.typingCancels[chatID]; ok && reflect.ValueOf(cur).Pointer() == reflect.ValueOf(cancel).Pointer() {
			delete(t.typingCancels, chatID)
		}
		t.typingMu.Unlock()
	}()
}

// stopTyping cancels any active typingLoop for chatID. Safe to call even when
// no loop is active.
func (t *TelegramChannel) stopTyping(chatID int64) {
	t.typingMu.Lock()
	if cancel, ok := t.typingCancels[chatID]; ok {
		cancel()
		delete(t.typingCancels, chatID)
	}
	t.typingMu.Unlock()
}

// telegramTypingRefresh is the cadence for resending the typing indicator.
// Telegram's chat action expires ~5s after each call, so we refresh slightly
// faster to keep the "..." dots visible during long runner loops.
const telegramTypingRefresh = 4 * time.Second

// SendResponse sends a text response to a Telegram chat.
// The chatIDStr parameter is the numeric chat ID as a string (from ChatPayload.ChatID).
// Falls back to the most recently cached chat ID if parsing fails.
// Long messages are split into chunks of telegramMaxChunk characters.
// replyToMessageID, when non-empty, makes each chunk reply-quote the original message.
func (t *TelegramChannel) SendResponse(ctx context.Context, chatIDStr, response, replyToMessageID string) error {
	chatID, replyToID, err := t.resolveSendTarget(chatIDStr, replyToMessageID)
	if err != nil {
		return err
	}

	// Stop any event-time typingLoop now that we have a real reply ready —
	// otherwise the dots would briefly linger after the message arrives.
	// startTyping at message-receive (Start loop) covers the runner-loop window;
	// this stopTyping closes that window cleanly.
	t.stopTyping(chatID)

	// Split into chunks.
	chunks := core.SplitChunks(response, telegramMaxChunk)
	for _, chunk := range chunks {
		if err := t.sendMessage(ctx, chatID, chunk, replyToID); err != nil {
			return fmt.Errorf("telegram: sendMessage: %w", err)
		}
	}
	return nil
}

// SendRichResponse sends an image response as a Telegram photo when possible.
func (t *TelegramChannel) SendRichResponse(ctx context.Context, chatIDStr string, response core.OutboundResponse, replyToMessageID string) error {
	if response.Image == nil || response.Image.URL == "" {
		return t.SendResponse(ctx, chatIDStr, response.Text, replyToMessageID)
	}

	chatID, replyToID, err := t.resolveSendTarget(chatIDStr, replyToMessageID)
	if err != nil {
		return err
	}
	t.stopTyping(chatID)

	caption := telegramCaption(response)
	if strings.HasPrefix(response.Image.URL, "data:image/") {
		return t.sendPhotoDataURI(ctx, chatID, response.Image.URL, caption, replyToID)
	}
	return t.sendPhotoURL(ctx, chatID, response.Image.URL, caption, replyToID)
}

// typingLoop sends the typing chat action immediately, then refreshes it every
// telegramTypingRefresh until ctx is canceled.
func (t *TelegramChannel) typingLoop(ctx context.Context, chatID int64) {
	_ = t.sendChatAction(ctx, chatID, "typing")
	ticker := time.NewTicker(telegramTypingRefresh)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			_ = t.sendChatAction(ctx, chatID, "typing")
		}
	}
}

// --- internal helpers ---

func (t *TelegramChannel) apiURL(method string) string {
	return t.apiBase + t.botToken + "/" + method
}

func (t *TelegramChannel) resolveSendTarget(chatIDStr, replyToMessageID string) (chatID, replyToID int64, err error) {
	chatID, err = strconv.ParseInt(chatIDStr, 10, 64)
	if err != nil {
		// Fall back to cached chat ID.
		t.mu.Lock()
		chatID = t.chatID
		t.mu.Unlock()
	}

	if chatID == 0 {
		return 0, 0, fmt.Errorf("telegram: no chat_id to respond to")
	}

	if replyToMessageID != "" {
		if id, parseErr := strconv.ParseInt(replyToMessageID, 10, 64); parseErr == nil {
			replyToID = id
		}
	}
	return chatID, replyToID, nil
}

func (t *TelegramChannel) getUpdates(ctx context.Context) ([]telegramUpdate, error) {
	t.mu.Lock()
	offset := t.offset
	t.mu.Unlock()

	body := map[string]any{
		"offset":  offset,
		"timeout": telegramPollSecs,
	}
	data, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		t.apiURL("getUpdates"), bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := t.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var result telegramResponse[[]telegramUpdate]
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decode getUpdates: %w", err)
	}
	if !result.OK {
		return nil, fmt.Errorf("getUpdates: %s", result.Description)
	}
	if result.Result == nil {
		return nil, nil
	}
	return *result.Result, nil
}

func (t *TelegramChannel) sendMessage(ctx context.Context, chatID int64, text string, replyToID int64) error {
	body := map[string]any{
		"chat_id": chatID,
		"text":    text,
	}
	if replyToID != 0 {
		body["reply_to_message_id"] = replyToID
	}
	data, err := json.Marshal(body)
	if err != nil {
		return err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		t.apiURL("sendMessage"), bytes.NewReader(data))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := t.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	return checkTelegramResponse(resp, "sendMessage")
}

func (t *TelegramChannel) sendPhotoURL(ctx context.Context, chatID int64, photoURL, caption string, replyToID int64) error {
	body := map[string]any{
		"chat_id": chatID,
		"photo":   photoURL,
	}
	if caption != "" {
		body["caption"] = caption
	}
	if replyToID != 0 {
		body["reply_to_message_id"] = replyToID
	}
	data, err := json.Marshal(body)
	if err != nil {
		return err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		t.apiURL("sendPhoto"), bytes.NewReader(data))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := t.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	return checkTelegramResponse(resp, "sendPhoto")
}

func (t *TelegramChannel) sendPhotoDataURI(ctx context.Context, chatID int64, dataURI, caption string, replyToID int64) error {
	mimeType, data, err := parseImageDataURI(dataURI)
	if err != nil {
		return err
	}

	var buf bytes.Buffer
	writer := multipart.NewWriter(&buf)
	if err := writer.WriteField("chat_id", strconv.FormatInt(chatID, 10)); err != nil {
		return err
	}
	if caption != "" {
		if err := writer.WriteField("caption", caption); err != nil {
			return err
		}
	}
	if replyToID != 0 {
		if err := writer.WriteField("reply_to_message_id", strconv.FormatInt(replyToID, 10)); err != nil {
			return err
		}
	}
	part, err := writer.CreateFormFile("photo", filenameForImageMIME(mimeType))
	if err != nil {
		return err
	}
	if _, err := part.Write(data); err != nil {
		return err
	}
	if err := writer.Close(); err != nil {
		return err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, t.apiURL("sendPhoto"), &buf)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", writer.FormDataContentType())

	resp, err := t.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	return checkTelegramResponse(resp, "sendPhoto")
}

func parseImageDataURI(dataURI string) (mimeType string, data []byte, err error) {
	header, payload, ok := strings.Cut(dataURI, ",")
	if !ok || !strings.HasPrefix(header, "data:image/") || !strings.HasSuffix(header, ";base64") {
		return "", nil, fmt.Errorf("invalid image data URI")
	}
	data, err = base64.StdEncoding.DecodeString(payload)
	if err != nil {
		return "", nil, fmt.Errorf("decode image data URI: %w", err)
	}
	return strings.TrimPrefix(strings.TrimSuffix(header, ";base64"), "data:"), data, nil
}

func filenameForImageMIME(mimeType string) string {
	switch mimeType {
	case "image/jpeg":
		return "image.jpg"
	case "image/gif":
		return "image.gif"
	case "image/webp":
		return "image.webp"
	default:
		return "image.png"
	}
}

func telegramCaption(response core.OutboundResponse) string {
	if response.Image == nil {
		return truncateTelegramCaption(response.Text)
	}
	if response.Image.Caption != "" {
		return truncateTelegramCaption(response.Image.Caption)
	}
	if response.Image.Alt != "" {
		return truncateTelegramCaption(response.Image.Alt)
	}
	return ""
}

func truncateTelegramCaption(text string) string {
	const maxCaptionRunes = 1024
	runes := []rune(strings.TrimSpace(text))
	if len(runes) <= maxCaptionRunes {
		return string(runes)
	}
	return string(runes[:maxCaptionRunes])
}

func checkTelegramResponse(resp *http.Response, method string) error {
	var result telegramResponse[json.RawMessage]
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return fmt.Errorf("decode %s: %w", method, err)
	}
	if !result.OK {
		return fmt.Errorf("%s: %s", method, result.Description)
	}
	return nil
}

func (t *TelegramChannel) sendChatAction(ctx context.Context, chatID int64, action string) error {
	body := map[string]any{
		"chat_id": chatID,
		"action":  action,
	}
	data, err := json.Marshal(body)
	if err != nil {
		return err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		t.apiURL("sendChatAction"), bytes.NewReader(data))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := t.client.Do(req)
	if err != nil {
		return err
	}
	resp.Body.Close()
	return nil
}

// transcribeVoice downloads a Telegram voice file and sends it to
// the OpenAI Whisper API for speech-to-text transcription.
func (t *TelegramChannel) transcribeVoice(ctx context.Context, fileID string) (string, error) {
	// Step 1: get the file path from Telegram.
	filePath, err := t.getFilePath(ctx, fileID)
	if err != nil {
		return "", fmt.Errorf("getFile: %w", err)
	}

	// Step 2: download the file bytes.
	fileURL := telegramFileURL(t.botToken, filePath)
	fileReq, err := http.NewRequestWithContext(ctx, http.MethodGet, fileURL, nil)
	if err != nil {
		return "", err
	}
	fileResp, err := t.client.Do(fileReq)
	if err != nil {
		return "", fmt.Errorf("download voice file: %w", err)
	}
	defer fileResp.Body.Close()

	audioData, err := io.ReadAll(fileResp.Body)
	if err != nil {
		return "", fmt.Errorf("read voice file: %w", err)
	}

	// Step 3: send to Whisper API.
	apiKey := os.Getenv("OPENAI_API_KEY")
	if apiKey == "" {
		return "", fmt.Errorf("OPENAI_API_KEY not set for voice transcription")
	}

	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	part, err := w.CreateFormFile("file", "voice.ogg")
	if err != nil {
		return "", err
	}
	if _, err := part.Write(audioData); err != nil {
		return "", err
	}
	_ = w.WriteField("model", "whisper-1")
	_ = w.WriteField("language", "ko")
	_ = w.Close()

	whisperReq, err := http.NewRequestWithContext(ctx, http.MethodPost, whisperAPI, &buf)
	if err != nil {
		return "", err
	}
	whisperReq.Header.Set("Authorization", "Bearer "+apiKey)
	whisperReq.Header.Set("Content-Type", w.FormDataContentType())

	whisperResp, err := t.client.Do(whisperReq)
	if err != nil {
		return "", fmt.Errorf("whisper request: %w", err)
	}
	defer whisperResp.Body.Close()

	var whisperResult struct {
		Text string `json:"text"`
	}
	if err := json.NewDecoder(whisperResp.Body).Decode(&whisperResult); err != nil {
		return "", fmt.Errorf("decode whisper response: %w", err)
	}

	slog.Info("telegram: transcribed voice message",
		"length", len(audioData), "text_length", len(whisperResult.Text))
	return whisperResult.Text, nil
}

func (t *TelegramChannel) getFilePath(ctx context.Context, fileID string) (string, error) {
	body := map[string]any{"file_id": fileID}
	data, err := json.Marshal(body)
	if err != nil {
		return "", err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		t.apiURL("getFile"), bytes.NewReader(data))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := t.client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	var result telegramResponse[telegramFile]
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", fmt.Errorf("decode getFile: %w", err)
	}
	if !result.OK || result.Result == nil {
		return "", fmt.Errorf("getFile: %s", result.Description)
	}
	return result.Result.FilePath, nil
}

// --- Confirmer implementation ---

// AskConfirmation sends an inline keyboard with approve/deny buttons and blocks
// until the user clicks one or ctx expires. The timeout is controlled by the
// caller via context.WithTimeout — this method only listens to ctx.Done().
func (t *TelegramChannel) AskConfirmation(ctx context.Context, chatID, description, resource string) (bool, error) {
	return t.AskConfirmationForRequester(ctx, chatID, "", description, resource)
}

func (t *TelegramChannel) AskConfirmationForRequester(ctx context.Context, chatID, requesterID, description, resource string) (bool, error) {
	reqID := uuid.New().String()
	ch := make(chan bool, 1)
	t.pending.Store(reqID, telegramPendingConfirmation{
		decision:    ch,
		requesterID: strings.TrimSpace(requesterID),
	})
	defer t.pending.Delete(reqID)

	chatIDInt, err := strconv.ParseInt(chatID, 10, 64)
	if err != nil {
		return false, fmt.Errorf("telegram: invalid chatID %q: %w", chatID, err)
	}

	if err := t.sendInlineKeyboard(ctx, chatIDInt, description, reqID); err != nil {
		return false, fmt.Errorf("telegram: send permission keyboard: %w", err)
	}

	select {
	case ok := <-ch:
		return ok, nil
	case <-ctx.Done():
		return false, ctx.Err()
	}
}

// resolveCallback handles a callback_query by looking up the requestID
// in the pending map and sending the approval/denial to the waiting goroutine.
func (t *TelegramChannel) resolveCallback(ctx context.Context, query *telegramCallbackQuery) {
	// Always acknowledge the callback to remove the loading spinner.
	t.answerCallbackQuery(ctx, query.ID)

	// Parse callback_data: "a:{reqID}" or "d:{reqID}"
	data := query.Data
	if len(data) < 3 || data[1] != ':' {
		slog.Debug("telegram: ignoring callback with unexpected format", "data", data)
		return
	}

	prefix := data[0]
	reqID := data[2:]

	val, ok := t.pending.Load(reqID)
	if !ok {
		// Stale or duplicate callback — the request already resolved or timed out.
		slog.Debug("telegram: no pending permission for callback", "req_id", reqID)
		return
	}
	pending, ok := telegramPendingFromValue(val)
	if !ok {
		slog.Warn("telegram: invalid pending permission entry", "req_id", reqID)
		t.pending.Delete(reqID)
		return
	}
	if !telegramCallbackRequesterMatches(pending.requesterID, query.From) {
		slog.Warn("telegram: ignoring permission callback from non-requester", "req_id", reqID)
		return
	}
	val, ok = t.pending.LoadAndDelete(reqID)
	if !ok {
		slog.Debug("telegram: no pending permission for callback", "req_id", reqID)
		return
	}
	pending, ok = telegramPendingFromValue(val)
	if !ok {
		slog.Warn("telegram: invalid pending permission entry", "req_id", reqID)
		return
	}

	switch prefix {
	case 'a':
		pending.decision <- true
	default:
		pending.decision <- false
	}
}

func telegramPendingFromValue(val any) (telegramPendingConfirmation, bool) {
	switch pending := val.(type) {
	case telegramPendingConfirmation:
		return pending, pending.decision != nil
	case chan bool:
		return telegramPendingConfirmation{decision: pending}, pending != nil
	default:
		return telegramPendingConfirmation{}, false
	}
}

func telegramCallbackRequesterMatches(requesterID string, from *telegramUser) bool {
	requesterID = strings.TrimSpace(requesterID)
	if requesterID == "" {
		return true
	}
	if from == nil || from.ID == 0 {
		return false
	}
	return strconv.FormatInt(from.ID, 10) == requesterID
}

// sendInlineKeyboard sends a message with an inline keyboard for permission approval.
func (t *TelegramChannel) sendInlineKeyboard(ctx context.Context, chatID int64, description, reqID string) error {
	// Truncate description to fit within Telegram's message limits.
	msg := "⚠️ Permission required:\n\n" + description + "\n\nApprove or deny?"
	if len(msg) > telegramMaxChunk {
		msg = msg[:telegramMaxChunk-3] + "..."
	}

	keyboard := telegramInlineKeyboardMarkup{
		InlineKeyboard: [][]telegramInlineKeyboardButton{
			{
				{Text: "✅ Approve", CallbackData: "a:" + reqID},
				{Text: "❌ Deny", CallbackData: "d:" + reqID},
			},
		},
	}

	body := map[string]any{
		"chat_id":      chatID,
		"text":         msg,
		"reply_markup": keyboard,
	}
	data, err := json.Marshal(body)
	if err != nil {
		return err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		t.apiURL("sendMessage"), bytes.NewReader(data))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := t.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	var result telegramResponse[json.RawMessage]
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return fmt.Errorf("decode sendMessage: %w", err)
	}
	if !result.OK {
		return fmt.Errorf("sendMessage: %s", result.Description)
	}
	return nil
}

// answerCallbackQuery acknowledges a callback_query to Telegram,
// removing the loading spinner from the button.
func (t *TelegramChannel) answerCallbackQuery(ctx context.Context, callbackQueryID string) {
	body := map[string]any{"callback_query_id": callbackQueryID}
	data, _ := json.Marshal(body)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		t.apiURL("answerCallbackQuery"), bytes.NewReader(data))
	if err != nil {
		return
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := t.client.Do(req)
	if err != nil {
		return
	}
	resp.Body.Close()
}

// Compile-time check: TelegramChannel implements Confirmer.
var _ Confirmer = (*TelegramChannel)(nil)
