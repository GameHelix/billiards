package web

import (
	"errors"
	"net/http"
	"net/url"

	"golang.org/x/crypto/bcrypt"

	"github.com/candylario/app/internal/store"
)

// An account's own big changes in Settings: moving to another email
// address, confirmed by a link to it, and deleting the account for good.

// contact is where users write when something is wrong with their account.
func (s *Server) contact() string {
	if len(s.Admins) > 0 {
		return s.Admins[0]
	}
	return ""
}

func (s *Server) changeEmail(w http.ResponseWriter, r *http.Request) {
	v := current(r)
	email := normalizeEmail(r.PostFormValue("email"))
	problem := checkEmail(email)
	switch {
	case problem != "":
	case bcrypt.CompareHashAndPassword([]byte(v.User.PasswordHash), []byte(r.PostFormValue("current"))) != nil:
		problem = "The current password is wrong."
	case email == v.User.Email:
		problem = "That is already your email."
	case s.adminAddress(email):
		problem = "That address cannot be used."
	case !s.mails.allow("change:" + v.User.ID.String()):
		problem = "Too many links sent; try again in an hour."
	}
	if problem == "" {
		switch _, err := s.Store.UserByEmail(r.Context(), email); {
		case err == nil:
			problem = "An account with this email exists."
		case !errors.Is(err, store.ErrNotFound):
			s.fail(w, r, err)
			return
		}
	}
	if problem != "" {
		s.settingsPage(w, r, http.StatusUnprocessableEntity, problem)
		return
	}
	token, err := s.Store.CreateEmailChange(r.Context(), v.User, email, verifyTTL)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if err := s.email(r.Context(), email, "Confirm your new email for Candylario",
		"Open this link to make this address the email of your Candylario account:",
		s.BaseURL+"/change-email?token="+url.QueryEscape(token),
		"The link works for 48 hours. If you did not ask for this, ignore this email."); err != nil {
		s.Logf("change email: %v", err)
		s.flash(w, "The email could not be sent; try again in a minute.")
	} else {
		s.flash(w, "We sent a link to "+email+". Your email changes when you open it.")
	}
	http.Redirect(w, r, "/settings", http.StatusSeeOther)
}

// confirmEmailChange moves the link's account to the address the link went
// to, and tells the old address.
func (s *Server) confirmEmailChange(w http.ResponseWriter, r *http.Request) {
	u, old, err := s.Store.MoveEmail(r.Context(), r.URL.Query().Get("token"))
	switch {
	case errors.Is(err, store.ErrNotFound):
		s.linkExpired(w, r, "Ask for a new one in Settings.")
		return
	case errors.Is(err, store.ErrEmailTaken):
		s.render(w, r, "error.html", http.StatusConflict, page{Title: "Address taken", Error: "Another account uses this address now; choose another one in Settings."})
		return
	case err != nil:
		s.fail(w, r, err)
		return
	}
	tail := "If it was you, there is nothing to do."
	if c := s.contact(); c != "" {
		tail = "If it was not you, write to " + c + " at once."
	}
	if err := s.email(r.Context(), old, "Your Candylario email was changed",
		"The email of your Candylario account was just changed to "+u.Email+".", "", tail); err != nil {
		s.Logf("change notice: %v", err)
	}
	s.flash(w, "Your email is now "+u.Email+".")
	if v, ok := s.sessionOf(r); ok && v.User.ID == u.ID {
		http.Redirect(w, r, "/settings", http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, "/login", http.StatusSeeOther)
}

// deleteAccount closes the account for good: the engine closes its tenant,
// so any wallet key it holds is erased; its Telegram chats disconnect;
// and the app forgets the account with its sessions and links. When the
// engine cannot close the tenant, nothing is deleted.
func (s *Server) deleteAccount(w http.ResponseWriter, r *http.Request) {
	v := current(r)
	switch {
	case bcrypt.CompareHashAndPassword([]byte(v.User.PasswordHash), []byte(r.PostFormValue("current"))) != nil:
		s.settingsPage(w, r, http.StatusUnprocessableEntity, "The current password is wrong.")
		return
	case r.PostFormValue("confirm") != "DELETE":
		s.settingsPage(w, r, http.StatusUnprocessableEntity, "Type DELETE to confirm.")
		return
	}
	ctx, cancel := callCtx(r)
	defer cancel()
	tenant := v.User.TenantID.String()
	if v.User.TenantReady {
		if _, err := s.Engine.CloseTenant(ctx, tenant); err != nil && !isCode(err, "not_found") {
			s.Logf("close tenant %s: %v", tenant, err)
			s.flash(w, userMessage(err))
			http.Redirect(w, r, "/settings", http.StatusSeeOther)
			return
		}
	}
	if s.Notifier != nil {
		chats, err := s.Notifier.Chats(ctx, tenant)
		if err != nil {
			s.Logf("notifier: %v", err)
		}
		for _, c := range chats {
			if err := s.Notifier.Disconnect(ctx, tenant, c.ChatID); err != nil {
				s.Logf("notifier: %v", err)
			}
		}
	}
	if err := s.Store.DeleteUser(r.Context(), v.User.ID); err != nil {
		s.fail(w, r, err)
		return
	}
	s.clearSession(w)
	s.flash(w, "Your account is deleted.")
	http.Redirect(w, r, "/", http.StatusSeeOther)
}
