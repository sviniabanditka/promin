// Package telegram is the Promin companion bot: search titles from the
// phone, open one on a TV that is online. Long polling only (no webhook),
// net/http + encoding/json only.
package telegram

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Client is a minimal Telegram Bot API client. The token is part of every
// request URL, so errors are scrubbed of *url.Error before they can reach a
// log line.
type Client struct {
	base  string
	token string
	http  *http.Client
}

// NewClient builds a Client for token against base (https://api.telegram.org).
func NewClient(base, token string) *Client {
	// Long poll asks for 30 s; the transport timeout must outlive it.
	return &Client{base: strings.TrimRight(base, "/"), token: token, http: &http.Client{Timeout: 45 * time.Second}}
}

// --- wire types (only the fields the bot reads) ------------------------------

type User struct {
	ID       int64  `json:"id"`
	Username string `json:"username"`
}

type Chat struct {
	ID   int64  `json:"id"`
	Type string `json:"type"` // private | group | supergroup | channel
}

type Message struct {
	MessageID int64  `json:"message_id"`
	Chat      Chat   `json:"chat"`
	Text      string `json:"text"`
}

type CallbackQuery struct {
	ID      string   `json:"id"`
	Data    string   `json:"data"`
	Message *Message `json:"message"`
}

type Update struct {
	UpdateID      int64          `json:"update_id"`
	Message       *Message       `json:"message"`
	CallbackQuery *CallbackQuery `json:"callback_query"`
}

type InlineKeyboardButton struct {
	Text         string `json:"text"`
	CallbackData string `json:"callback_data,omitempty"`
}

type InlineKeyboardMarkup struct {
	InlineKeyboard [][]InlineKeyboardButton `json:"inline_keyboard"`
}

type apiResponse struct {
	OK          bool            `json:"ok"`
	Result      json.RawMessage `json:"result"`
	Description string          `json:"description"`
	ErrorCode   int             `json:"error_code"`
	Parameters  struct {
		RetryAfter int `json:"retry_after"`
	} `json:"parameters"`
}

// APIError is a non-ok Bot API answer.
type APIError struct {
	Code        int
	Description string
	RetryAfter  time.Duration
}

func (e *APIError) Error() string { return fmt.Sprintf("telegram: %d %s", e.Code, e.Description) }

// call POSTs params as JSON to method and decodes result into out (may be nil).
func (c *Client) call(ctx context.Context, method string, params, out any) error {
	body, err := json.Marshal(params)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.base+"/bot"+c.token+"/"+method, bytes.NewReader(body))
	if err != nil {
		return scrub(err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("telegram %s: %w", method, scrub(err))
	}
	defer resp.Body.Close()
	var r apiResponse
	if err := json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(&r); err != nil {
		return fmt.Errorf("telegram %s: decode: %w", method, err)
	}
	if !r.OK {
		return &APIError{Code: r.ErrorCode, Description: r.Description, RetryAfter: time.Duration(r.Parameters.RetryAfter) * time.Second}
	}
	if out != nil {
		return json.Unmarshal(r.Result, out)
	}
	return nil
}

// scrub drops the URL (which carries the bot token) from transport errors.
func scrub(err error) error {
	var ue *url.Error
	if errors.As(err, &ue) {
		return ue.Err
	}
	return err
}

func (c *Client) GetMe(ctx context.Context) (User, error) {
	var u User
	err := c.call(ctx, "getMe", struct{}{}, &u)
	return u, err
}

func (c *Client) GetUpdates(ctx context.Context, offset int64, timeout time.Duration) ([]Update, error) {
	var out []Update
	err := c.call(ctx, "getUpdates", map[string]any{
		"offset":          offset,
		"timeout":         int(timeout.Seconds()),
		"allowed_updates": []string{"message", "callback_query"},
	}, &out)
	return out, err
}

func (c *Client) SendMessage(ctx context.Context, chatID int64, text string, markup *InlineKeyboardMarkup) error {
	p := map[string]any{"chat_id": chatID, "text": text}
	if markup != nil {
		p["reply_markup"] = markup
	}
	return c.call(ctx, "sendMessage", p, nil)
}

// EditMessage replaces text and keyboard of an existing message in one call
// (editMessageText accepts reply_markup, so a separate
// editMessageReplyMarkup is not needed).
func (c *Client) EditMessage(ctx context.Context, chatID, messageID int64, text string, markup *InlineKeyboardMarkup) error {
	p := map[string]any{"chat_id": chatID, "message_id": messageID, "text": text}
	if markup != nil {
		p["reply_markup"] = markup
	}
	return c.call(ctx, "editMessageText", p, nil)
}

func (c *Client) AnswerCallback(ctx context.Context, id, text string) error {
	p := map[string]any{"callback_query_id": id}
	if text != "" {
		p["text"] = text
	}
	return c.call(ctx, "answerCallbackQuery", p, nil)
}
