// Package notifier calls the notifier's API: one-time links that connect a
// Telegram chat to a tenant, and the chats connected.
package notifier

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

type Client struct {
	BaseURL string
	Token   string
	HTTP    *http.Client
}

// Link opens Telegram on the bot and connects the chat, once, until it
// expires.
type Link struct {
	URL       string    `json:"url"`
	ExpiresAt time.Time `json:"expires_at"`
}

type Chat struct {
	ChatID   int64     `json:"chat_id"`
	Name     string    `json:"name"`
	LinkedAt time.Time `json:"linked_at"`
}

// Error is a request the notifier refused.
type Error struct {
	Status  int
	Code    string `json:"code"`
	Message string `json:"message"`
}

func (e *Error) Error() string { return fmt.Sprintf("notifier %d %s: %s", e.Status, e.Code, e.Message) }

// ErrUnavailable is returned when the notifier cannot be reached.
var ErrUnavailable = errors.New("the notifier is unreachable")

func (c *Client) do(ctx context.Context, method, path string, out any) error {
	req, err := http.NewRequestWithContext(ctx, method, strings.TrimRight(c.BaseURL, "/")+path, bytes.NewReader(nil))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.Token)
	hc := c.HTTP
	if hc == nil {
		hc = http.DefaultClient
	}
	resp, err := hc.Do(req)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrUnavailable, errors.Unwrap(err))
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		var body struct {
			Error *Error `json:"error"`
		}
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<16))
		if json.Unmarshal(raw, &body) != nil || body.Error == nil {
			return &Error{Status: resp.StatusCode, Code: "internal", Message: http.StatusText(resp.StatusCode)}
		}
		body.Error.Status = resp.StatusCode
		return body.Error
	}
	if out == nil {
		return nil
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

func tenantPath(tenant string, rest ...string) string {
	p := "/v1/tenants/" + url.PathEscape(tenant)
	for _, r := range rest {
		p += "/" + url.PathEscape(r)
	}
	return p
}

func (c *Client) NewLink(ctx context.Context, tenant string) (Link, error) {
	var out Link
	return out, c.do(ctx, http.MethodPost, tenantPath(tenant, "links"), &out)
}

func (c *Client) Chats(ctx context.Context, tenant string) ([]Chat, error) {
	var out struct {
		Chats []Chat `json:"chats"`
	}
	return out.Chats, c.do(ctx, http.MethodGet, tenantPath(tenant, "chats"), &out)
}

func (c *Client) Disconnect(ctx context.Context, tenant string, chat int64) error {
	return c.do(ctx, http.MethodDelete, tenantPath(tenant, "chats", strconv.FormatInt(chat, 10)), nil)
}
