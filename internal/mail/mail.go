// Package mail sends the app's emails: through Mailjet's Send API when it is
// set up, or to the log for a local run.
package mail

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// Message is one email to one address, as plain text and HTML.
type Message struct {
	To      string
	Subject string
	Text    string
	HTML    string
}

type Sender interface {
	Send(ctx context.Context, m Message) error
}

// Mailjet sends through Mailjet's Send API v3.1, from an address on a domain
// Mailjet has validated.
type Mailjet struct {
	APIKey    string
	SecretKey string
	From      string
	FromName  string
	URL       string // the Send API; empty means Mailjet's own
	HTTP      *http.Client
}

const sendURL = "https://api.mailjet.com/v3.1/send"

type address struct {
	Email string `json:"Email"`
	Name  string `json:"Name,omitempty"`
}

type mjError struct {
	ErrorCode    string `json:"ErrorCode"`
	ErrorMessage string `json:"ErrorMessage"`
}

func (m *Mailjet) Send(ctx context.Context, msg Message) error {
	body, err := json.Marshal(map[string]any{"Messages": []map[string]any{{
		"From":     address{Email: m.From, Name: m.FromName},
		"To":       []address{{Email: msg.To}},
		"Subject":  msg.Subject,
		"TextPart": msg.Text,
		"HTMLPart": msg.HTML,
	}}})
	if err != nil {
		return err
	}
	url := m.URL
	if url == "" {
		url = sendURL
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.SetBasicAuth(m.APIKey, m.SecretKey)
	hc := m.HTTP
	if hc == nil {
		hc = http.DefaultClient
	}
	resp, err := hc.Do(req)
	if err != nil {
		return fmt.Errorf("mailjet: %w", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	var out struct {
		mjError
		Messages []struct {
			Status string    `json:"Status"`
			Errors []mjError `json:"Errors"`
		} `json:"Messages"`
	}
	json.Unmarshal(raw, &out)
	if resp.StatusCode == http.StatusOK && len(out.Messages) == 1 && out.Messages[0].Status == "success" {
		return nil
	}
	var problems []string
	if out.ErrorMessage != "" {
		problems = append(problems, strings.TrimSpace(out.ErrorCode+" "+out.ErrorMessage))
	}
	for _, m := range out.Messages {
		for _, e := range m.Errors {
			problems = append(problems, strings.TrimSpace(e.ErrorCode+" "+e.ErrorMessage))
		}
	}
	if len(problems) == 0 {
		problems = append(problems, http.StatusText(resp.StatusCode))
	}
	return fmt.Errorf("mailjet: %d %s", resp.StatusCode, strings.Join(problems, "; "))
}

// Log writes emails to the log instead of sending them, for a local run
// without Mailjet.
type Log struct {
	Logf func(format string, args ...any)
}

func (l Log) Send(_ context.Context, m Message) error {
	l.Logf("email not sent, Mailjet is not set up; to %s: %s\n%s", m.To, m.Subject, m.Text)
	return nil
}
