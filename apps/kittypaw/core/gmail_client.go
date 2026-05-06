package core

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"html"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	xhtml "golang.org/x/net/html"
)

type GmailMessageRef struct {
	ID       string
	ThreadID string
}

type GmailMessage struct {
	ID       string
	ThreadID string
	Snippet  string
	From     string
	Subject  string
	Date     string
	BodyText string
}

type GmailClient struct {
	baseURL string
	client  *http.Client
}

func NewGmailClient(baseURL string, client *http.Client) *GmailClient {
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}
	if baseURL == "" {
		baseURL = "https://gmail.googleapis.com"
	}
	return &GmailClient{baseURL: strings.TrimRight(baseURL, "/"), client: client}
}

func (c *GmailClient) ListMessages(ctx context.Context, accessToken string, maxResults int) ([]GmailMessageRef, error) {
	return c.SearchMessages(ctx, accessToken, maxResults, "")
}

func (c *GmailClient) SearchMessages(ctx context.Context, accessToken string, maxResults int, query string) ([]GmailMessageRef, error) {
	if maxResults <= 0 {
		maxResults = 10
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/gmail/v1/users/me/messages", nil)
	if err != nil {
		return nil, err
	}
	q := req.URL.Query()
	q.Set("maxResults", strconv.Itoa(maxResults))
	q.Set("fields", "messages(id,threadId)")
	if strings.TrimSpace(query) != "" {
		q.Set("q", strings.TrimSpace(query))
	}
	req.URL.RawQuery = q.Encode()
	req.Header.Set("Authorization", "Bearer "+accessToken)

	resp, err := c.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("gmail list request: %w", err)
	}
	defer resp.Body.Close()
	if err := gmailStatusError(resp); err != nil {
		return nil, err
	}
	var body struct {
		Messages []struct {
			ID       string `json:"id"`
			ThreadID string `json:"threadId"`
		} `json:"messages"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return nil, fmt.Errorf("decode gmail list: %w", err)
	}
	out := make([]GmailMessageRef, 0, len(body.Messages))
	for _, m := range body.Messages {
		out = append(out, GmailMessageRef{ID: m.ID, ThreadID: m.ThreadID})
	}
	return out, nil
}

func (c *GmailClient) GetMessage(ctx context.Context, accessToken, id string) (GmailMessage, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/gmail/v1/users/me/messages/"+id, nil)
	if err != nil {
		return GmailMessage{}, err
	}
	q := req.URL.Query()
	q.Set("format", "full")
	q.Set("fields", "id,threadId,snippet,payload(headers,body,parts,mimeType)")
	req.URL.RawQuery = q.Encode()
	req.Header.Set("Authorization", "Bearer "+accessToken)

	resp, err := c.client.Do(req)
	if err != nil {
		return GmailMessage{}, fmt.Errorf("gmail get request: %w", err)
	}
	defer resp.Body.Close()
	if err := gmailStatusError(resp); err != nil {
		return GmailMessage{}, err
	}
	var body gmailMessageResponse
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return GmailMessage{}, fmt.Errorf("decode gmail message: %w", err)
	}
	headers := gmailHeaders(body.Payload.Headers)
	text, _ := gmailPlainText(body.Payload)
	return GmailMessage{
		ID:       body.ID,
		ThreadID: body.ThreadID,
		Snippet:  body.Snippet,
		From:     headers["from"],
		Subject:  headers["subject"],
		Date:     headers["date"],
		BodyText: text,
	}, nil
}

type gmailMessageResponse struct {
	ID       string           `json:"id"`
	ThreadID string           `json:"threadId"`
	Snippet  string           `json:"snippet"`
	Payload  gmailMessagePart `json:"payload"`
}

type gmailMessagePart struct {
	MimeType string             `json:"mimeType"`
	Headers  []gmailHeader      `json:"headers"`
	Body     gmailMessageBody   `json:"body"`
	Parts    []gmailMessagePart `json:"parts"`
}

type gmailHeader struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

type gmailMessageBody struct {
	Data string `json:"data"`
}

func gmailHeaders(headers []gmailHeader) map[string]string {
	out := make(map[string]string, len(headers))
	for _, h := range headers {
		out[strings.ToLower(h.Name)] = h.Value
	}
	return out
}

func gmailPlainText(part gmailMessagePart) (string, bool) {
	if text, ok := gmailPlainTextPreferred(part); ok {
		return text, true
	}
	if text, ok := gmailHTMLTextPreferred(part); ok {
		return text, true
	}
	if part.Body.Data != "" {
		if decoded, err := decodeGmailBody(part.Body.Data); err == nil {
			return decoded, true
		}
	}
	return "", false
}

func gmailPlainTextPreferred(part gmailMessagePart) (string, bool) {
	if strings.HasPrefix(strings.ToLower(part.MimeType), "text/plain") && part.Body.Data != "" {
		if decoded, err := decodeGmailBody(part.Body.Data); err == nil {
			return decoded, true
		}
	}
	for _, child := range part.Parts {
		if text, ok := gmailPlainTextPreferred(child); ok {
			return text, true
		}
	}
	return "", false
}

func gmailHTMLTextPreferred(part gmailMessagePart) (string, bool) {
	if strings.HasPrefix(strings.ToLower(part.MimeType), "text/html") && part.Body.Data != "" {
		if decoded, err := decodeGmailBody(part.Body.Data); err == nil {
			return htmlToReadableText(decoded), true
		}
	}
	for _, child := range part.Parts {
		if text, ok := gmailHTMLTextPreferred(child); ok {
			return text, true
		}
	}
	return "", false
}

func decodeGmailBody(raw string) (string, error) {
	if b, err := base64.RawURLEncoding.DecodeString(raw); err == nil {
		return string(b), nil
	}
	b, err := base64.URLEncoding.DecodeString(raw)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

func htmlToReadableText(raw string) string {
	root, err := xhtml.Parse(strings.NewReader(raw))
	if err != nil {
		return normalizeTextWhitespace(html.UnescapeString(raw))
	}
	var chunks []string
	var walk func(*xhtml.Node)
	walk = func(n *xhtml.Node) {
		if n.Type == xhtml.ElementNode {
			switch strings.ToLower(n.Data) {
			case "head", "script", "style", "noscript":
				return
			}
		}
		if n.Type == xhtml.TextNode {
			if text := normalizeTextWhitespace(n.Data); text != "" {
				chunks = append(chunks, text)
			}
		}
		for child := n.FirstChild; child != nil; child = child.NextSibling {
			walk(child)
		}
	}
	walk(root)
	return strings.Join(chunks, " ")
}

func normalizeTextWhitespace(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

func gmailStatusError(resp *http.Response) error {
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return nil
	}
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		return fmt.Errorf("gmail authorization failed (%d); run: kittypaw connect gmail", resp.StatusCode)
	}
	return fmt.Errorf("gmail response %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
}
