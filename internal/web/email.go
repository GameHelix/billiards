package web

import (
	"bytes"
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"html/template"
	"io"
	"net/http"
	"net/url"
	"time"

	"golang.org/x/crypto/bcrypt"

	"github.com/candylario/app/internal/mail"
	"github.com/candylario/app/internal/store"
)

// Emails: the link that confirms an account's address, the link that sets a
// forgotten password, the notice of a changed password, and Mailjet's
// reports of mail that could not be delivered.

const (
	verifyTTL = 48 * time.Hour
	resetTTL  = time.Hour
)

// email sends a short message built around one link, if any.
func (s *Server) email(ctx context.Context, to, subject, lead, link, tail string) error {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	esc := template.HTMLEscapeString
	text, html := lead+"\n\n", "<p>"+esc(lead)+"</p>\n"
	if link != "" {
		text += link + "\n\n"
		html += "<p><a href=\"" + esc(link) + "\">" + esc(link) + "</a></p>\n"
	}
	return s.Mail.Send(ctx, mail.Message{To: to, Subject: subject, Text: text + tail + "\n", HTML: html + "<p>" + esc(tail) + "</p>\n"})
}

// sendVerification emails the account a link that confirms its address.
func (s *Server) sendVerification(ctx context.Context, u store.User) error {
	token, err := s.Store.CreateEmailToken(ctx, u, store.PurposeVerify, verifyTTL)
	if err != nil {
		return err
	}
	return s.email(ctx, u.Email, "Confirm your email for Candylario",
		"Confirm your email address to start using Candylario:",
		s.BaseURL+"/verify?token="+url.QueryEscape(token),
		"The link works for 48 hours. If you did not sign up, ignore this email.")
}

type verifyPage struct {
	Email   string
	Problem string
}

// verifyNotice asks an account whose address is not confirmed to open the
// link, and offers a new one or another address.
func (s *Server) verifyNotice(w http.ResponseWriter, r *http.Request) {
	v := current(r)
	if v.User.Verified() {
		http.Redirect(w, r, s.landing(v.User), http.StatusSeeOther)
		return
	}
	s.render(w, r, "verify_email.html", http.StatusOK, page{Title: "Confirm your email", Data: verifyPage{Email: v.User.Email, Problem: v.User.EmailProblem}})
}

func (s *Server) resendVerification(w http.ResponseWriter, r *http.Request) {
	v := current(r)
	switch {
	case v.User.Verified():
		http.Redirect(w, r, s.landing(v.User), http.StatusSeeOther)
		return
	case !s.mails.allow("verify:" + v.User.ID.String()):
		s.flash(w, "Too many links sent; try again in an hour.")
	case s.sendVerification(r.Context(), v.User) != nil:
		s.flash(w, "The email could not be sent; try again in a minute.")
	default:
		s.flash(w, "We sent a new link to "+v.User.Email+".")
	}
	http.Redirect(w, r, "/verify-email", http.StatusSeeOther)
}

// changeAddress fixes a mistyped address before it is confirmed.
func (s *Server) changeAddress(w http.ResponseWriter, r *http.Request) {
	v := current(r)
	if v.User.Verified() {
		http.Redirect(w, r, s.landing(v.User), http.StatusSeeOther)
		return
	}
	email := normalizeEmail(r.PostFormValue("email"))
	problem := checkEmail(email)
	if problem == "" && !s.mails.allow("verify:"+v.User.ID.String()) {
		problem = "Too many links sent; try again in an hour."
	}
	if problem == "" {
		switch err := s.Store.ChangeEmail(r.Context(), v.User.ID, email); {
		case errors.Is(err, store.ErrEmailTaken):
			problem = "An account with this email exists."
		case err != nil:
			s.fail(w, r, err)
			return
		}
	}
	if problem != "" {
		s.render(w, r, "verify_email.html", http.StatusUnprocessableEntity, page{Title: "Confirm your email", Error: problem, Data: verifyPage{Email: v.User.Email, Problem: v.User.EmailProblem}})
		return
	}
	v.User.Email, v.User.EmailProblem = email, ""
	if err := s.sendVerification(r.Context(), v.User); err != nil {
		s.Logf("verification email: %v", err)
		s.flash(w, "The address is changed, but the email could not be sent; send a new link in a minute.")
	} else {
		s.flash(w, "We sent a link to "+email+".")
	}
	http.Redirect(w, r, "/verify-email", http.StatusSeeOther)
}

// verify confirms the address of the link's account.
func (s *Server) verify(w http.ResponseWriter, r *http.Request) {
	u, err := s.Store.VerifyEmail(r.Context(), r.URL.Query().Get("token"))
	v, loggedIn := s.sessionOf(r)
	switch {
	case errors.Is(err, store.ErrNotFound) && loggedIn && v.User.Verified():
		http.Redirect(w, r, s.landing(v.User), http.StatusSeeOther)
	case errors.Is(err, store.ErrNotFound):
		s.linkExpired(w, r, "Log in to get a new one.")
	case err != nil:
		s.fail(w, r, err)
	case loggedIn && v.User.ID == u.ID && s.isAdmin(u):
		s.flash(w, "Email confirmed.")
		http.Redirect(w, r, "/admin", http.StatusSeeOther)
	case loggedIn && v.User.ID == u.ID:
		s.flash(w, "Email confirmed.")
		http.Redirect(w, r, "/telegram", http.StatusSeeOther)
	default:
		s.flash(w, "Email confirmed; log in.")
		http.Redirect(w, r, "/login", http.StatusSeeOther)
	}
}

const forgotHint = "Ask for a new one under Forgot your password."

func (s *Server) linkExpired(w http.ResponseWriter, r *http.Request, hint string) {
	s.render(w, r, "error.html", http.StatusGone, page{Title: "Link expired", Error: "This link is used, expired or replaced by a newer one. " + hint})
}

type forgotPage struct {
	Email string
	Sent  bool
}

func (s *Server) forgotForm(w http.ResponseWriter, r *http.Request) {
	s.render(w, r, "forgot.html", http.StatusOK, page{Title: "Forgot your password?", Data: forgotPage{}})
}

// forgot emails a reset link to the address if it has an account. The page
// says the same either way.
func (s *Server) forgot(w http.ResponseWriter, r *http.Request) {
	if !sameSite(r) {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}
	email := normalizeEmail(r.PostFormValue("email"))
	if problem := checkEmail(email); problem != "" {
		s.render(w, r, "forgot.html", http.StatusUnprocessableEntity, page{Title: "Forgot your password?", Error: problem, Data: forgotPage{Email: email}})
		return
	}
	if !s.mails.allow("forgot-ip:"+clientIP(r)) || !s.mails.allow("forgot:"+email) {
		s.render(w, r, "forgot.html", http.StatusTooManyRequests, page{Title: "Forgot your password?", Error: "Too many links asked for; try again in an hour.", Data: forgotPage{Email: email}})
		return
	}
	u, err := s.Store.UserByEmail(r.Context(), email)
	switch {
	case errors.Is(err, store.ErrNotFound):
	case err != nil:
		s.fail(w, r, err)
		return
	default:
		if err := s.sendReset(r.Context(), u); err != nil {
			s.Logf("reset email: %v", err)
		}
	}
	s.render(w, r, "forgot.html", http.StatusOK, page{Title: "Forgot your password?", Data: forgotPage{Email: email, Sent: true}})
}

func (s *Server) sendReset(ctx context.Context, u store.User) error {
	token, err := s.Store.CreateEmailToken(ctx, u, store.PurposeReset, resetTTL)
	if err != nil {
		return err
	}
	return s.email(ctx, u.Email, "Reset your Candylario password",
		"Someone asked to reset the password of your Candylario account. To choose a new one, open:",
		s.BaseURL+"/reset?token="+url.QueryEscape(token),
		"The link works for an hour. If it was not you, ignore this email; your password stays the same.")
}

type resetPage struct {
	Token string
	Email string
}

func (s *Server) resetForm(w http.ResponseWriter, r *http.Request) {
	token := r.URL.Query().Get("token")
	u, err := s.Store.EmailTokenUser(r.Context(), token, store.PurposeReset)
	if errors.Is(err, store.ErrNotFound) {
		s.linkExpired(w, r, forgotHint)
		return
	}
	if err != nil {
		s.fail(w, r, err)
		return
	}
	s.render(w, r, "reset.html", http.StatusOK, page{Title: "Set a new password", Data: resetPage{Token: token, Email: u.Email}})
}

// reset sets the new password, logs the account out everywhere else and in
// here.
func (s *Server) reset(w http.ResponseWriter, r *http.Request) {
	if !sameSite(r) {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}
	token, password := r.PostFormValue("token"), r.PostFormValue("password")
	problem := checkPassword(password)
	if password != r.PostFormValue("again") {
		problem = "The new passwords differ."
	}
	if problem != "" {
		u, err := s.Store.EmailTokenUser(r.Context(), token, store.PurposeReset)
		if errors.Is(err, store.ErrNotFound) {
			s.linkExpired(w, r, forgotHint)
			return
		}
		if err != nil {
			s.fail(w, r, err)
			return
		}
		s.render(w, r, "reset.html", http.StatusUnprocessableEntity, page{Title: "Set a new password", Error: problem, Data: resetPage{Token: token, Email: u.Email}})
		return
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	u, err := s.Store.ResetPassword(r.Context(), token, string(hash))
	if errors.Is(err, store.ErrNotFound) {
		s.linkExpired(w, r, forgotHint)
		return
	}
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if err := s.startSession(w, r, u); err != nil {
		s.fail(w, r, err)
		return
	}
	s.flash(w, "Password changed; other browsers were logged out.")
	http.Redirect(w, r, s.landing(u), http.StatusSeeOther)
}

// sendPasswordNotice tells the account its password changed, in case it was
// not its owner.
func (s *Server) sendPasswordNotice(ctx context.Context, u store.User) error {
	return s.email(ctx, u.Email, "Your Candylario password was changed",
		"The password of your Candylario account was just changed, and other browsers were logged out. If it was not you, set a new one here:",
		s.BaseURL+"/forgot",
		"If it was you, there is nothing to do.")
}

// mailEvent is one of Mailjet's event reports; only failures matter here.
type mailEvent struct {
	Event      string `json:"event"`
	Email      string `json:"email"`
	HardBounce bool   `json:"hard_bounce"`
	Error      string `json:"error"`
}

func (e mailEvent) problem() string {
	switch {
	case e.Event == "bounce" && e.HardBounce:
		return trimJoin("bounced", e.Error)
	case e.Event == "blocked":
		return trimJoin("blocked", e.Error)
	case e.Event == "spam":
		return "reported as spam"
	}
	return ""
}

func trimJoin(what, why string) string {
	if why == "" {
		return what
	}
	return what + ": " + why
}

// mailjetWebhook takes Mailjet's event reports, one at a time or grouped,
// and records which addresses mail cannot reach. Mailjet logs in with basic
// auth, user mailjet and MAILJET_WEBHOOK_SECRET, and retries until it gets a
// 200.
func (s *Server) mailjetWebhook(w http.ResponseWriter, r *http.Request) {
	if s.MailjetWebhookSecret == "" {
		http.NotFound(w, r)
		return
	}
	user, pass, ok := r.BasicAuth()
	if !ok || subtle.ConstantTimeCompare([]byte(user), []byte("mailjet")) != 1 || subtle.ConstantTimeCompare([]byte(pass), []byte(s.MailjetWebhookSecret)) != 1 {
		w.Header().Set("WWW-Authenticate", `Basic realm="mailjet"`)
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	raw, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	var events []mailEvent
	if raw = bytes.TrimSpace(raw); len(raw) > 0 && raw[0] == '[' {
		err = json.Unmarshal(raw, &events)
	} else {
		var e mailEvent
		err = json.Unmarshal(raw, &e)
		events = []mailEvent{e}
	}
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	for _, e := range events {
		if p := e.problem(); p != "" && e.Email != "" {
			if err := s.Store.SetEmailProblem(r.Context(), normalizeEmail(e.Email), p); err != nil {
				s.Logf("database: %v", err)
				w.WriteHeader(http.StatusServiceUnavailable)
				return
			}
			s.Logf("mail to %s failed: %s", e.Email, p)
		}
	}
	w.WriteHeader(http.StatusOK)
}
