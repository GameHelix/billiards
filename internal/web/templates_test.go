package web

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/candylario/app/internal/notifier"
	"github.com/candylario/app/internal/store"
)

type templateScenario struct {
	name, view, route string
	user              *store.User
	data              any
	want              []string
}

// Exercise real page data without a database or external API. In particular,
// empty states and optional integrations must still produce complete pages.
func templateScenarios() []templateScenario {
	now := time.Date(2026, 10, 26, 12, 0, 0, 0, time.UTC)
	user := store.User{ID: uuid.New(), Email: "founder@example.com", EmailVerifiedAt: &now, CreatedAt: now, TenantReady: true}
	admin := store.User{ID: uuid.New(), Email: "admin@example.com", EmailVerifiedAt: &now, CreatedAt: now}
	unverified := user
	unverified.ID = uuid.New()
	unverified.EmailVerifiedAt = nil
	return []templateScenario{
		{"home", "home", "/", nil, nil, []string{"A little more", "Illustrative alert", "id=\"how-it-works\"", "https://t.me/candylario"}},
		{"radar", "radar", "/radar", nil, nil, []string{"pump-and-crash", "momentum run"}},
		{"login", "login", "/login", nil, nil, []string{"Welcome back.", "autocomplete=\"current-password\""}},
		{"signup", "signup", "/signup", nil, authForm{Email: "founder@example.com"}, []string{"A smarter start.", "action=\"/signup\""}},
		{"forgot", "forgot", "/forgot", nil, forgotPage{}, []string{"Forgot your password?", "action=\"/forgot\""}},
		{"forgot-sent", "forgot", "/forgot", nil, forgotPage{Sent: true, Email: user.Email}, []string{"Check your inbox.", "If an account exists"}},
		{"reset", "reset", "/reset", nil, resetPage{Email: user.Email, Token: "reset-example"}, []string{"Set a new password.", "value=\"reset-example\""}},
		{"verify-email", "verify_email", "/verify-email", &unverified, verifyPage{Email: user.Email}, []string{"Confirm your email.", "action=\"/verify-email/address\""}},
		{"verify-problem", "verify_email", "/verify-email", &unverified, verifyPage{Email: user.Email, Problem: "bounced: user unknown"}, []string{"Mail to this address failed: bounced: user unknown"}},
		{"error", "error", "/missing", nil, nil, []string{"Back to the start"}},
		{"telegram", "telegram", "/telegram", &user, telegramPage{Configured: true}, []string{"Connect a chat", "No chat connected yet.", "/disconnect", `href="/telegram" aria-current="page"`}},
		{"telegram-off", "telegram", "/telegram", &user, telegramPage{}, []string{"Telegram is not set up on this server.", "Open the channel"}},
		{"telegram-connected", "telegram", "/telegram", &user, telegramPage{Configured: true, Chats: []notifier.Chat{{ChatID: 42, Name: "@founder", LinkedAt: now}}}, []string{"@founder", "/telegram/chats/42/disconnect"}},
		{"telegram-error", "telegram", "/telegram", &user, telegramPage{Configured: true, Problem: "Telegram is not reachable"}, []string{"Telegram is not reachable", "Connect a chat"}},
		{"settings", "settings", "/settings", &user, nil, []string{"/settings/password", "/settings/email", "/settings/delete"}},
		{"admin", "admin", "/admin", &admin, []adminRow{{User: user}, {User: unverified}, {User: admin, Admin: true}}, []string{"id=\"accounts-table\"", "unconfirmed", "Platform admin", "/admin/password"}},
		{"admin-empty", "admin", "/admin", &admin, []adminRow(nil), []string{"No accounts yet."}},
	}
}

func TestAllPageTemplatesRender(t *testing.T) {
	s := &Server{tmpl: parseTemplates(), Logf: t.Logf, Admins: []string{"admin@example.com"}}
	for _, scenario := range templateScenarios() {
		t.Run(scenario.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodGet, scenario.route, nil)
			if scenario.user != nil {
				r = r.WithContext(context.WithValue(r.Context(), ctxKey{}, viewer{User: *scenario.user, CSRF: "example-csrf"}))
			}
			w := httptest.NewRecorder()
			s.render(w, r, scenario.view+".html", http.StatusOK, page{Title: scenario.name, Data: scenario.data})
			if w.Code != http.StatusOK {
				t.Fatalf("render: %d %s", w.Code, w.Body.String())
			}
			body := w.Body.String()
			for _, want := range append(scenario.want, `id="main"`, `name="viewport"`, `/static/app.css`, `/static/app.js`) {
				if !strings.Contains(body, want) {
					t.Errorf("page missing %q", want)
				}
			}
			if strings.Contains(body, "<script>alert(1)</script>") || strings.Contains(body, "ZgotmplZ") {
				t.Error("unescaped user data or unsafe template URL")
			}
			workspace := scenario.user != nil && scenario.user.Verified()
			if strings.Contains(body, `class="app-shell"`) != workspace {
				t.Error("workspace navigation must only appear for verified accounts")
			}
			if workspace && !strings.Contains(body, `name="csrf" value="example-csrf"`) {
				t.Error("authenticated page missing CSRF token")
			}
			if scenario.user != nil && s.isAdmin(*scenario.user) && strings.Contains(body, `href="/telegram`) {
				t.Error("platform admins must not see trading navigation")
			}
		})
	}
}

func TestPublicPagesAndDesignAssets(t *testing.T) {
	s := &Server{Logf: t.Logf}
	h := s.Handler()
	for _, route := range []string{"/", "/radar", "/terms", "/privacy", "/risk", "/refunds", "/login", "/signup", "/forgot", "/static/app.css", "/static/app.js", "/static/brand.svg", "/static/htmx.min.js", "/favicon.ico"} {
		t.Run(route, func(t *testing.T) {
			w := httptest.NewRecorder()
			h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, route, nil))
			if w.Code != http.StatusOK || w.Body.Len() == 0 {
				t.Fatalf("route: %d %s", w.Code, w.Body.String())
			}
			if w.Header().Get("Content-Security-Policy") == "" {
				t.Error("security headers missing")
			}
		})
	}
}

func TestInteractiveFragmentsRender(t *testing.T) {
	s := &Server{tmpl: parseTemplates(), Logf: t.Logf}
	for _, tc := range []struct {
		name string
		data any
		want string
	}{
		{"tglink", newLinkView(notifier.Link{URL: "https://t.me/TestBot?start=code1", ExpiresAt: time.Now().Add(time.Minute)}), "Open in Telegram"},
		{"tglink", linkView{Problem: "Telegram is not reachable"}, "Try again"},
	} {
		t.Run(tc.name+"/"+tc.want, func(t *testing.T) {
			w := httptest.NewRecorder()
			s.partial(w, tc.name, http.StatusOK, tc.data)
			if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), tc.want) {
				t.Fatalf("fragment: %d %s", w.Code, w.Body.String())
			}
		})
	}
}
