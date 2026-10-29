package web

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/candylario/trader/api"

	"github.com/candylario/app/internal/engine"
	"github.com/candylario/app/internal/notifier"
	"github.com/candylario/app/internal/store"
	"github.com/candylario/app/internal/tiers"
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
	user := store.User{ID: uuid.New(), Email: "founder@example.com", Tier: "pro", EmailVerifiedAt: &now, CreatedAt: now, TenantReady: true}
	admin := store.User{ID: uuid.New(), Email: "admin@example.com", EmailVerifiedAt: &now, CreatedAt: now}
	unverified := user
	unverified.ID = uuid.New()
	unverified.EmailVerifiedAt = nil
	tier, _ := tiers.Get("pro")
	trial, _ := tiers.Get("trial")
	key := keyView{ID: "binance-example", Kind: "binance", Mode: "testnet", Hint: "…5678", CreatedAt: now}
	keys := keysForm{Action: "/setup/keys", CSRF: "example-csrf", Setup: true}
	form := agentForm{Action: "/setup/agent", Tier: trial, Setup: true, Capital: "100", Interval: 4, Intervals: []float64{4, 8, 12, 24}, Binance: []keyView{key}, Coins: []coinOption{{Symbol: "BTCUSDT", Coin: "BTC", Checked: true}, {Symbol: "ETHUSDT", Coin: "ETH", Checked: true}}}
	agent := api.Agent{ID: "steady-btc", Name: "steady-btc", Mode: "testnet", Status: "active", Running: true}
	next := now.Add(4 * time.Hour)
	status := api.Status{Equity: "1048.72", Capital: "1000", BuyAndHold: "1031.25", Cash: "300", Fees: "0.52", Running: true, Trading: api.TradingOn, NextDecision: &next, Trades: 8, RightCalls: 6, GradedCalls: 8,
		Positions:    []api.Position{{Coin: "BTC", Symbol: "BTCUSDT", Qty: "0.0063", Value: "500", Weight: .48}, {Coin: "ETH", Symbol: "ETHUSDT", Qty: "0.08", Value: "248.72", Weight: .24}},
		LastDecision: &api.Decision{At: now, Kind: "scheduled", Summary: "Market context <script>alert(1)</script>", Ratings: map[string]api.Rating{"BTCUSDT": {Rating: "strong_buy", Confidence: "medium", Rationale: "A measured allocation."}}, CostUSD: .07},
		AISpend:      &api.AISpend{TodayUSD: .21, DailyLimitUSD: 5, MonthUSD: 1.8, MonthlyLimitUSD: 100}}
	points := []engine.Point{{At: now.Add(-3 * time.Hour), Equity: 1000, BuyAndHold: 1000}, {At: now.Add(-2 * time.Hour), Equity: 1015, BuyAndHold: 1010}, {At: now.Add(-time.Hour), Equity: 1027, BuyAndHold: 1021}, {At: now, Equity: 1048.72, BuyAndHold: 1031.25}}
	events := []eventView{{At: now, Type: "decision.complete", Summary: "A scheduled review completed.", Raw: json.RawMessage(`{"summary":"Market context <script>alert(1)</script>"}`)}}
	entries := eventViews([]api.Event{
		{Seq: 52, At: now, Type: "notice", Hash: "3fa29c0e1d2b4a5f6e7d8c9b", Payload: json.RawMessage(`{"text":"🧪 ⚠️ Price data unavailable <script>alert(1)</script>","alert":"prices"}`)},
		{Seq: 51, At: now.Add(-time.Minute), Type: "trade.booked", Hash: "9d8e7f", Payload: json.RawMessage(`{"trade":{"side":"BUY","symbol":"BTCUSDT","qty":"0.001","avg_price":"67000"}}`)},
		{Seq: 50, At: now.Add(-2 * time.Minute), Type: "something.new", Payload: json.RawMessage(`{"a":"x"}`)},
	})
	log := activityPage{Agent: agent, Agents: []link{{Title: "steady-btc", URL: "/activity?agent=steady-btc", Current: true}, {Title: "eth-swing", URL: "/activity?agent=eth-swing"}},
		Filters: []link{{Title: "All", URL: "/activity?agent=steady-btc", Current: true}, {Title: "Trades", URL: "/activity?agent=steady-btc&show=trades"}},
		Rows:    activityRows{Events: entries, Newer: "/activity?after=52&agent=steady-btc", Older: "/activity?agent=steady-btc&before=50"}}
	return []templateScenario{
		{"home", "home", "/", nil, nil, []string{"A little more", "Illustrative preview", "id=\"how-it-works\""}},
		{"login", "login", "/login", nil, nil, []string{"Welcome back.", "autocomplete=\"current-password\""}},
		{"signup", "signup", "/signup", nil, authForm{Email: "founder@example.com"}, []string{"A smarter start.", "action=\"/signup\""}},
		{"forgot", "forgot", "/forgot", nil, forgotPage{}, []string{"Forgot your password?", "action=\"/forgot\""}},
		{"forgot-sent", "forgot", "/forgot", nil, forgotPage{Sent: true, Email: user.Email}, []string{"Check your inbox.", "If an account exists"}},
		{"reset", "reset", "/reset", nil, resetPage{Email: user.Email, Token: "reset-example"}, []string{"Set a new password.", "value=\"reset-example\""}},
		{"verify-email", "verify_email", "/verify-email", &unverified, verifyPage{Email: user.Email}, []string{"Confirm your email.", "action=\"/verify-email/address\""}},
		{"verify-problem", "verify_email", "/verify-email", &unverified, verifyPage{Email: user.Email, Problem: "bounced: user unknown"}, []string{"Mail to this address failed: bounced: user unknown"}},
		{"error", "error", "/missing", nil, nil, []string{"Back to the start"}},
		{"agents-empty", "agents", "/agents", &user, agentsPage{Tier: trial, CanAdd: true}, []string{"No agents yet.", "Set up your first agent"}},
		{"agents", "agents", "/agents", &user, agentsPage{Tier: tier, CanAdd: true, Agents: []agentRow{{Agent: agent, Equity: status.Equity, Trading: "on"}}}, []string{"steady-btc", "1048.72", "id=\"agent-table\""}},
		{"agents-limit", "agents", "/agents", &user, agentsPage{Tier: trial, Agents: []agentRow{{Agent: agent, Problem: "status unavailable"}}}, []string{"status unavailable", "Your Trial tier runs"}},
		{"setup-keys", "setup_keys", "/setup/keys", &user, keys, []string{"aria-current=\"step\"", "testnet.binance.vision", "sk-ant-"}},
		{"new-agent", "agent_form", "/setup/agent", &user, form, []string{"New agent", "value=\"BTCUSDT\"", "Create and start"}},
		{"agent", "agent", "/agents/steady-btc", &user, agentPage{Agent: agent, Status: statusView{Agent: agent, Status: &status}, Chart: buildChart(points), Updates: updatesView{Agent: agent.ID, Events: events}}, []string{"1048.72", "strong buy", "Market context &lt;script&gt;", "Sell everything", "hx-trigger=\"every 20s\"", "A scheduled review completed.", `href="/activity?agent=steady-btc"`}},
		{"agent-waiting", "agent", "/agents/steady-btc", &user, agentPage{Agent: api.Agent{ID: agent.ID, Name: agent.Name, Mode: "testnet", Status: "stopped"}, Status: statusView{Agent: api.Agent{ID: agent.ID, Status: "stopped"}, Problem: "Not started yet."}}, []string{"Start agent", "No equity history yet", "Nothing recorded yet."}},
		{"activity", "activity", "/activity", &user, log, []string{"Every move, on record.", "eth-swing", "show=trades", "Price data unavailable &lt;script&gt;", "Trade booked", "something.new", "Entry 52", "3fa29c0e1d2b4a5f", "Load older entries", "before=50", "after=52", `href="/activity" aria-current="page"`}},
		{"activity-empty", "activity", "/activity", &user, activityPage{}, []string{"Nothing on record yet.", "/setup"}},
		{"activity-problem", "activity", "/activity", &user, activityPage{Agent: agent, Problem: "The log is not available right now."}, []string{"The log is not available right now."}},
		{"telegram", "telegram", "/telegram", &user, telegramPage{Configured: true, Setup: true}, []string{"Connect a chat", "aria-current=\"step\"", "/sellall"}},
		{"telegram-off", "telegram", "/telegram", &user, telegramPage{}, []string{"Telegram is not set up on this server."}},
		{"telegram-connected", "telegram", "/telegram", &user, telegramPage{Configured: true, Chats: []notifier.Chat{{ChatID: 42, Name: "@founder", LinkedAt: now}}}, []string{"@founder", "/telegram/chats/42/disconnect"}},
		{"telegram-error", "telegram", "/telegram", &user, telegramPage{Configured: true, Problem: "Telegram is not reachable"}, []string{"Telegram is not reachable", "Connect a chat"}},
		{"settings", "settings", "/settings", &user, settingsPage{Tier: tier, Binance: []keyView{key}, Anthropic: []keyView{{ID: "anthropic-example", Hint: "…1234", CreatedAt: now}}, Form: keysForm{Action: "/settings/keys", CSRF: "example-csrf", Binance: []keyView{key}, HasAnthropic: true, LiveAllowed: true, ServerIP: "203.0.113.7"}}, []string{"…5678", "real-money key", "203.0.113.7", "/settings/password"}},
		{"settings-empty", "settings", "/settings", &user, settingsPage{Tier: trial, Form: keysForm{Action: "/settings/keys"}}, []string{"No keys connected yet.", "Trial"}},
		{"admin", "admin", "/admin", &admin, adminPage{Rows: []adminRow{{User: user, Agents: 2, Running: 1}, {User: unverified}, {User: admin, Admin: true}}, Tiers: tiers.All}, []string{"id=\"accounts-table\"", "unconfirmed", "1 running of 2", "/tier", "Admin · no tier", "/admin/password"}},
		{"admin-empty", "admin", "/admin", &admin, adminPage{Tiers: tiers.All}, []string{"No accounts yet."}},
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
			if scenario.user != nil && s.isAdmin(*scenario.user) && strings.Contains(body, `href="/agents`) {
				t.Error("platform admins must not see trading navigation")
			}
		})
	}
}

func TestPublicPagesAndDesignAssets(t *testing.T) {
	s := &Server{Logf: t.Logf}
	h := s.Handler()
	for _, route := range []string{"/", "/login", "/signup", "/forgot", "/static/app.css", "/static/app.js", "/static/brand.svg", "/static/htmx.min.js", "/favicon.ico"} {
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
		{"sellall", api.Agent{ID: "agent-1", Name: "steady-btc"}, `"confirm": "yes"`},
		{"status", statusView{Agent: api.Agent{ID: "agent-1", Status: "stopped"}, Message: "Done: sell_all", ClearConfirm: true}, `id="confirm" hx-swap-oob`},
		{"tglink", newLinkView(notifier.Link{URL: "https://t.me/TestBot?start=code1", ExpiresAt: time.Now().Add(time.Minute)}), "Open in Telegram"},
		{"tglink", linkView{Problem: "Telegram is not reachable"}, "Try again"},
		{"activity-rows", activityRows{Newer: "/activity?after=7&agent=a"}, `hx-trigger="every 20s"`},
		{"activity-rows", activityRows{Events: eventViews([]api.Event{{Seq: 1, Type: "account.created", Payload: json.RawMessage(`{}`)}}), Start: true}, "Start of the record"},
		{"events", updatesView{Agent: "agent-1"}, "Nothing recorded yet."},
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
