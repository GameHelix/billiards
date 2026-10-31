package web

import (
	"context"
	"encoding/json"
	"errors"
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

// fakeEngine is the engine API with in-memory tenants, as openapi.yaml
// describes it.
type fakeEngine struct {
	mu      sync.Mutex
	token   string
	tenants map[string]api.Tenant
}

func newFakeEngine() *fakeEngine {
	return &fakeEngine{token: "engine-token", tenants: map[string]api.Tenant{}}
}

func (f *fakeEngine) server(t *testing.T) *httptest.Server {
	mux := http.NewServeMux()
	reply := func(w http.ResponseWriter, status int, v any) {
		w.WriteHeader(status)
		json.NewEncoder(w).Encode(v)
	}
	notFound := func(w http.ResponseWriter) {
		reply(w, 404, map[string]any{"error": map[string]string{"code": "not_found", "message": "no such tenant"}})
	}
	mux.HandleFunc("GET /v1/health", func(w http.ResponseWriter, r *http.Request) { reply(w, 200, map[string]bool{"ok": true}) })
	mux.HandleFunc("POST /v1/tenants", func(w http.ResponseWriter, r *http.Request) {
		var in api.NewTenant
		d := json.NewDecoder(r.Body)
		d.DisallowUnknownFields()
		if err := d.Decode(&in); err != nil {
			reply(w, 400, map[string]any{"error": map[string]string{"code": "bad_request", "message": err.Error()}})
			return
		}
		f.mu.Lock()
		defer f.mu.Unlock()
		if t, ok := f.tenants[in.ID]; ok {
			reply(w, 200, t)
			return
		}
		t := api.Tenant{ID: in.ID, Name: in.Name, Status: "active"}
		f.tenants[in.ID] = t
		reply(w, 201, t)
	})
	mux.HandleFunc("GET /v1/tenants/{t}", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		t, ok := f.tenants[r.PathValue("t")]
		if !ok {
			notFound(w)
			return
		}
		reply(w, 200, t)
	})
	mux.HandleFunc("DELETE /v1/tenants/{t}", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		t, ok := f.tenants[r.PathValue("t")]
		if !ok {
			notFound(w)
			return
		}
		t.Status, t.Name = "closed", ""
		f.tenants[t.ID] = t
		reply(w, 200, t)
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+f.token {
			reply(w, 401, map[string]any{"error": map[string]string{"code": "unauthorized", "message": "bad token"}})
			return
		}
		notFound(w)
	})
	return httptest.NewServer(mux)
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
		if r.Header.Get("Authorization") != "Bearer notifier-token" && r.URL.Path != "/v1/health" {
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
	} else if resp.StatusCode != 303 || next != "/telegram" {
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

func TestSignupCreatesTheTenantAndLogsIn(t *testing.T) {
	r := newRig(t)
	r.signup("Ada@Example.com")
	tenant := r.tenantOf("ada@example.com")
	r.engine.mu.Lock()
	tn, ok := r.engine.tenants[tenant]
	r.engine.mu.Unlock()
	if !ok || tn.Name != "ada@example.com" || tn.Status != "active" {
		t.Fatalf("tenant in engine: %+v %v", tn, ok)
	}
	resp, body := r.get("/telegram")
	if resp.StatusCode != 200 || !strings.Contains(body, "Connect a chat") || !strings.Contains(body, `href="/telegram" aria-current="page"`) {
		t.Fatalf("telegram page: %d", resp.StatusCode)
	}
	for _, gone := range []string{"/agents", "/activity", "/setup", "/setup/keys"} {
		if resp, _ := r.get(gone); resp.StatusCode != 404 {
			t.Fatalf("%s is gone: %d", gone, resp.StatusCode)
		}
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
	if resp.StatusCode != 303 || resp.Header.Get("Location") != "/telegram" {
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
	resp, _ = r.get("/telegram")
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
	resp, _ = r.get("/telegram")
	if resp.StatusCode != 200 {
		t.Fatalf("logged in with new password: %d", resp.StatusCode)
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

func TestLogoutEndsTheSession(t *testing.T) {
	r := newRig(t)
	csrf := r.signup("ivy@example.com")
	resp, _ := r.post("/logout", url.Values{"csrf": {csrf}})
	if resp.StatusCode != 303 {
		t.Fatalf("logout: %d", resp.StatusCode)
	}
	resp, _ = r.get("/telegram")
	if resp.StatusCode != 303 || resp.Header.Get("Location") != "/login" {
		t.Fatalf("after logout: %d", resp.StatusCode)
	}
	req, _ := http.NewRequest(http.MethodGet, r.app.URL+"/telegram", nil)
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
	resp, _ = r.get("/telegram")
	if resp.StatusCode != 303 || resp.Header.Get("Location") != "/verify-email" {
		t.Fatalf("telegram before confirming: %d %s", resp.StatusCode, resp.Header.Get("Location"))
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
	if resp, _ = r.get(second); resp.StatusCode != 303 || resp.Header.Get("Location") != "/telegram" {
		t.Fatalf("link: %d %s", resp.StatusCode, resp.Header.Get("Location"))
	}
	if resp, _ = r.get(second); resp.StatusCode != 303 || resp.Header.Get("Location") != "/telegram" {
		t.Fatalf("link again while logged in: %d %s", resp.StatusCode, resp.Header.Get("Location"))
	}
	if resp, _ = r.get("/telegram"); resp.StatusCode != 200 || r.tenants() != 1 {
		t.Fatalf("telegram after confirming: %d, %d tenants", resp.StatusCode, r.tenants())
	}
}

func TestAMistypedAddressCanBeChangedBeforeConfirming(t *testing.T) {
	r := newRig(t)
	if _, err := r.srv.Store.CreateUser(context.Background(), "gus@example.com", "x"); err != nil {
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
	if resp, _ = r.get(r.mail.link(t, "fran@example.com")); resp.StatusCode != 303 || resp.Header.Get("Location") != "/telegram" {
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
	if resp.StatusCode != 303 || resp.Header.Get("Location") != "/telegram" {
		t.Fatalf("reset: %d %s", resp.StatusCode, resp.Header.Get("Location"))
	}
	if resp, _ = r.get("/telegram"); resp.StatusCode != 200 {
		t.Fatalf("logged in after the reset: %d", resp.StatusCode)
	}
	if resp, _ = r.get(link); resp.StatusCode != 410 {
		t.Fatalf("link used twice: %d", resp.StatusCode)
	}
	r.client.Jar = first
	if resp, _ = r.get("/telegram"); resp.StatusCode != 303 || resp.Header.Get("Location") != "/login" {
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

func TestAdminSeesEveryAccount(t *testing.T) {
	r := newRig(t)
	r.srv.Admins = []string{"boss@example.com"}
	ctx := context.Background()
	if _, err := r.srv.Store.CreateUser(ctx, "kim@example.com", "x"); err != nil {
		t.Fatal(err)
	}
	r.signup("lee@example.com")
	jar, _ := cookiejar.New(nil)
	r.client.Jar = jar
	r.signup("boss@example.com")
	resp, body := r.get("/admin")
	if resp.StatusCode != 200 || !strings.Contains(body, "kim@example.com") || !strings.Contains(body, "lee@example.com") || !strings.Contains(body, "unconfirmed") || !strings.Contains(body, `href="/admin"`) {
		t.Fatalf("admin page: %d %s", resp.StatusCode, body)
	}
	if strings.Contains(body, "/tier") {
		t.Fatal("tiers are gone")
	}
}

func TestAdminIsNotAPlatformUser(t *testing.T) {
	r := newRig(t)
	r.srv.Admins = []string{"boss@example.com"}
	csrf := r.signup("boss@example.com")
	if n := r.tenants(); n != 0 {
		t.Fatalf("the admin got %d engine tenants", n)
	}
	for _, p := range []string{"/", "/telegram", "/settings"} {
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
	if resp, _ = r.post("/admin/password", url.Values{"csrf": {csrf}, "current": {"correct horse battery"}, "password": {"a brand new password"}, "again": {"a brand new password"}}); resp.StatusCode != 404 {
		t.Fatalf("an admin route for a user: %d", resp.StatusCode)
	}
	jar, _ := cookiejar.New(nil)
	r.client.Jar = jar
	r.post("/signup", url.Values{"email": {"boss@example.com"}, "password": {"correct horse battery"}})
	if resp, _ = r.get("/admin"); resp.StatusCode != 303 || resp.Header.Get("Location") != "/verify-email" {
		t.Fatalf("admin address before confirming: %d %s", resp.StatusCode, resp.Header.Get("Location"))
	}
}

func TestChangingTheEmailNeedsTheNewAddressConfirmed(t *testing.T) {
	r := newRig(t)
	r.srv.Admins = []string{"boss@example.com"}
	ctx := context.Background()
	if _, err := r.srv.Store.CreateUser(ctx, "taken@example.com", "x"); err != nil {
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
	if resp, _ := r.get("/telegram"); resp.StatusCode != 303 || resp.Header.Get("Location") != "/login" {
		t.Fatalf("still logged in: %d", resp.StatusCode)
	}
	if resp, _ := r.post("/login", url.Values{"email": {"oli@example.com"}, "password": {"correct horse battery"}}); resp.StatusCode != 401 {
		t.Fatalf("log-in after deleting: %d", resp.StatusCode)
	}
	if resp, _ := r.post("/signup", url.Values{"email": {"oli@example.com"}, "password": {"correct horse battery"}}); resp.StatusCode != 303 {
		t.Fatalf("the address is not free again: %d", resp.StatusCode)
	}
}

func TestHealthCoversTheEngineAndTheNotifier(t *testing.T) {
	r := newRig(t)
	resp, body := r.get("/healthz")
	if resp.StatusCode != 200 || !strings.Contains(body, `"engine":true`) || !strings.Contains(body, `"notifier":true`) || !strings.Contains(body, `"ok":true`) {
		t.Fatalf("healthy: %d %s", resp.StatusCode, body)
	}
	r.srv.Notifier.BaseURL = "http://127.0.0.1:1"
	if resp, body = r.get("/healthz"); resp.StatusCode != 503 || !strings.Contains(body, `"notifier":false`) || !strings.Contains(body, `"ok":false`) {
		t.Fatalf("notifier down: %d %s", resp.StatusCode, body)
	}
}

func TestLegalPagesAreLinkedFromEveryPublicPage(t *testing.T) {
	r := newRig(t)
	for path, want := range map[string]string{"/terms": "Ismat Samadov", "/privacy": "Hetzner Online", "/risk": "can be liquidated", "/refunds": "no subscriptions"} {
		if resp, body := r.get(path); resp.StatusCode != 200 || !strings.Contains(body, want) {
			t.Fatalf("%s: %d, want %q", path, resp.StatusCode, want)
		}
	}
	_, body := r.get("/signup")
	for _, want := range []string{`href="/terms"`, `href="/privacy"`, `href="/risk"`, `href="/refunds"`, "By creating an account you agree"} {
		if !strings.Contains(body, want) {
			t.Fatalf("sign-up page lacks %s", want)
		}
	}
}
