package web

import (
	"context"
	"crypto/subtle"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/bcrypt"

	"github.com/candylario/trader/api"

	"github.com/candylario/app/internal/store"
	"github.com/candylario/app/internal/tiers"
)

type ctxKey struct{}

// viewer is the logged-in user of a request.
type viewer struct {
	User  store.User
	CSRF  string
	Token string
}

func current(r *http.Request) viewer {
	v, _ := r.Context().Value(ctxKey{}).(viewer)
	return v
}

// sessionOf finds the request's live session, if any.
func (s *Server) sessionOf(r *http.Request) (viewer, bool) {
	c, err := r.Cookie(sessionCookie)
	if err != nil || c.Value == "" {
		return viewer{}, false
	}
	sess, user, err := s.Store.Session(r.Context(), c.Value)
	if err != nil {
		if !errors.Is(err, store.ErrNotFound) {
			s.Logf("database: %v", err)
		}
		return viewer{}, false
	}
	return viewer{User: user, CSRF: sess.CSRF, Token: c.Value}, true
}

// Who may open a page.
type access int

const (
	anyAccount    access = iota // also an account whose address is not confirmed
	platformUser                // a confirmed account that trades; admins go to /admin
	platformAdmin               // a confirmed admin
)

// requireUser loads the session, checks the CSRF token of every change,
// sends an account whose address is not confirmed to confirm it and an admin
// to the admin page, makes sure the engine knows the tenant, and redirects
// to log-in otherwise.
func (s *Server) requireUser(h http.HandlerFunc) http.Handler {
	return s.requireAccess(h, platformUser)
}

// requireAccess is requireUser for one kind of account. Only platform users
// get an engine tenant: admins run the platform and never trade on it.
func (s *Server) requireAccess(h http.HandlerFunc, who access) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		v, ok := s.sessionOf(r)
		if !ok {
			s.clearSession(w)
			if r.Header.Get("HX-Request") != "" {
				w.Header().Set("HX-Redirect", "/login")
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			http.Redirect(w, r, "/login", http.StatusSeeOther)
			return
		}
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			token := r.Header.Get("X-CSRF-Token")
			if token == "" {
				token = r.PostFormValue("csrf")
			}
			if subtle.ConstantTimeCompare([]byte(token), []byte(v.CSRF)) != 1 {
				http.Error(w, "The page expired; reload it and try again.", http.StatusForbidden)
				return
			}
		}
		switch {
		case who == anyAccount:
		case !v.User.Verified():
			s.sendTo(w, r, "/verify-email")
			return
		case who == platformAdmin && !s.isAdmin(v.User):
			s.notFound(w, r)
			return
		case who == platformUser && s.isAdmin(v.User):
			s.sendTo(w, r, "/admin")
			return
		case who == platformUser && !v.User.TenantReady:
			if err := s.ensureTenant(r.Context(), &v.User); err != nil {
				s.fail(w, r, fmt.Errorf("engine tenant: %w", err))
				return
			}
		}
		h(w, r.WithContext(context.WithValue(r.Context(), ctxKey{}, v)))
	})
}

// sendTo redirects, the htmx way for an htmx request.
func (s *Server) sendTo(w http.ResponseWriter, r *http.Request, path string) {
	if r.Header.Get("HX-Request") != "" {
		w.Header().Set("HX-Redirect", path)
		w.WriteHeader(http.StatusForbidden)
		return
	}
	http.Redirect(w, r, path, http.StatusSeeOther)
}

// landing is where an account goes once it is logged in.
func (s *Server) landing(u store.User) string {
	if s.isAdmin(u) {
		return "/admin"
	}
	return "/agents"
}

// ensureTenant creates the user's tenant in the engine, on their tier. The
// id is the app's, so a retry after a timeout finds the same tenant.
func (s *Server) ensureTenant(ctx context.Context, u *store.User) error {
	t, ok := tiers.Get(u.Tier)
	if !ok {
		return fmt.Errorf("unknown tier %q", u.Tier)
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	if _, err := s.Engine.CreateTenant(ctx, api.NewTenant{ID: u.TenantID.String(), Name: u.Email, Plan: t.Plan}); err != nil {
		return err
	}
	if err := s.Store.SetTenantReady(ctx, u.ID); err != nil {
		return err
	}
	u.TenantReady = true
	return nil
}

func (s *Server) startSession(w http.ResponseWriter, r *http.Request, u store.User) error {
	token, _, err := s.Store.CreateSession(r.Context(), u.ID, sessionTTL)
	if err != nil {
		return err
	}
	http.SetCookie(w, &http.Cookie{Name: sessionCookie, Value: token, Path: "/", MaxAge: int(sessionTTL.Seconds()), HttpOnly: true, SameSite: http.SameSiteLaxMode, Secure: s.https()})
	return nil
}

func (s *Server) clearSession(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{Name: sessionCookie, Value: "", Path: "/", MaxAge: -1, HttpOnly: true, SameSite: http.SameSiteLaxMode, Secure: s.https()})
}

// sameSite rejects a form posted from another site, for the forms a
// logged-out visitor can send.
func sameSite(r *http.Request) bool {
	if site := r.Header.Get("Sec-Fetch-Site"); site != "" && site != "same-origin" && site != "none" {
		return false
	}
	if origin := r.Header.Get("Origin"); origin != "" {
		u, err := url.Parse(origin)
		if err != nil || !strings.EqualFold(u.Host, r.Host) {
			return false
		}
	}
	return true
}

type authForm struct {
	Email string
}

func (s *Server) signupForm(w http.ResponseWriter, r *http.Request) {
	if v, ok := s.sessionOf(r); ok {
		http.Redirect(w, r, s.landing(v.User), http.StatusSeeOther)
		return
	}
	s.render(w, r, "signup.html", http.StatusOK, page{Title: "Sign up"})
}

func (s *Server) loginForm(w http.ResponseWriter, r *http.Request) {
	if v, ok := s.sessionOf(r); ok {
		http.Redirect(w, r, s.landing(v.User), http.StatusSeeOther)
		return
	}
	s.render(w, r, "login.html", http.StatusOK, page{Title: "Log in"})
}

func normalizeEmail(s string) string { return strings.ToLower(strings.TrimSpace(s)) }

func checkEmail(email string) string {
	at := strings.Index(email, "@")
	if len(email) < 3 || len(email) > 254 || at < 1 || at == len(email)-1 || strings.ContainsAny(email, " \t\n") {
		return "Enter a valid email address."
	}
	return ""
}

func checkPassword(pw string) string {
	switch {
	case len(pw) < 10:
		return "Use a password of at least 10 characters."
	case len(pw) > 72:
		return "Use a password of at most 72 characters."
	}
	return ""
}

func (s *Server) signup(w http.ResponseWriter, r *http.Request) {
	if !sameSite(r) {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}
	email := normalizeEmail(r.PostFormValue("email"))
	password := r.PostFormValue("password")
	problem := checkEmail(email)
	if problem == "" {
		problem = checkPassword(password)
	}
	if problem != "" {
		s.render(w, r, "signup.html", http.StatusUnprocessableEntity, page{Title: "Sign up", Error: problem, Data: authForm{Email: email}})
		return
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	u, err := s.Store.CreateUser(r.Context(), email, string(hash), tiers.Default)
	if errors.Is(err, store.ErrEmailTaken) {
		s.render(w, r, "signup.html", http.StatusUnprocessableEntity, page{Title: "Sign up", Error: "An account with this email exists. Log in instead.", Data: authForm{Email: email}})
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
	// The engine tenant comes once the address is confirmed.
	if err := s.sendVerification(r.Context(), u); err != nil {
		s.Logf("verification email: %v", err)
		s.flash(w, "The email could not be sent; send a new link in a minute.")
	}
	http.Redirect(w, r, "/verify-email", http.StatusSeeOther)
}

// dummyHash keeps a log-in with an unknown email as slow as one with a
// wrong password.
var dummyHash, _ = bcrypt.GenerateFromPassword([]byte("not a password"), bcrypt.DefaultCost)

func (s *Server) login(w http.ResponseWriter, r *http.Request) {
	if !sameSite(r) {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}
	email := normalizeEmail(r.PostFormValue("email"))
	password := r.PostFormValue("password")
	if !s.logins.allow("ip:"+clientIP(r)) || !s.logins.allow("email:"+email) {
		s.render(w, r, "login.html", http.StatusTooManyRequests, page{Title: "Log in", Error: "Too many attempts; try again in a few minutes.", Data: authForm{Email: email}})
		return
	}
	u, err := s.Store.UserByEmail(r.Context(), email)
	hash := []byte(u.PasswordHash)
	if errors.Is(err, store.ErrNotFound) {
		hash = dummyHash
	} else if err != nil {
		s.fail(w, r, err)
		return
	}
	if bcrypt.CompareHashAndPassword(hash, []byte(password)) != nil || hash == nil || errors.Is(err, store.ErrNotFound) {
		s.render(w, r, "login.html", http.StatusUnauthorized, page{Title: "Log in", Error: "Wrong email or password.", Data: authForm{Email: email}})
		return
	}
	if err := s.startSession(w, r, u); err != nil {
		s.fail(w, r, err)
		return
	}
	http.Redirect(w, r, s.landing(u), http.StatusSeeOther)
}

func (s *Server) logout(w http.ResponseWriter, r *http.Request) {
	if !sameSite(r) {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}
	if c, err := r.Cookie(sessionCookie); err == nil && c.Value != "" {
		if err := s.Store.DeleteSession(r.Context(), c.Value); err != nil {
			s.Logf("database: %v", err)
		}
	}
	s.clearSession(w)
	http.Redirect(w, r, "/login", http.StatusSeeOther)
}

func clientIP(r *http.Request) string {
	if fwd := r.Header.Get("X-Forwarded-For"); fwd != "" {
		return strings.TrimSpace(strings.Split(fwd, ",")[0])
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// limiter allows n events per key in a window.
type limiter struct {
	mu     sync.Mutex
	n      int
	window time.Duration
	hits   map[string][]time.Time
}

func newLimiter(n int, window time.Duration) *limiter {
	return &limiter{n: n, window: window, hits: map[string][]time.Time{}}
}

func (l *limiter) allow(key string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := time.Now()
	if len(l.hits) > 10000 {
		for k, ts := range l.hits {
			if len(ts) == 0 || now.Sub(ts[len(ts)-1]) > l.window {
				delete(l.hits, k)
			}
		}
	}
	var live []time.Time
	for _, t := range l.hits[key] {
		if now.Sub(t) < l.window {
			live = append(live, t)
		}
	}
	if len(live) >= l.n {
		l.hits[key] = live
		return false
	}
	l.hits[key] = append(live, now)
	return true
}
