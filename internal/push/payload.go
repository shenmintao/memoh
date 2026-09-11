// Package push defines the transport-independent notification input contract.
package push

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"mime"
	"strings"
	"unicode/utf8"
)

const (
	MaxBodyBytes = 64 * 1024
	MaxTextBytes = 16 * 1024
)

// Payload accepts ordinary notifications and sms_forwarding's standard POST JSON.
// Routing and credentials cannot be supplied in the payload.
type Payload struct {
	Title       string `json:"title,omitempty"`
	Text        string `json:"text,omitempty"`
	Content     string `json:"content,omitempty"`
	Message     string `json:"message,omitempty"`
	Sender      string `json:"sender,omitempty"`
	Timestamp   string `json:"timestamp,omitempty"`
	LocalNumber string `json:"local_number,omitempty"`
	Remark      string `json:"remark,omitempty"`
	EventID     string `json:"event_id,omitempty"`
}

type Notification struct {
	Text        string
	EventID     string
	PayloadHash string
}

func Hash(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

func Parse(contentType string, body []byte, idempotencyKey string) (Notification, error) {
	if len(body) > MaxBodyBytes || !utf8.Valid(body) {
		return Notification{}, errors.New("invalid or oversized UTF-8 body")
	}
	mediaType, _, err := mime.ParseMediaType(contentType)
	if err != nil {
		return Notification{}, errors.New("Content-Type must be application/json or text/plain")
	}
	var payload Payload
	switch mediaType {
	case "text/plain":
		payload.Text = string(body)
	case "application/json":
		if err := json.Unmarshal(body, &payload); err != nil {
			return Notification{}, errors.New("invalid notification JSON")
		}
	default:
		return Notification{}, errors.New("Content-Type must be application/json or text/plain")
	}
	text := ""
	for _, value := range []string{payload.Text, payload.Content, payload.Message} {
		if strings.TrimSpace(value) != "" {
			if text != "" {
				return Notification{}, errors.New("provide only one of text, content or message")
			}
			text = strings.TrimSpace(value)
		}
	}
	if text == "" {
		return Notification{}, errors.New("message text is required")
	}
	lines := make([]string, 0, 7)
	if title := strings.TrimSpace(payload.Title); title != "" {
		lines = append(lines, title)
	}
	for _, field := range []struct{ label, value string }{
		{"发送者", payload.Sender}, {"接收号码", payload.LocalNumber}, {"时间", payload.Timestamp}, {"备注", payload.Remark},
	} {
		if value := strings.TrimSpace(field.value); value != "" {
			lines = append(lines, fmt.Sprintf("%s：%s", field.label, value))
		}
	}
	if len(lines) > 0 {
		lines = append(lines, "")
	}
	lines = append(lines, text)
	text = strings.Join(lines, "\n")
	if len(text) > MaxTextBytes || strings.ContainsRune(text, '\x00') {
		return Notification{}, errors.New("message exceeds 16 KiB or contains NUL")
	}
	eventID := strings.TrimSpace(idempotencyKey)
	if eventID == "" {
		eventID = strings.TrimSpace(payload.EventID)
	}
	if len(eventID) > 256 {
		return Notification{}, errors.New("event ID exceeds 256 bytes")
	}
	hash := Hash(text)
	if eventID == "" && payload.Sender != "" && payload.Timestamp != "" {
		eventID = "sms:" + hash
	}
	return Notification{Text: text, EventID: eventID, PayloadHash: hash}, nil
}
