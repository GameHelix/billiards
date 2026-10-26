package mail

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestMailjetSendsOneMessageWithTheKeys(t *testing.T) {
	var got struct {
		Messages []struct {
			From     address
			To       []address
			Subject  string
			TextPart string
			HTMLPart string
		}
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if user, pass, ok := r.BasicAuth(); !ok || user != "key" || pass != "secret" {
			w.WriteHeader(401)
			w.Write([]byte(`{"ErrorIdentifier":"x","ErrorCode":"mj-0015","ErrorMessage":"Invalid credentials","StatusCode":401}`))
			return
		}
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Error(err)
		}
		w.Write([]byte(`{"Messages":[{"Status":"success","To":[{"Email":"ada@example.com","MessageID":1}]}]}`))
	}))
	defer srv.Close()
	m := &Mailjet{APIKey: "key", SecretKey: "secret", From: "no-reply@candy.surf", FromName: "Candylario", URL: srv.URL}
	if err := m.Send(context.Background(), Message{To: "ada@example.com", Subject: "Hi", Text: "text", HTML: "<p>html</p>"}); err != nil {
		t.Fatal(err)
	}
	if len(got.Messages) != 1 {
		t.Fatalf("messages: %+v", got)
	}
	msg := got.Messages[0]
	if msg.From.Email != "no-reply@candy.surf" || msg.From.Name != "Candylario" || len(msg.To) != 1 || msg.To[0].Email != "ada@example.com" ||
		msg.Subject != "Hi" || msg.TextPart != "text" || msg.HTMLPart != "<p>html</p>" {
		t.Fatalf("message: %+v", msg)
	}
	m.SecretKey = "wrong"
	if err := m.Send(context.Background(), Message{To: "ada@example.com"}); err == nil || !strings.Contains(err.Error(), "mj-0015 Invalid credentials") {
		t.Fatalf("wrong secret: %v", err)
	}
}

func TestMailjetReportsAMessageItRefused(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(400)
		w.Write([]byte(`{"Messages":[{"Status":"error","Errors":[{"ErrorCode":"send-0008","ErrorMessage":"\"no-reply@candy.surf\" is not an authorized sender email address for your account.","StatusCode":403,"ErrorRelatedTo":["From"]}]}]}`))
	}))
	defer srv.Close()
	err := (&Mailjet{URL: srv.URL}).Send(context.Background(), Message{To: "ada@example.com"})
	if err == nil || !strings.Contains(err.Error(), "send-0008") || !strings.Contains(err.Error(), "not an authorized sender") {
		t.Fatalf("refused: %v", err)
	}
}
