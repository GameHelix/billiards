package web

import (
	"encoding/base64"
	"errors"
	"html/template"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"golang.org/x/crypto/bcrypt"
	"rsc.io/qr"

	"github.com/candylario/app/internal/notifier"
)

type telegramPage struct {
	Configured bool
	Chats      []notifier.Chat
	Problem    string
}

func (s *Server) telegram(w http.ResponseWriter, r *http.Request) {
	v := current(r)
	p := telegramPage{Configured: s.Notifier != nil}
	if p.Configured {
		ctx, cancel := callCtx(r)
		defer cancel()
		chats, err := s.Notifier.Chats(ctx, v.User.TenantID.String())
		if err != nil {
			s.Logf("notifier: %v", err)
			p.Problem = "Telegram is not reachable right now; try again in a minute."
		}
		p.Chats = chats
	}
	s.render(w, r, "telegram.html", http.StatusOK, page{Title: "Telegram", Data: p})
}

type linkView struct {
	Link    notifier.Link
	Problem string
	Bot     string       // the bot's username
	Code    string       // what the bot's /start takes
	WebURL  string       // the link in Telegram Web
	QR      template.URL // the link as a QR code, for a phone
}

// newLinkView adds ways to use a link on a computer without Telegram: a QR
// code for the phone, Telegram Web, and the message to send by hand.
func newLinkView(l notifier.Link) linkView {
	v := linkView{Link: l}
	if u, err := url.Parse(l.URL); err == nil {
		v.Bot, v.Code = strings.Trim(u.Path, "/"), u.Query().Get("start")
	}
	if v.Bot != "" && v.Code != "" {
		v.WebURL = "https://web.telegram.org/k/#?tgaddr=" + url.QueryEscape("tg://resolve?domain="+v.Bot+"&start="+v.Code)
	}
	if c, err := qr.Encode(l.URL, qr.M); err == nil {
		c.Scale = 4
		v.QR = template.URL("data:image/png;base64," + base64.StdEncoding.EncodeToString(c.PNG()))
	}
	return v
}

func (s *Server) telegramLink(w http.ResponseWriter, r *http.Request) {
	if s.Notifier == nil {
		s.partial(w, "tglink", http.StatusOK, linkView{Problem: "Telegram is not set up on this server."})
		return
	}
	ctx, cancel := callCtx(r)
	defer cancel()
	link, err := s.Notifier.NewLink(ctx, current(r).User.TenantID.String())
	if err != nil {
		s.Logf("notifier: %v", err)
		s.partial(w, "tglink", http.StatusOK, linkView{Problem: "Telegram is not reachable right now; try again in a minute."})
		return
	}
	s.partial(w, "tglink", http.StatusOK, newLinkView(link))
}

func (s *Server) telegramDisconnect(w http.ResponseWriter, r *http.Request) {
	chat, err := strconv.ParseInt(r.PathValue("chat"), 10, 64)
	if err != nil || s.Notifier == nil {
		s.notFound(w, r)
		return
	}
	ctx, cancel := callCtx(r)
	defer cancel()
	switch err := s.Notifier.Disconnect(ctx, current(r).User.TenantID.String(), chat); {
	case err == nil:
		s.flash(w, "Chat disconnected.")
	default:
		var ne *notifier.Error
		if errors.As(err, &ne) && ne.Status == http.StatusNotFound {
			s.flash(w, "That chat was already disconnected.")
		} else {
			s.Logf("notifier: %v", err)
			s.flash(w, "Telegram is not reachable right now; try again in a minute.")
		}
	}
	http.Redirect(w, r, "/telegram", http.StatusSeeOther)
}

func (s *Server) settingsPage(w http.ResponseWriter, r *http.Request, status int, problem string) {
	s.render(w, r, "settings.html", status, page{Title: "Settings", Error: problem})
}

func (s *Server) settings(w http.ResponseWriter, r *http.Request) {
	s.settingsPage(w, r, http.StatusOK, "")
}

func (s *Server) changePassword(w http.ResponseWriter, r *http.Request) {
	switch problem, err := s.setPassword(r); {
	case err != nil:
		s.fail(w, r, err)
	case problem != "":
		s.settingsPage(w, r, http.StatusUnprocessableEntity, problem)
	default:
		s.flash(w, "Password changed; other browsers were logged out.")
		http.Redirect(w, r, "/settings", http.StatusSeeOther)
	}
}

// setPassword stores the form's new password if the current one is right,
// logs the account's other browsers out and emails it a notice. problem is
// what to tell the user when the form is refused.
func (s *Server) setPassword(r *http.Request) (problem string, err error) {
	v := current(r)
	cur, next, again := r.PostFormValue("current"), r.PostFormValue("password"), r.PostFormValue("again")
	switch {
	case bcrypt.CompareHashAndPassword([]byte(v.User.PasswordHash), []byte(cur)) != nil:
		return "The current password is wrong.", nil
	case next != again:
		return "The new passwords differ.", nil
	}
	if problem := checkPassword(next); problem != "" {
		return problem, nil
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(next), bcrypt.DefaultCost)
	if err != nil {
		return "", err
	}
	if err := s.Store.SetPassword(r.Context(), v.User.ID, string(hash)); err != nil {
		return "", err
	}
	if err := s.Store.DeleteOtherSessions(r.Context(), v.User.ID, v.Token); err != nil {
		s.Logf("database: %v", err)
	}
	if err := s.sendPasswordNotice(r.Context(), v.User); err != nil {
		s.Logf("password notice: %v", err)
	}
	return "", nil
}
