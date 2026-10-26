// Package web serves the app: accounts, onboarding and dashboards on the
// engine API. Users' keys pass through to the engine's vault and are never
// kept here.
package web

import (
	"bytes"
	"context"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"io/fs"
	"net/http"
	"net/url"
	"path"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/candylario/trader/api"

	"github.com/candylario/app/internal/engine"
	"github.com/candylario/app/internal/mail"
	"github.com/candylario/app/internal/notifier"
	"github.com/candylario/app/internal/store"
	"github.com/candylario/app/internal/tiers"
)

//go:embed templates static
var files embed.FS

type Server struct {
	Store    *store.Store
	Engine   *api.Client
	Events   *engine.Events
	Notifier *notifier.Client // nil when Telegram is not set up
	BaseURL  string
	Mail     mail.Sender // nil writes emails to the log
	Logf     func(format string, args ...any)

	// MailjetWebhookSecret is the password Mailjet's event reports carry;
	// empty turns the webhook off.
	MailjetWebhookSecret string
	// Admins are the addresses whose confirmed accounts run the platform.
	Admins []string
	// ServerIP is the address exchanges see the engine's calls come from,
	// for users to allow on their keys.
	ServerIP string

	once   sync.Once
	tmpl   map[string]*template.Template
	logins *limiter
	mails  *limiter
}

const (
	sessionCookie = "session"
	flashCookie   = "flash"
	sessionTTL    = 30 * 24 * time.Hour
)

func (s *Server) init() {
	s.tmpl = parseTemplates()
	s.logins = newLimiter(10, 15*time.Minute)
	s.mails = newLimiter(5, time.Hour)
	if s.Logf == nil {
		s.Logf = func(string, ...any) {}
	}
	if s.Mail == nil {
		s.Mail = mail.Log{Logf: s.Logf}
	}
	if s.Events == nil {
		s.Events = &engine.Events{Client: s.Engine}
	}
}

func (s *Server) Handler() http.Handler {
	s.once.Do(s.init)
	mux := http.NewServeMux()
	static, _ := fs.Sub(files, "static")
	mux.Handle("GET /static/", http.StripPrefix("/static/", http.FileServerFS(static)))
	mux.Handle("GET /favicon.ico", http.FileServerFS(static))
	mux.HandleFunc("GET /healthz", s.health)
	mux.HandleFunc("GET /{$}", s.home)
	mux.HandleFunc("GET /signup", s.signupForm)
	mux.HandleFunc("POST /signup", s.signup)
	mux.HandleFunc("GET /login", s.loginForm)
	mux.HandleFunc("POST /login", s.login)
	mux.HandleFunc("POST /logout", s.logout)
	mux.HandleFunc("GET /verify", s.verify)
	mux.HandleFunc("GET /forgot", s.forgotForm)
	mux.HandleFunc("POST /forgot", s.forgot)
	mux.HandleFunc("GET /reset", s.resetForm)
	mux.HandleFunc("POST /reset", s.reset)
	mux.HandleFunc("GET /change-email", s.confirmEmailChange)
	mux.HandleFunc("POST /webhooks/mailjet", s.mailjetWebhook)
	mux.Handle("GET /verify-email", s.requireAccess(s.verifyNotice, anyAccount))
	mux.Handle("POST /verify-email", s.requireAccess(s.resendVerification, anyAccount))
	mux.Handle("POST /verify-email/address", s.requireAccess(s.changeAddress, anyAccount))
	mux.Handle("GET /admin", s.requireAccess(s.admin, platformAdmin))
	mux.Handle("POST /admin/users/{id}/tier", s.requireAccess(s.adminSetTier, platformAdmin))
	mux.Handle("POST /admin/password", s.requireAccess(s.adminChangePassword, platformAdmin))
	for pattern, h := range map[string]http.HandlerFunc{
		"GET /setup":                             s.setup,
		"GET /setup/keys":                        s.keysForm,
		"POST /setup/keys":                       s.addKeys,
		"GET /setup/agent":                       s.agentForm,
		"POST /setup/agent":                      s.createAgent,
		"GET /agents":                            s.agents,
		"GET /agents/new":                        s.agentForm,
		"POST /agents":                           s.createAgent,
		"GET /agents/{id}":                       s.agent,
		"GET /agents/{id}/status":                s.agentStatus,
		"GET /agents/{id}/events":                s.agentEvents,
		"GET /agents/{id}/chart":                 s.agentChart,
		"GET /agents/{id}/sellall":               s.sellAllForm,
		"POST /agents/{id}/commands":             s.command,
		"POST /agents/{id}/start":                s.startAgent,
		"POST /agents/{id}/stop":                 s.stopAgent,
		"GET /telegram":                          s.telegram,
		"POST /telegram/links":                   s.telegramLink,
		"POST /telegram/chats/{chat}/disconnect": s.telegramDisconnect,
		"GET /settings":                          s.settings,
		"POST /settings/keys":                    s.addKeys,
		"POST /settings/keys/{id}/revoke":        s.revokeKey,
		"POST /settings/password":                s.changePassword,
		"POST /settings/email":                   s.changeEmail,
		"POST /settings/delete":                  s.deleteAccount,
	} {
		mux.Handle(pattern, s.requireUser(h))
	}
	return s.secure(s.logged(mux))
}

// health says whether the app, the engine and the notifier all answer, for
// an uptime monitor: 200 when they do, 503 naming the ones that do not.
func (s *Server) health(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	parts := map[string]bool{"app": s.Store.Ping(ctx) == nil, "engine": answers(ctx, s.Engine.BaseURL)}
	if s.Notifier != nil {
		parts["notifier"] = answers(ctx, s.Notifier.BaseURL)
	}
	ok := true
	for _, up := range parts {
		ok = ok && up
	}
	parts["ok"] = ok
	status := http.StatusOK
	if !ok {
		status = http.StatusServiceUnavailable
	}
	writeJSON(w, status, parts)
}

// answers says whether a Candylario service's health check answers 200.
func answers(ctx context.Context, base string) bool {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(base, "/")+"/v1/health", nil)
	if err != nil {
		return false
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return false
	}
	resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func (s *Server) home(w http.ResponseWriter, r *http.Request) {
	if v, ok := s.sessionOf(r); ok {
		http.Redirect(w, r, s.landing(v.User), http.StatusSeeOther)
		return
	}
	s.render(w, r, "home.html", http.StatusOK, page{Title: "Trading agents that work around the clock"})
}

// secure sets the headers every response carries.
func (s *Server) secure(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Content-Security-Policy", "default-src 'self'; img-src 'self' data:; frame-ancestors 'none'; form-action 'self'")
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "same-origin")
		h.Set("X-Frame-Options", "DENY")
		next.ServeHTTP(w, r)
	})
}

type statusWriter struct {
	http.ResponseWriter
	status int
}

func (w *statusWriter) WriteHeader(code int) {
	w.status = code
	w.ResponseWriter.WriteHeader(code)
}

func (s *Server) logged(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/static/") || r.URL.Path == "/favicon.ico" || r.URL.Path == "/healthz" {
			next.ServeHTTP(w, r)
			return
		}
		sw := &statusWriter{ResponseWriter: w, status: http.StatusOK}
		started := time.Now()
		next.ServeHTTP(sw, r)
		s.Logf("%s %s %d %dms", r.Method, r.URL.Path, sw.status, time.Since(started).Milliseconds())
	})
}

// page is what every full page gets.
type page struct {
	Title     string
	View      string
	Section   string
	Workspace bool
	User      *store.User
	CSRF      string
	Flash     string
	Error     string
	Admin     bool
	Data      any
}

func (s *Server) render(w http.ResponseWriter, r *http.Request, name string, status int, p page) {
	p.View = strings.TrimSuffix(name, ".html")
	p.Section = strings.Split(strings.TrimPrefix(r.URL.Path, "/"), "/")[0]
	if v, ok := r.Context().Value(ctxKey{}).(viewer); ok {
		p.User, p.CSRF, p.Admin = &v.User, v.CSRF, s.isAdmin(v.User)
		p.Workspace = v.User.Verified()
	}
	if p.Flash == "" {
		p.Flash = s.takeFlash(w, r)
	}
	t := s.tmpl[name]
	if t == nil {
		s.Logf("no template %s", name)
		http.Error(w, "Something went wrong.", http.StatusInternalServerError)
		return
	}
	var buf bytes.Buffer
	if err := t.ExecuteTemplate(&buf, "layout", p); err != nil {
		s.Logf("template %s: %v", name, err)
		http.Error(w, "Something went wrong.", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	buf.WriteTo(w)
}

// partial renders one fragment, for htmx.
func (s *Server) partial(w http.ResponseWriter, name string, status int, data any) {
	var buf bytes.Buffer
	if err := s.tmpl["partials"].ExecuteTemplate(&buf, name, data); err != nil {
		s.Logf("partial %s: %v", name, err)
		http.Error(w, "Something went wrong.", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	buf.WriteTo(w)
}

func (s *Server) fail(w http.ResponseWriter, r *http.Request, err error) {
	s.Logf("error: %v", err)
	s.render(w, r, "error.html", http.StatusServiceUnavailable, page{Title: "Not available", Error: "The service is not available right now; try again in a minute."})
}

func (s *Server) notFound(w http.ResponseWriter, r *http.Request) {
	s.render(w, r, "error.html", http.StatusNotFound, page{Title: "Not found", Error: "There is nothing here."})
}

// flash stores a message for the next page the browser loads.
func (s *Server) flash(w http.ResponseWriter, msg string) {
	http.SetCookie(w, &http.Cookie{Name: flashCookie, Value: url.QueryEscape(msg), Path: "/", MaxAge: 120, HttpOnly: true, SameSite: http.SameSiteLaxMode, Secure: s.https()})
}

func (s *Server) takeFlash(w http.ResponseWriter, r *http.Request) string {
	c, err := r.Cookie(flashCookie)
	if err != nil {
		return ""
	}
	http.SetCookie(w, &http.Cookie{Name: flashCookie, Value: "", Path: "/", MaxAge: -1, HttpOnly: true, SameSite: http.SameSiteLaxMode, Secure: s.https()})
	msg, _ := url.QueryUnescape(c.Value)
	return msg
}

func (s *Server) https() bool { return strings.HasPrefix(strings.ToLower(s.BaseURL), "https://") }

// userMessage is what to tell the user about a failed engine call. The
// engine's own words are shown when it refused something.
func userMessage(err error) string {
	var e *api.Error
	if errors.As(err, &e) {
		switch e.Code {
		case "refused", "not_running", "no_account", "conflict", "bad_request":
			return e.Message
		case "not_found":
			return "Not found."
		}
	}
	return "The engine did not answer; try again in a minute."
}

func isCode(err error, code string) bool {
	var e *api.Error
	return errors.As(err, &e) && e.Code == code
}

func parseTemplates() map[string]*template.Template {
	funcs := template.FuncMap{
		"money": func(s string) string {
			f, err := strconv.ParseFloat(s, 64)
			if err != nil {
				return s
			}
			return fmt.Sprintf("%.2f", f)
		},
		"usd":    func(f float64) string { return fmt.Sprintf("%.2f", f) },
		"pct":    func(f float64) string { return fmt.Sprintf("%.0f%%", f*100) },
		"when":   func(t time.Time) string { return t.UTC().Format("Jan 2, 15:04") + " UTC" },
		"coin":   tiers.Coin,
		"rating": func(r string) string { return strings.ReplaceAll(r, "_", " ") },
		"pretty": func(raw json.RawMessage) string {
			var buf bytes.Buffer
			if json.Indent(&buf, raw, "", "  ") != nil {
				return string(raw)
			}
			return buf.String()
		},
		"summary": engine.Summary,
		"interval": func(h float64) string {
			if h < 1 {
				return fmt.Sprintf("%.0f minutes", h*60)
			}
			if h == 1 {
				return "1 hour"
			}
			return fmt.Sprintf("%g hours", h)
		},
	}
	base := template.Must(template.New("").Funcs(funcs).ParseFS(files, "templates/layout.html", "templates/partials/*.html"))
	pages, err := fs.Glob(files, "templates/pages/*.html")
	if err != nil {
		panic(err)
	}
	m := map[string]*template.Template{"partials": base}
	for _, p := range pages {
		m[path.Base(p)] = template.Must(template.Must(base.Clone()).ParseFS(files, p))
	}
	return m
}

// callCtx bounds one call to the engine or the notifier.
func callCtx(r *http.Request) (context.Context, context.CancelFunc) {
	return context.WithTimeout(r.Context(), 40*time.Second)
}
