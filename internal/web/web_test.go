package web

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/candylario/trader/api"

	"github.com/candylario/app/internal/mail"
	"github.com/candylario/app/internal/notifier"
	"github.com/candylario/app/internal/store"
)

// testDatabase creates an empty, migrated database on the server in
// TEST_DATABASE_URL, or skips the test.
func testDatabase(t *testing.T) string {
	t.Helper()
	base := os.Getenv("TEST_DATABASE_URL")
	if base == "" {
		t.Skip("set TEST_DATABASE_URL to run the app tests")
	}
	ctx := context.Background()
	admin, err := pgx.Connect(ctx, base)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close(ctx)
	name := "app_test_" + strings.ReplaceAll(uuid.NewString()[:8], "-", "")
	if _, err := admin.Exec(ctx, "create database "+name); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if c, err := pgx.Connect(context.Background(), base); err == nil {
			c.Exec(context.Background(), "drop database "+name+" with (force)")
			c.Close(context.Background())
		}
	})
	i := strings.LastIndex(base, "/")
	url := base[:i+1] + name
	if q := strings.Index(base[i:], "?"); q >= 0 {
		url += base[i+q:]
	}
	if err := store.Migrate(ctx, url); err != nil {
		t.Fatal(err)
	}
	if err := store.Migrate(ctx, url); err != nil {
		t.Fatalf("migrations run once: %v", err)
	}
	return url
}

// fakeEngine is the engine API with in-memory tenants, keys, agents and
// events, as openapi.yaml describes it.
type fakeEngine struct {
	mu       sync.Mutex
	token    string
	tenants  map[string]api.Tenant
	keys     map[string][]api.Key
	secrets  map[string]map[string]string
	agents   map[string][]*api.Agent
	configs  map[string]json.RawMessage
	statuses map[string]api.Status
	events   map[string][]api.Event
	started  []string
	commands []api.Command
}

func newFakeEngine() *fakeEngine {
	return &fakeEngine{token: "engine-token", tenants: map[string]api.Tenant{}, keys: map[string][]api.Key{}, secrets: map[string]map[string]string{},
		agents: map[string][]*api.Agent{}, configs: map[string]json.RawMessage{}, statuses: map[string]api.Status{}, events: map[string][]api.Event{}}
}

func (f *fakeEngine) refuse(w http.ResponseWriter, status int, code, msg string) {
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(map[string]any{"error": map[string]string{"code": code, "message": msg}})
}

func (f *fakeEngine) agent(tenant, id string) *api.Agent {
	for _, a := range f.agents[tenant] {
		if a.ID == id {
			return a
		}
	}
	return nil
}

func (f *fakeEngine) server(t *testing.T) *httptest.Server {
	mux := http.NewServeMux()
	reply := func(w http.ResponseWriter, status int, v any) {
		w.WriteHeader(status)
		json.NewEncoder(w).Encode(v)
	}
	mux.HandleFunc("GET /v1/health", func(w http.ResponseWriter, r *http.Request) { reply(w, 200, map[string]bool{"ok": true}) })
	mux.HandleFunc("DELETE /v1/tenants/{t}", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		t, ok := f.tenants[r.PathValue("t")]
		if !ok {
			reply(w, 404, map[string]any{"error": map[string]string{"code": "not_found", "message": "no such tenant"}})
			return
		}
		t.Status, t.Name = "closed", ""
		f.tenants[t.ID] = t
		reply(w, 200, api.Closed{Stopped: []api.Stopped{}})
	})
	mux.HandleFunc("POST /v1/tenants", func(w http.ResponseWriter, r *http.Request) {
		var in api.NewTenant
		json.NewDecoder(r.Body).Decode(&in)
		f.mu.Lock()
		defer f.mu.Unlock()
		if t, ok := f.tenants[in.ID]; ok {
			reply(w, 200, t)
			return
		}
		t := api.Tenant{ID: in.ID, Name: in.Name, Status: "active", Plan: in.Plan}
		f.tenants[in.ID] = t
		reply(w, 201, t)
	})
	mux.HandleFunc("GET /v1/tenants/{t}", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		t, ok := f.tenants[r.PathValue("t")]
		if !ok {
			f.refuse(w, 404, "not_found", "no such tenant")
			return
		}
		reply(w, 200, t)
	})
	mux.HandleFunc("PUT /v1/tenants/{t}/plan", func(w http.ResponseWriter, r *http.Request) {
		var p api.Plan
		json.NewDecoder(r.Body).Decode(&p)
		f.mu.Lock()
		defer f.mu.Unlock()
		t := f.tenants[r.PathValue("t")]
		t.Plan = p
		f.tenants[r.PathValue("t")] = t
		reply(w, 200, api.PlanChange{Plan: p, Stopped: []api.Stopped{}})
	})
	mux.HandleFunc("POST /v1/tenants/{t}/keys", func(w http.ResponseWriter, r *http.Request) {
		var in api.NewKey
		json.NewDecoder(r.Body).Decode(&in)
		var secret map[string]string
		json.Unmarshal(in.Secret, &secret)
		if strings.Contains(secret["key"], "bad") {
			f.refuse(w, 422, "refused", "the key was not accepted")
			return
		}
		f.mu.Lock()
		defer f.mu.Unlock()
		visible := secret["key"]
		k := api.Key{ID: uuid.NewString(), Kind: in.Kind, Label: in.Label, Hint: "…" + visible[max(0, len(visible)-4):], CreatedAt: time.Now()}
		f.keys[r.PathValue("t")] = append(f.keys[r.PathValue("t")], k)
		f.secrets[k.ID] = secret
		reply(w, 201, k)
	})
	mux.HandleFunc("GET /v1/tenants/{t}/keys", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		reply(w, 200, api.KeyList{Keys: append([]api.Key{}, f.keys[r.PathValue("t")]...)})
	})
	mux.HandleFunc("DELETE /v1/tenants/{t}/keys/{k}", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		keys := f.keys[r.PathValue("t")]
		for i := range keys {
			if keys[i].ID == r.PathValue("k") {
				keys[i].Revoked = true
				reply(w, 200, api.Revoked{Stopped: []api.Stopped{{AgentID: "x", Reason: "key revoked"}}})
				return
			}
		}
		f.refuse(w, 404, "not_found", "no such key")
	})
	mux.HandleFunc("POST /v1/tenants/{t}/agents", func(w http.ResponseWriter, r *http.Request) {
		var in api.NewAgent
		json.NewDecoder(r.Body).Decode(&in)
		f.mu.Lock()
		defer f.mu.Unlock()
		for _, a := range f.agents[r.PathValue("t")] {
			if a.Name == in.Name {
				f.refuse(w, 422, "refused", "an agent with this name exists")
				return
			}
		}
		a := &api.Agent{ID: uuid.NewString(), Name: in.Name, Mode: in.Mode, Status: "stopped", Version: 1, Keys: map[string]string{}}
		for _, id := range in.Keys {
			for _, k := range f.keys[r.PathValue("t")] {
				if k.ID == id {
					a.Keys[k.Kind] = id
				}
			}
		}
		f.agents[r.PathValue("t")] = append(f.agents[r.PathValue("t")], a)
		f.configs[a.ID] = in.Config
		reply(w, 201, a)
	})
	mux.HandleFunc("GET /v1/tenants/{t}/agents", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		out := api.AgentList{Agents: []api.Agent{}}
		for _, a := range f.agents[r.PathValue("t")] {
			out.Agents = append(out.Agents, *a)
		}
		reply(w, 200, out)
	})
	withAgent := func(fn func(w http.ResponseWriter, r *http.Request, a *api.Agent)) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			f.mu.Lock()
			defer f.mu.Unlock()
			a := f.agent(r.PathValue("t"), r.PathValue("a"))
			if a == nil {
				f.refuse(w, 404, "not_found", "no such agent")
				return
			}
			fn(w, r, a)
		}
	}
	mux.HandleFunc("GET /v1/tenants/{t}/agents/{a}", withAgent(func(w http.ResponseWriter, r *http.Request, a *api.Agent) { reply(w, 200, a) }))
	mux.HandleFunc("POST /v1/tenants/{t}/agents/{a}/start", withAgent(func(w http.ResponseWriter, r *http.Request, a *api.Agent) {
		a.Status, a.Running = "active", true
		f.started = append(f.started, a.ID)
		reply(w, 200, a)
	}))
	mux.HandleFunc("POST /v1/tenants/{t}/agents/{a}/stop", withAgent(func(w http.ResponseWriter, r *http.Request, a *api.Agent) {
		a.Status, a.Running = "stopped", false
		reply(w, 200, a)
	}))
	mux.HandleFunc("GET /v1/tenants/{t}/agents/{a}/status", withAgent(func(w http.ResponseWriter, r *http.Request, a *api.Agent) {
		st, ok := f.statuses[a.ID]
		if !ok {
			f.refuse(w, 422, "no_account", "the agent has no account yet")
			return
		}
		reply(w, 200, st)
	}))
	mux.HandleFunc("POST /v1/tenants/{t}/agents/{a}/commands", withAgent(func(w http.ResponseWriter, r *http.Request, a *api.Agent) {
		var c api.Command
		json.NewDecoder(r.Body).Decode(&c)
		f.commands = append(f.commands, c)
		reply(w, 200, api.CommandResult{OK: true, Message: "Done: " + c.Command})
	}))
	mux.HandleFunc("GET /v1/tenants/{t}/agents/{a}/events", withAgent(func(w http.ResponseWriter, r *http.Request, a *api.Agent) {
		after, _ := strconv.ParseInt(r.URL.Query().Get("after"), 10, 64)
		limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
		types := r.URL.Query().Get("type")
		out := api.EventList{Events: []api.Event{}}
		for _, e := range f.events[a.ID] {
			if e.Seq > after && (types == "" || strings.Contains(","+types+",", ","+e.Type+",")) && len(out.Events) < limit {
				out.Events = append(out.Events, e)
			}
		}
		reply(w, 200, out)
	}))
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+f.token {
			f.refuse(w, 401, "unauthorized", "bad token")
			return
		}
		w.Header().Set("Content-Type", "application/json")
		mux.ServeHTTP(w, r)
	}))
}

type fakeNotifier struct {
	mu    sync.Mutex
	chats []notifier.Chat
	links int
}

func (f *fakeNotifier) server(t *testing.T) *httptest.Server {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/tenants/{t}/links", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		f.links++
		w.WriteHeader(201)
		json.NewEncoder(w).Encode(map[string]any{"url": "https://t.me/TestBot?start=code" + strconv.Itoa(f.links), "expires_at": time.Now().Add(10 * time.Minute)})
	})
	mux.HandleFunc("GET /v1/health", func(w http.ResponseWriter, r *http.Request) { json.NewEncoder(w).Encode(map[string]bool{"ok": true}) })
	mux.HandleFunc("GET /v1/tenants/{t}/chats", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		json.NewEncoder(w).Encode(map[string]any{"chats": f.chats})
	})
	mux.HandleFunc("DELETE /v1/tenants/{t}/chats/{c}", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		id, _ := strconv.ParseInt(r.PathValue("c"), 10, 64)
		for i, c := range f.chats {
			if c.ChatID == id {
				f.chats = append(f.chats[:i], f.chats[i+1:]...)
				json.NewEncoder(w).Encode(map[string]bool{"ok": true})
				return
			}
		}
		w.WriteHeader(404)
		json.NewEncoder(w).Encode(map[string]any{"error": map[string]string{"code": "not_found", "message": "no such chat"}})
	})
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer notifier-token" {
			w.WriteHeader(401)
			return
		}
		mux.ServeHTTP(w, r)
	}))
}

// fakeMail keeps the emails the app sends, or fails them all.
type fakeMail struct {
	mu   sync.Mutex
	sent []mail.Message
	fail bool
}

func (f *fakeMail) Send(_ context.Context, m mail.Message) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.fail {
		return errors.New("mail is down")
	}
	f.sent = append(f.sent, m)
	return nil
}

func (f *fakeMail) to(addr string) []mail.Message {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []mail.Message
	for _, m := range f.sent {
		if m.To == addr {
			out = append(out, m)
		}
	}
	return out
}

var linkPattern = regexp.MustCompile(`http://app\.test(/\S+)`)

// link is the path of the link in the last email to the address.
func (f *fakeMail) link(t *testing.T, addr string) string {
	t.Helper()
	sent := f.to(addr)
	if len(sent) > 0 {
		if m := linkPattern.FindStringSubmatch(sent[len(sent)-1].Text); m != nil {
			return m[1]
		}
	}
	t.Fatalf("no link mailed to %s", addr)
	return ""
}

type rig struct {
	t      *testing.T
	engine *fakeEngine
	notif  *fakeNotifier
	mail   *fakeMail
	srv    *Server
	app    *httptest.Server
	client *http.Client
}

func newRig(t *testing.T) *rig {
	t.Helper()
	url := testDatabase(t)
	st, err := store.Open(context.Background(), url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(st.Close)
	eng := newFakeEngine()
	es := eng.server(t)
	t.Cleanup(es.Close)
	nf := &fakeNotifier{}
	ns := nf.server(t)
	t.Cleanup(ns.Close)
	fm := &fakeMail{}
	srv := &Server{Store: st, Engine: &api.Client{BaseURL: es.URL, Token: eng.token}, Notifier: &notifier.Client{BaseURL: ns.URL, Token: "notifier-token"},
		BaseURL: "http://app.test", Mail: fm, MailjetWebhookSecret: "hook-secret", Logf: t.Logf}
	app := httptest.NewServer(srv.Handler())
	t.Cleanup(app.Close)
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	return &rig{t: t, engine: eng, notif: nf, mail: fm, srv: srv, app: app, client: client}
}

func (r *rig) get(path string) (*http.Response, string) {
	r.t.Helper()
	resp, err := r.client.Get(r.app.URL + path)
	if err != nil {
		r.t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	return resp, string(body)
}

func (r *rig) post(path string, form url.Values, headers ...string) (*http.Response, string) {
	r.t.Helper()
	req, _ := http.NewRequest(http.MethodPost, r.app.URL+path, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	for i := 0; i+1 < len(headers); i += 2 {
		req.Header.Set(headers[i], headers[i+1])
	}
	resp, err := r.client.Do(req)
	if err != nil {
		r.t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	return resp, string(body)
}

var csrfPattern = regexp.MustCompile(`name="csrf" value="([^"]+)"`)

// signup registers, confirms the address with the emailed link and logs
// in, and returns the session's CSRF token.
func (r *rig) signup(email string) string {
	r.t.Helper()
	resp, body := r.post("/signup", url.Values{"email": {email}, "password": {"correct horse battery"}})
	if resp.StatusCode != 303 || resp.Header.Get("Location") != "/verify-email" {
		r.t.Fatalf("signup: %d %s %s", resp.StatusCode, resp.Header.Get("Location"), body)
	}
	resp, body = r.get(r.mail.link(r.t, normalizeEmail(email)))
	next, page := resp.Header.Get("Location"), "/settings"
	if next == "/admin" {
		page = "/admin"
	} else if resp.StatusCode != 303 || next != "/setup" {
		r.t.Fatalf("confirm: %d %s %s", resp.StatusCode, next, body)
	}
	_, body = r.get(page)
	m := csrfPattern.FindStringSubmatch(body)
	if m == nil {
		r.t.Fatalf("no csrf token in settings page: %s", body)
	}
	return m[1]
}

func (r *rig) tenantOf(email string) string {
	r.t.Helper()
	u, err := r.srv.Store.UserByEmail(context.Background(), email)
	if err != nil {
		r.t.Fatal(err)
	}
	return u.TenantID.String()
}

func (r *rig) addKeys(csrf string) {
	r.t.Helper()
	resp, body := r.post("/setup/keys", url.Values{"csrf": {csrf}, "anthropic_key": {"sk-ant-good-1234"}, "binance_key": {"bkey-5678"}, "binance_secret": {"bsecret"}, "binance_mode": {"testnet"}})
	if resp.StatusCode != 303 || resp.Header.Get("Location") != "/setup" {
		r.t.Fatalf("keys: %d %s", resp.StatusCode, body)
	}
}

func (r *rig) createAgent(csrf string) string {
	r.t.Helper()
	resp, body := r.post("/setup/agent", url.Values{"csrf": {csrf}, "name": {"btc-eth"}, "symbols": {"BTCUSDT", "ETHUSDT"}, "capital": {"100"}, "interval": {"4"}})
	if resp.StatusCode != 303 || !strings.HasPrefix(resp.Header.Get("Location"), "/agents/") {
		r.t.Fatalf("create agent: %d %s %s", resp.StatusCode, resp.Header.Get("Location"), body)
	}
	return strings.TrimPrefix(resp.Header.Get("Location"), "/agents/")
}

func TestSignupCreatesTheTenantOnTrialAndLogsIn(t *testing.T) {
	r := newRig(t)
	r.signup("Ada@Example.com")
	tenant := r.tenantOf("ada@example.com")
	r.engine.mu.Lock()
	tn, ok := r.engine.tenants[tenant]
	r.engine.mu.Unlock()
	if !ok || tn.Plan.Live || tn.Plan.MaxAgents != 1 || len(tn.Plan.Symbols) != 2 || tn.Name != "ada@example.com" {
		t.Fatalf("tenant in engine: %+v %v", tn, ok)
	}
	resp, _ := r.get("/setup")
	if resp.StatusCode != 303 || resp.Header.Get("Location") != "/setup/keys" {
		t.Fatalf("setup without keys: %d %s", resp.StatusCode, resp.Header.Get("Location"))
	}
	resp, body := r.get("/agents")
	if resp.StatusCode != 200 || !strings.Contains(body, "Set up your first agent") {
		t.Fatalf("agents page: %d", resp.StatusCode)
	}
	resp, body = r.post("/signup", url.Values{"email": {"ada@example.com"}, "password": {"another password"}})
	if resp.StatusCode != 422 || !strings.Contains(body, "An account with this email exists") {
		t.Fatalf("second signup: %d", resp.StatusCode)
	}
	resp, body = r.post("/signup", url.Values{"email": {"not an email"}, "password": {"short"}})
	if resp.StatusCode != 422 || !strings.Contains(body, "valid email") {
		t.Fatalf("bad email: %d", resp.StatusCode)
	}
}

func TestLoginRejectsWrongPasswordsAndTooManyTries(t *testing.T) {
	r := newRig(t)
	r.signup("bob@example.com")
	jar, _ := cookiejar.New(nil)
	r.client.Jar = jar
	resp, body := r.post("/login", url.Values{"email": {"bob@example.com"}, "password": {"wrong password!"}})
	if resp.StatusCode != 401 || !strings.Contains(body, "Wrong email or password") {
		t.Fatalf("wrong password: %d", resp.StatusCode)
	}
	resp, _ = r.post("/login", url.Values{"email": {"nobody@example.com"}, "password": {"wrong password!"}})
	if resp.StatusCode != 401 {
		t.Fatalf("unknown email: %d", resp.StatusCode)
	}
	for i := 0; i < 8; i++ {
		r.post("/login", url.Values{"email": {"bob@example.com"}, "password": {"wrong password!"}})
	}
	resp, _ = r.post("/login", url.Values{"email": {"bob@example.com"}, "password": {"correct horse battery"}})
	if resp.StatusCode != 429 {
		t.Fatalf("after 10 failures: %d, want 429", resp.StatusCode)
	}
	r.srv.logins = newLimiter(10, time.Minute)
	resp, _ = r.post("/login", url.Values{"email": {"bob@example.com"}, "password": {"correct horse battery"}})
	if resp.StatusCode != 303 || resp.Header.Get("Location") != "/agents" {
		t.Fatalf("right password: %d %s", resp.StatusCode, resp.Header.Get("Location"))
	}
	resp, _ = r.post("/login", url.Values{"email": {"bob@example.com"}, "password": {"correct horse battery"}}, "Sec-Fetch-Site", "cross-site")
	if resp.StatusCode != 403 {
		t.Fatalf("cross-site login: %d", resp.StatusCode)
	}
}

func TestChangesNeedTheSessionsToken(t *testing.T) {
	r := newRig(t)
	csrf := r.signup("cy@example.com")
	resp, _ := r.post("/settings/password", url.Values{"current": {"correct horse battery"}, "password": {"a brand new password"}, "again": {"a brand new password"}})
	if resp.StatusCode != 403 {
		t.Fatalf("without token: %d", resp.StatusCode)
	}
	resp, _ = r.post("/settings/password", url.Values{"current": {"correct horse battery"}, "password": {"a brand new password"}, "again": {"a brand new password"}}, "X-CSRF-Token", csrf)
	if resp.StatusCode != 303 {
		t.Fatalf("with token: %d", resp.StatusCode)
	}
	resp, _ = r.get("/agents")
	if resp.StatusCode != 200 {
		t.Fatalf("still logged in: %d", resp.StatusCode)
	}
	if sent := r.mail.to("cy@example.com"); sent[len(sent)-1].Subject != "Your Candylario password was changed" {
		t.Fatalf("no notice of the change: %+v", sent)
	}
	jar, _ := cookiejar.New(nil)
	r.client.Jar = jar
	resp, _ = r.post("/login", url.Values{"email": {"cy@example.com"}, "password": {"a brand new password"}})
	if resp.StatusCode != 303 {
		t.Fatalf("new password: %d", resp.StatusCode)
	}
	resp, _ = r.get("/agents")
	if resp.StatusCode != 200 {
		t.Fatalf("logged in with new password: %d", resp.StatusCode)
	}
}

func TestKeysGoStraightToTheEngine(t *testing.T) {
	r := newRig(t)
	csrf := r.signup("dee@example.com")
	resp, body := r.post("/setup/keys", url.Values{"csrf": {csrf}, "anthropic_key": {"sk-ant-bad"}, "binance_key": {"bkey"}, "binance_secret": {"bsec"}, "binance_mode": {"live"}})
	if resp.StatusCode != 422 || !strings.Contains(body, "the key was not accepted") || !strings.Contains(body, "does not include real-money trading") {
		t.Fatalf("refused keys: %d %s", resp.StatusCode, body)
	}
	r.addKeys(csrf)
	tenant := r.tenantOf("dee@example.com")
	r.engine.mu.Lock()
	keys := r.engine.keys[tenant]
	var kinds []string
	for _, k := range keys {
		kinds = append(kinds, k.Kind+"/"+k.Label)
	}
	secret := r.engine.secrets[keys[1].ID]
	r.engine.mu.Unlock()
	if strings.Join(kinds, " ") != "anthropic/default binance/testnet" {
		t.Fatalf("keys stored: %v", kinds)
	}
	if secret["key"] != "bkey-5678" || secret["secret"] != "bsecret" {
		t.Fatalf("binance secret forwarded: %v", secret)
	}
	resp, _ = r.get("/setup")
	if resp.Header.Get("Location") != "/setup/agent" {
		t.Fatalf("setup with keys: %s", resp.Header.Get("Location"))
	}
	_, body = r.get("/settings")
	if !strings.Contains(body, "…1234") || !strings.Contains(body, "…5678") || strings.Contains(body, "bsecret") {
		t.Fatalf("settings shows hints only: %s", body)
	}
}

func TestCreatingAnAgentBuildsItsConfigAndStartsIt(t *testing.T) {
	r := newRig(t)
	csrf := r.signup("eve@example.com")
	r.addKeys(csrf)
	resp, body := r.post("/setup/agent", url.Values{"csrf": {csrf}, "name": {"btc-eth"}, "symbols": {"SOLUSDT"}, "capital": {"100"}, "interval": {"4"}})
	if resp.StatusCode != 422 || !strings.Contains(body, "from your tier") {
		t.Fatalf("coin outside the tier: %d", resp.StatusCode)
	}
	resp, body = r.post("/setup/agent", url.Values{"csrf": {csrf}, "name": {"btc-eth"}, "symbols": {"BTCUSDT"}, "capital": {"100"}, "interval": {"1"}})
	if resp.StatusCode != 422 || !strings.Contains(body, "interval") {
		t.Fatalf("interval below the tier: %d", resp.StatusCode)
	}
	id := r.createAgent(csrf)
	tenant := r.tenantOf("eve@example.com")
	r.engine.mu.Lock()
	a := r.engine.agent(tenant, id)
	var cfg map[string]any
	json.Unmarshal(r.engine.configs[id], &cfg)
	started := append([]string{}, r.engine.started...)
	keys := r.engine.keys[tenant]
	r.engine.mu.Unlock()
	if a == nil || a.Mode != "testnet" || a.Keys["anthropic"] != keys[0].ID || a.Keys["binance"] != keys[1].ID {
		t.Fatalf("agent: %+v", a)
	}
	if cfg["capital_usd"] != 100.0 || cfg["decision_interval_hours"] != 4.0 || cfg["max_event_decisions_per_day"] != 4.0 || fmt.Sprint(cfg["symbols"]) != "[BTCUSDT ETHUSDT]" || cfg["name"] != "btc-eth" {
		t.Fatalf("config: %v", cfg)
	}
	if len(started) != 1 || started[0] != id {
		t.Fatalf("started: %v", started)
	}
	resp, body = r.get("/agents/" + id)
	if resp.StatusCode != 200 || !strings.Contains(body, "testnet") || !strings.Contains(body, "Not started yet") {
		t.Fatalf("agent page before its account exists: %d %s", resp.StatusCode, body)
	}
	resp, body = r.post("/setup/agent", url.Values{"csrf": {csrf}, "name": {"btc-eth"}, "symbols": {"BTCUSDT"}, "capital": {"100"}, "interval": {"4"}})
	if resp.StatusCode != 422 || !strings.Contains(body, "an agent with this name exists") {
		t.Fatalf("engine refusal shown: %d %s", resp.StatusCode, body)
	}
}

func TestAgentPageShowsTheAccountAndTakesCommands(t *testing.T) {
	r := newRig(t)
	csrf := r.signup("fay@example.com")
	r.addKeys(csrf)
	id := r.createAgent(csrf)
	tenant := r.tenantOf("fay@example.com")
	next := time.Now().Add(2 * time.Hour)
	r.engine.mu.Lock()
	r.engine.statuses[id] = api.Status{AgentID: id, Name: "btc-eth", Mode: "testnet", Running: true, Trading: api.TradingOn, Capital: "100", Equity: "101.5", BuyAndHold: "100.2",
		Cash: "55.5", Fees: "0.1", Positions: []api.Position{{Symbol: "BTCUSDT", Coin: "BTC", Qty: "0.0004", Value: "46", Weight: 0.45}},
		LastDecision: &api.Decision{ID: 3, At: time.Now(), Kind: "scheduled", Summary: "Quiet weekend <b>market</b>", Ratings: map[string]api.Rating{"BTCUSDT": {Rating: "strong_buy", Confidence: "medium", Rationale: "ETF inflows"}}, CostUSD: 0.07},
		NextDecision: &next, Trades: 2, GradedCalls: 4, RightCalls: 3, AISpend: &api.AISpend{TodayUSD: 0.2, MonthUSD: 1.1, DailyLimitUSD: 5, MonthlyLimitUSD: 100}, At: time.Now(), Message: "ok"}
	for i := 1; i <= 3; i++ {
		r.engine.events[id] = append(r.engine.events[id], api.Event{Seq: int64(i), ID: uuid.NewString(), Type: "equity.hourly", At: time.Now().Add(time.Duration(i-3) * time.Hour),
			Payload: json.RawMessage(fmt.Sprintf(`{"equity":"%d","buy_and_hold":"100"}`, 99+i))})
	}
	r.engine.events[id] = append(r.engine.events[id], api.Event{Seq: 4, ID: uuid.NewString(), Type: "notice", At: time.Now(), Payload: json.RawMessage(`{"text":"🧪 Bought BTC <script>"}`)})
	r.engine.mu.Unlock()

	resp, body := r.get("/agents/" + id)
	for _, want := range []string{"101.50", "100.20", "strong buy", "ETF inflows", "Quiet weekend &lt;b&gt;market&lt;/b&gt;", "3 of 4 graded calls right", "🧪 Bought BTC &lt;script&gt;", "<svg", "Sell everything"} {
		if !strings.Contains(body, want) {
			t.Errorf("agent page lacks %q", want)
		}
	}
	if resp.StatusCode != 200 || strings.Contains(body, "<script>") && !strings.Contains(body, `<script src="/static/htmx.min.js"`) {
		t.Fatalf("agent page: %d", resp.StatusCode)
	}
	for _, p := range []string{"/status", "/events", "/chart"} {
		if resp, _ := r.get("/agents/" + id + p); resp.StatusCode != 200 {
			t.Errorf("%s: %d", p, resp.StatusCode)
		}
	}
	resp, body = r.post("/agents/"+id+"/commands", url.Values{"command": {"sell_all"}}, "X-CSRF-Token", csrf)
	if resp.StatusCode != 400 || !strings.Contains(body, "Confirm") {
		t.Fatalf("sell all without confirm: %d", resp.StatusCode)
	}
	resp, body = r.post("/agents/"+id+"/commands", url.Values{"command": {"sell_all"}, "confirm": {"yes"}}, "X-CSRF-Token", csrf)
	if resp.StatusCode != 200 || !strings.Contains(body, "Done: sell_all") || !strings.Contains(body, `id="confirm" hx-swap-oob`) {
		t.Fatalf("sell all confirmed: %d %s", resp.StatusCode, body)
	}
	r.post("/agents/"+id+"/commands", url.Values{"command": {"decide"}}, "X-CSRF-Token", csrf)
	resp, _ = r.post("/agents/"+id+"/commands", url.Values{"command": {"explode"}}, "X-CSRF-Token", csrf)
	if resp.StatusCode != 400 {
		t.Fatalf("unknown command: %d", resp.StatusCode)
	}
	r.post("/agents/"+id+"/stop", nil, "X-CSRF-Token", csrf)
	r.engine.mu.Lock()
	var names []string
	for _, c := range r.engine.commands {
		names = append(names, c.Command+"/"+c.Via)
	}
	a := r.engine.agent(tenant, id)
	r.engine.mu.Unlock()
	if strings.Join(names, " ") != "sell_all/app decide/app" || a.Status != "stopped" {
		t.Fatalf("engine saw %v, agent %s", names, a.Status)
	}
	resp, _ = r.get("/agents/" + uuid.NewString())
	if resp.StatusCode != 404 {
		t.Fatalf("someone else's agent: %d", resp.StatusCode)
	}
}

func TestTelegramLinksAndChats(t *testing.T) {
	r := newRig(t)
	csrf := r.signup("gus@example.com")
	resp, body := r.post("/telegram/links", nil, "X-CSRF-Token", csrf)
	if resp.StatusCode != 200 || !strings.Contains(body, "https://t.me/TestBot?start=code1") || !strings.Contains(body, `src="data:image/png;base64,`) ||
		!strings.Contains(body, "https://web.telegram.org/k/#?tgaddr=tg%3A%2F%2Fresolve%3Fdomain%3DTestBot%26start%3Dcode1") || !strings.Contains(body, "<code>/start code1</code> to @TestBot") {
		t.Fatalf("link: %d %s", resp.StatusCode, body)
	}
	r.notif.mu.Lock()
	r.notif.chats = []notifier.Chat{{ChatID: 42, Name: "@gus", LinkedAt: time.Now()}}
	r.notif.mu.Unlock()
	_, body = r.get("/telegram")
	if !strings.Contains(body, "@gus") || !strings.Contains(body, "/telegram/chats/42/disconnect") {
		t.Fatalf("chats: %s", body)
	}
	resp, _ = r.post("/telegram/chats/42/disconnect", url.Values{"csrf": {csrf}})
	if resp.StatusCode != 303 {
		t.Fatalf("disconnect: %d", resp.StatusCode)
	}
	r.notif.mu.Lock()
	left := len(r.notif.chats)
	r.notif.mu.Unlock()
	if left != 0 {
		t.Fatal("chat not disconnected")
	}
	r.srv.Notifier.BaseURL = "http://127.0.0.1:1"
	resp, body = r.get("/telegram")
	if resp.StatusCode != 200 || !strings.Contains(body, "not reachable") {
		t.Fatalf("notifier down: %d %s", resp.StatusCode, body)
	}
	_, body = r.post("/telegram/links", nil, "X-CSRF-Token", csrf)
	if !strings.Contains(body, "not reachable") {
		t.Fatalf("link with notifier down: %s", body)
	}
}

func TestSettingsRevokeAKey(t *testing.T) {
	r := newRig(t)
	csrf := r.signup("hal@example.com")
	r.addKeys(csrf)
	tenant := r.tenantOf("hal@example.com")
	r.engine.mu.Lock()
	key := r.engine.keys[tenant][1].ID
	r.engine.mu.Unlock()
	resp, _ := r.post("/settings/keys/"+key+"/revoke", url.Values{"csrf": {csrf}})
	if resp.StatusCode != 303 {
		t.Fatalf("revoke: %d", resp.StatusCode)
	}
	_, body := r.get("/settings")
	if !strings.Contains(body, "Key revoked; 1 agent(s) stopped") || strings.Contains(body, "…5678") {
		t.Fatalf("after revoke: %s", body)
	}
}

func TestLogoutEndsTheSession(t *testing.T) {
	r := newRig(t)
	csrf := r.signup("ivy@example.com")
	resp, _ := r.post("/logout", url.Values{"csrf": {csrf}})
	if resp.StatusCode != 303 {
		t.Fatalf("logout: %d", resp.StatusCode)
	}
	resp, _ = r.get("/agents")
	if resp.StatusCode != 303 || resp.Header.Get("Location") != "/login" {
		t.Fatalf("after logout: %d", resp.StatusCode)
	}
	req, _ := http.NewRequest(http.MethodGet, r.app.URL+"/agents/x/status", nil)
	req.Header.Set("HX-Request", "true")
	resp, err := r.client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != 401 || resp.Header.Get("HX-Redirect") != "/login" {
		t.Fatalf("htmx after logout: %d", resp.StatusCode)
	}
}

func TestPagesCarryTheFavicon(t *testing.T) {
	r := newRig(t)
	for _, p := range []string{"/favicon.ico", "/static/apple-touch-icon.png"} {
		resp, _ := r.get(p)
		if resp.StatusCode != 200 || !strings.HasPrefix(resp.Header.Get("Content-Type"), "image/") {
			t.Fatalf("%s: %d %s", p, resp.StatusCode, resp.Header.Get("Content-Type"))
		}
	}
	if _, body := r.get("/"); !strings.Contains(body, `<link rel="icon" href="/favicon.ico"`) {
		t.Fatal("home page has no favicon link")
	}
}

func (r *rig) tenants() int {
	r.engine.mu.Lock()
	defer r.engine.mu.Unlock()
	return len(r.engine.tenants)
}

func TestSignupNeedsTheEmailConfirmed(t *testing.T) {
	r := newRig(t)
	resp, _ := r.post("/signup", url.Values{"email": {"eve@example.com"}, "password": {"correct horse battery"}})
	if resp.StatusCode != 303 || resp.Header.Get("Location") != "/verify-email" {
		t.Fatalf("signup: %d %s", resp.StatusCode, resp.Header.Get("Location"))
	}
	resp, _ = r.get("/agents")
	if resp.StatusCode != 303 || resp.Header.Get("Location") != "/verify-email" {
		t.Fatalf("agents before confirming: %d %s", resp.StatusCode, resp.Header.Get("Location"))
	}
	if n := r.tenants(); n != 0 {
		t.Fatalf("%d engine tenants before confirming", n)
	}
	resp, body := r.get("/verify-email")
	m := csrfPattern.FindStringSubmatch(body)
	if resp.StatusCode != 200 || !strings.Contains(body, "eve@example.com") || m == nil {
		t.Fatalf("notice: %d %s", resp.StatusCode, body)
	}
	first := r.mail.link(t, "eve@example.com")
	resp, _ = r.post("/verify-email", url.Values{"csrf": {m[1]}})
	second := r.mail.link(t, "eve@example.com")
	if resp.StatusCode != 303 || first == second {
		t.Fatalf("resend: %d, new link %v", resp.StatusCode, first != second)
	}
	if resp, _ = r.get(first); resp.StatusCode != 410 {
		t.Fatalf("replaced link: %d", resp.StatusCode)
	}
	if resp, _ = r.get(second); resp.StatusCode != 303 || resp.Header.Get("Location") != "/setup" {
		t.Fatalf("link: %d %s", resp.StatusCode, resp.Header.Get("Location"))
	}
	if resp, _ = r.get(second); resp.StatusCode != 303 || resp.Header.Get("Location") != "/agents" {
		t.Fatalf("link again while logged in: %d %s", resp.StatusCode, resp.Header.Get("Location"))
	}
	if resp, _ = r.get("/agents"); resp.StatusCode != 200 || r.tenants() != 1 {
		t.Fatalf("agents after confirming: %d, %d tenants", resp.StatusCode, r.tenants())
	}
}

func TestAMistypedAddressCanBeChangedBeforeConfirming(t *testing.T) {
	r := newRig(t)
	if _, err := r.srv.Store.CreateUser(context.Background(), "gus@example.com", "x", "trial"); err != nil {
		t.Fatal(err)
	}
	r.post("/signup", url.Values{"email": {"fran@exmaple.com"}, "password": {"correct horse battery"}})
	typo := r.mail.link(t, "fran@exmaple.com")
	_, body := r.get("/verify-email")
	csrf := csrfPattern.FindStringSubmatch(body)[1]
	resp, body := r.post("/verify-email/address", url.Values{"csrf": {csrf}, "email": {"gus@example.com"}})
	if resp.StatusCode != 422 || !strings.Contains(body, "An account with this email exists") {
		t.Fatalf("taken address: %d", resp.StatusCode)
	}
	resp, _ = r.post("/verify-email/address", url.Values{"csrf": {csrf}, "email": {"Fran@Example.com"}})
	if resp.StatusCode != 303 {
		t.Fatalf("change: %d", resp.StatusCode)
	}
	if resp, _ = r.get(typo); resp.StatusCode != 410 {
		t.Fatalf("link to the old address: %d", resp.StatusCode)
	}
	if resp, _ = r.get(r.mail.link(t, "fran@example.com")); resp.StatusCode != 303 || resp.Header.Get("Location") != "/setup" {
		t.Fatalf("link to the new address: %d %s", resp.StatusCode, resp.Header.Get("Location"))
	}
}

func TestForgottenPasswordIsResetByEmail(t *testing.T) {
	r := newRig(t)
	r.signup("hal@example.com")
	first := r.client.Jar
	jar, _ := cookiejar.New(nil)
	r.client.Jar = jar
	resp, body := r.post("/forgot", url.Values{"email": {"nobody@example.com"}})
	if resp.StatusCode != 200 || !strings.Contains(body, "If an account exists") || len(r.mail.to("nobody@example.com")) != 0 {
		t.Fatalf("unknown address: %d", resp.StatusCode)
	}
	resp, body = r.post("/forgot", url.Values{"email": {"HAL@example.com"}})
	if resp.StatusCode != 200 || !strings.Contains(body, "If an account exists") {
		t.Fatalf("forgot: %d", resp.StatusCode)
	}
	link := r.mail.link(t, "hal@example.com")
	token := strings.TrimPrefix(link, "/reset?token=")
	if resp, body = r.get(link); resp.StatusCode != 200 || !strings.Contains(body, "For hal@example.com") {
		t.Fatalf("reset form: %d", resp.StatusCode)
	}
	if resp, body = r.post("/reset", url.Values{"token": {token}, "password": {"a brand new password"}, "again": {"another password"}}); resp.StatusCode != 422 || !strings.Contains(body, "differ") {
		t.Fatalf("passwords differ: %d", resp.StatusCode)
	}
	if resp, _ = r.post("/reset", url.Values{"token": {token}, "password": {"short"}, "again": {"short"}}); resp.StatusCode != 422 {
		t.Fatalf("short password: %d", resp.StatusCode)
	}
	resp, _ = r.post("/reset", url.Values{"token": {token}, "password": {"a brand new password"}, "again": {"a brand new password"}})
	if resp.StatusCode != 303 || resp.Header.Get("Location") != "/agents" {
		t.Fatalf("reset: %d %s", resp.StatusCode, resp.Header.Get("Location"))
	}
	if resp, _ = r.get("/agents"); resp.StatusCode != 200 {
		t.Fatalf("logged in after the reset: %d", resp.StatusCode)
	}
	if resp, _ = r.get(link); resp.StatusCode != 410 {
		t.Fatalf("link used twice: %d", resp.StatusCode)
	}
	r.client.Jar = first
	if resp, _ = r.get("/agents"); resp.StatusCode != 303 || resp.Header.Get("Location") != "/login" {
		t.Fatalf("other browser still logged in: %d", resp.StatusCode)
	}
	if resp, _ = r.post("/login", url.Values{"email": {"hal@example.com"}, "password": {"correct horse battery"}}); resp.StatusCode != 401 {
		t.Fatalf("old password: %d", resp.StatusCode)
	}
	if resp, _ = r.post("/login", url.Values{"email": {"hal@example.com"}, "password": {"a brand new password"}}); resp.StatusCode != 303 {
		t.Fatalf("new password: %d", resp.StatusCode)
	}
	if resp, _ = r.post("/reset", url.Values{"token": {token}, "password": {"x"}, "again": {"y"}}, "Sec-Fetch-Site", "cross-site"); resp.StatusCode != 403 {
		t.Fatalf("cross-site reset: %d", resp.StatusCode)
	}
}

func TestMailjetReportsMarkAddressesThatFail(t *testing.T) {
	r := newRig(t)
	r.post("/signup", url.Values{"email": {"ivy@example.com"}, "password": {"correct horse battery"}})
	hook := func(user, pass, body string) int {
		req, _ := http.NewRequest(http.MethodPost, r.app.URL+"/webhooks/mailjet", strings.NewReader(body))
		req.SetBasicAuth(user, pass)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		return resp.StatusCode
	}
	bounce := `{"event":"bounce","time":1,"MessageID":13792286917004336,"email":"Ivy@example.com","hard_bounce":true,"error_related_to":"recipient","error":"user unknown"}`
	if code := hook("mailjet", "wrong", bounce); code != 401 {
		t.Fatalf("wrong secret: %d", code)
	}
	if code := hook("mailjet", "hook-secret", bounce); code != 200 {
		t.Fatalf("bounce: %d", code)
	}
	if _, body := r.get("/verify-email"); !strings.Contains(body, "Mail to this address failed: bounced: user unknown") {
		t.Fatalf("notice without the bounce: %s", body)
	}
	if code := hook("mailjet", "hook-secret", `[{"event":"open","email":"ivy@example.com"},{"event":"blocked","email":"ivy@example.com","error":"preblocked"}]`); code != 200 {
		t.Fatalf("grouped: %d", code)
	}
	if code := hook("mailjet", "hook-secret", `{"event":"bounce","email":"ivy@example.com","hard_bounce":false,"error":"quota exceeded"}`); code != 200 {
		t.Fatalf("soft bounce: %d", code)
	}
	if _, body := r.get("/verify-email"); !strings.Contains(body, "blocked: preblocked") {
		t.Fatalf("notice: %s", body)
	}
	r.srv.MailjetWebhookSecret = ""
	if code := hook("mailjet", "", bounce); code != 404 {
		t.Fatalf("webhook off: %d", code)
	}
}

func TestSignupSaysWhenTheEmailCouldNotBeSent(t *testing.T) {
	r := newRig(t)
	r.mail.fail = true
	r.post("/signup", url.Values{"email": {"jo@example.com"}, "password": {"correct horse battery"}})
	if _, body := r.get("/verify-email"); !strings.Contains(body, "could not be sent") {
		t.Fatalf("notice: %s", body)
	}
}

func TestAdminSeesEveryAccountAndSetsTiers(t *testing.T) {
	r := newRig(t)
	r.srv.Admins = []string{"boss@example.com"}
	ctx := context.Background()
	kim, err := r.srv.Store.CreateUser(ctx, "kim@example.com", "x", "trial")
	if err != nil {
		t.Fatal(err)
	}
	r.signup("lee@example.com")
	jar, _ := cookiejar.New(nil)
	r.client.Jar = jar
	csrf := r.signup("boss@example.com")
	resp, body := r.get("/admin")
	if resp.StatusCode != 200 || !strings.Contains(body, "kim@example.com") || !strings.Contains(body, "lee@example.com") || !strings.Contains(body, "unconfirmed") || !strings.Contains(body, `href="/admin"`) {
		t.Fatalf("admin page: %d %s", resp.StatusCode, body)
	}
	resp, _ = r.post("/admin/users/"+kim.ID.String()+"/tier", url.Values{"csrf": {csrf}, "tier": {"pro"}})
	if u, _ := r.srv.Store.UserByEmail(ctx, "kim@example.com"); resp.StatusCode != 303 || u.Tier != "pro" {
		t.Fatalf("tier of an account without a tenant: %d %s", resp.StatusCode, u.Tier)
	}
	lee, _ := r.srv.Store.UserByEmail(ctx, "lee@example.com")
	r.post("/admin/users/"+lee.ID.String()+"/tier", url.Values{"csrf": {csrf}, "tier": {"standard"}})
	r.engine.mu.Lock()
	live := r.engine.tenants[lee.TenantID.String()].Plan.Live
	r.engine.mu.Unlock()
	if u, _ := r.srv.Store.UserByEmail(ctx, "lee@example.com"); u.Tier != "standard" || !live {
		t.Fatalf("tier with a tenant: %s, engine plan live %v", u.Tier, live)
	}
	boss, _ := r.srv.Store.UserByEmail(ctx, "boss@example.com")
	r.post("/admin/users/"+boss.ID.String()+"/tier", url.Values{"csrf": {csrf}, "tier": {"pro"}})
	if u, _ := r.srv.Store.UserByEmail(ctx, "boss@example.com"); u.Tier != "trial" {
		t.Fatalf("an admin got a tier: %s", u.Tier)
	}
	if resp, _ = r.post("/admin/users/"+lee.ID.String()+"/tier", url.Values{"csrf": {csrf}, "tier": {"gold"}}); resp.StatusCode != 404 {
		t.Fatalf("unknown tier: %d", resp.StatusCode)
	}
}

func TestAdminIsNotAPlatformUser(t *testing.T) {
	r := newRig(t)
	r.srv.Admins = []string{"boss@example.com"}
	csrf := r.signup("boss@example.com")
	if n := r.tenants(); n != 0 {
		t.Fatalf("the admin got %d engine tenants", n)
	}
	for _, p := range []string{"/", "/agents", "/setup", "/setup/keys", "/telegram", "/settings"} {
		if resp, _ := r.get(p); resp.StatusCode != 303 || resp.Header.Get("Location") != "/admin" {
			t.Fatalf("%s for the admin: %d %s", p, resp.StatusCode, resp.Header.Get("Location"))
		}
	}
	if resp, _ := r.post("/telegram/links", url.Values{"csrf": {csrf}}, "HX-Request", "true"); resp.Header.Get("HX-Redirect") != "/admin" {
		t.Fatalf("htmx request of the admin: %d %s", resp.StatusCode, resp.Header.Get("HX-Redirect"))
	}
	jar, _ := cookiejar.New(nil)
	r.client.Jar = jar
	if resp, _ := r.post("/login", url.Values{"email": {"boss@example.com"}, "password": {"correct horse battery"}}); resp.StatusCode != 303 || resp.Header.Get("Location") != "/admin" {
		t.Fatalf("admin log-in: %d %s", resp.StatusCode, resp.Header.Get("Location"))
	}
	_, body := r.get("/admin")
	csrf = csrfPattern.FindStringSubmatch(body)[1]
	if resp, body := r.post("/admin/password", url.Values{"csrf": {csrf}, "current": {"wrong password!"}, "password": {"a brand new password"}, "again": {"a brand new password"}}); resp.StatusCode != 422 || !strings.Contains(body, "The current password is wrong") {
		t.Fatalf("wrong current password: %d", resp.StatusCode)
	}
	resp, _ := r.post("/admin/password", url.Values{"csrf": {csrf}, "current": {"correct horse battery"}, "password": {"a brand new password"}, "again": {"a brand new password"}})
	if sent := r.mail.to("boss@example.com"); resp.StatusCode != 303 || resp.Header.Get("Location") != "/admin" || sent[len(sent)-1].Subject != "Your Candylario password was changed" {
		t.Fatalf("admin password change: %d %s", resp.StatusCode, resp.Header.Get("Location"))
	}
	if n := r.tenants(); n != 0 {
		t.Fatalf("the admin got %d engine tenants", n)
	}
}

func TestOnlyConfirmedAdminsSeeTheAdminPage(t *testing.T) {
	r := newRig(t)
	r.srv.Admins = []string{"boss@example.com"}
	csrf := r.signup("lee@example.com")
	resp, body := r.get("/admin")
	if resp.StatusCode != 404 || strings.Contains(body, `href="/admin"`) {
		t.Fatalf("admin page for a user: %d", resp.StatusCode)
	}
	lee, _ := r.srv.Store.UserByEmail(context.Background(), "lee@example.com")
	if resp, _ = r.post("/admin/users/"+lee.ID.String()+"/tier", url.Values{"csrf": {csrf}, "tier": {"pro"}}); resp.StatusCode != 404 {
		t.Fatalf("tier change by a user: %d", resp.StatusCode)
	}
	jar, _ := cookiejar.New(nil)
	r.client.Jar = jar
	r.post("/signup", url.Values{"email": {"boss@example.com"}, "password": {"correct horse battery"}})
	if resp, _ = r.get("/admin"); resp.StatusCode != 303 || resp.Header.Get("Location") != "/verify-email" {
		t.Fatalf("admin address before confirming: %d %s", resp.StatusCode, resp.Header.Get("Location"))
	}
}

func TestKeyFormsSayHowToGetEachKey(t *testing.T) {
	r := newRig(t)
	r.srv.ServerIP = "203.0.113.7"
	r.signup("max@example.com")
	_, body := r.get("/setup/keys")
	for _, want := range []string{"https://platform.claude.com/settings/keys", "https://testnet.binance.vision", "Log In with GitHub"} {
		if !strings.Contains(body, want) {
			t.Fatalf("setup page lacks %q", want)
		}
	}
	if strings.Contains(body, "real-money key") {
		t.Fatal("trial accounts see how to get a real-money key")
	}
	u, _ := r.srv.Store.UserByEmail(context.Background(), "max@example.com")
	r.srv.Store.SetTier(context.Background(), u.ID, "standard")
	if _, body = r.get("/settings"); !strings.Contains(body, "real-money key") || !strings.Contains(body, "<code>203.0.113.7</code>, this platform's address") {
		t.Fatal("settings lack the real-money steps with the server's address")
	}
}

func TestChangingTheEmailNeedsTheNewAddressConfirmed(t *testing.T) {
	r := newRig(t)
	r.srv.Admins = []string{"boss@example.com"}
	ctx := context.Background()
	if _, err := r.srv.Store.CreateUser(ctx, "taken@example.com", "x", "trial"); err != nil {
		t.Fatal(err)
	}
	csrf := r.signup("pat@example.com")
	change := func(email, current string) (*http.Response, string) {
		return r.post("/settings/email", url.Values{"csrf": {csrf}, "email": {email}, "current": {current}})
	}
	for _, tc := range []struct{ email, current, want string }{
		{"pat@new.example", "wrong password!", "current password is wrong"},
		{"pat@example.com", "correct horse battery", "already your email"},
		{"boss@example.com", "correct horse battery", "cannot be used"},
		{"taken@example.com", "correct horse battery", "An account with this email exists"},
	} {
		if resp, body := change(tc.email, tc.current); resp.StatusCode != 422 || !strings.Contains(body, tc.want) {
			t.Fatalf("%s: %d, want %q", tc.email, resp.StatusCode, tc.want)
		}
	}
	if resp, _ := change("Pat@New.example", "correct horse battery"); resp.StatusCode != 303 {
		t.Fatalf("change: %d", resp.StatusCode)
	}
	link := r.mail.link(t, "pat@new.example")
	if u, err := r.srv.Store.UserByEmail(ctx, "pat@example.com"); err != nil || !u.Verified() {
		t.Fatalf("the address changed before the link: %v", err)
	}
	if resp, _ := r.get(link); resp.StatusCode != 303 || resp.Header.Get("Location") != "/settings" {
		t.Fatalf("link: %d %s", resp.StatusCode, resp.Header.Get("Location"))
	}
	if u, err := r.srv.Store.UserByEmail(ctx, "pat@new.example"); err != nil || !u.Verified() {
		t.Fatalf("new address: %+v %v", u, err)
	}
	if sent := r.mail.to("pat@example.com"); sent[len(sent)-1].Subject != "Your Candylario email was changed" || !strings.Contains(sent[len(sent)-1].Text, "boss@example.com") {
		t.Fatalf("no notice to the old address: %+v", sent[len(sent)-1])
	}
	if resp, _ := r.get(link); resp.StatusCode != 410 {
		t.Fatalf("link used twice: %d", resp.StatusCode)
	}
	jar, _ := cookiejar.New(nil)
	r.client.Jar = jar
	if resp, _ := r.post("/login", url.Values{"email": {"pat@example.com"}, "password": {"correct horse battery"}}); resp.StatusCode != 401 {
		t.Fatalf("old address logs in: %d", resp.StatusCode)
	}
	if resp, _ := r.post("/login", url.Values{"email": {"pat@new.example"}, "password": {"correct horse battery"}}); resp.StatusCode != 303 {
		t.Fatalf("new address: %d", resp.StatusCode)
	}
}

func TestDeletingTheAccountClosesItEverywhere(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	csrf := r.signup("oli@example.com")
	tenant := r.tenantOf("oli@example.com")
	r.notif.mu.Lock()
	r.notif.chats = []notifier.Chat{{ChatID: 7, Name: "@oli", LinkedAt: time.Now()}}
	r.notif.mu.Unlock()
	if resp, body := r.post("/settings/delete", url.Values{"csrf": {csrf}, "current": {"wrong password!"}, "confirm": {"DELETE"}}); resp.StatusCode != 422 || !strings.Contains(body, "current password is wrong") {
		t.Fatalf("wrong password: %d", resp.StatusCode)
	}
	if resp, body := r.post("/settings/delete", url.Values{"csrf": {csrf}, "current": {"correct horse battery"}, "confirm": {"delete"}}); resp.StatusCode != 422 || !strings.Contains(body, "Type DELETE") {
		t.Fatalf("without the confirmation: %d", resp.StatusCode)
	}
	resp, _ := r.post("/settings/delete", url.Values{"csrf": {csrf}, "current": {"correct horse battery"}, "confirm": {"DELETE"}})
	if resp.StatusCode != 303 || resp.Header.Get("Location") != "/" {
		t.Fatalf("delete: %d %s", resp.StatusCode, resp.Header.Get("Location"))
	}
	r.engine.mu.Lock()
	status := r.engine.tenants[tenant].Status
	r.engine.mu.Unlock()
	r.notif.mu.Lock()
	left := len(r.notif.chats)
	r.notif.mu.Unlock()
	if status != "closed" || left != 0 {
		t.Fatalf("engine tenant %s, %d chats left", status, left)
	}
	if _, err := r.srv.Store.UserByEmail(ctx, "oli@example.com"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("the account is still there: %v", err)
	}
	if resp, _ := r.get("/agents"); resp.StatusCode != 303 || resp.Header.Get("Location") != "/login" {
		t.Fatalf("still logged in: %d", resp.StatusCode)
	}
	if resp, _ := r.post("/login", url.Values{"email": {"oli@example.com"}, "password": {"correct horse battery"}}); resp.StatusCode != 401 {
		t.Fatalf("log-in after deleting: %d", resp.StatusCode)
	}
	if resp, _ := r.post("/signup", url.Values{"email": {"oli@example.com"}, "password": {"correct horse battery"}}); resp.StatusCode != 303 {
		t.Fatalf("the address is not free again: %d", resp.StatusCode)
	}
}
