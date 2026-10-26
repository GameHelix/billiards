package web

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"
	"rsc.io/qr"

	"github.com/candylario/trader/api"

	"github.com/candylario/app/internal/engine"
	"github.com/candylario/app/internal/notifier"
	"github.com/candylario/app/internal/tiers"
)

// keyView is a stored key as the pages show it. A Binance key's mode is
// kept in its label when the app stores it.
type keyView struct {
	ID        string
	Kind      string
	Mode      string
	Hint      string
	CreatedAt time.Time
}

// keys lists the tenant's live keys, newest first.
func (s *Server) keys(ctx context.Context, tenant string) (anthropic, binance []keyView, err error) {
	keys, err := s.Engine.Keys(ctx, tenant)
	if err != nil {
		return nil, nil, err
	}
	for _, k := range keys {
		if k.Revoked {
			continue
		}
		v := keyView{ID: k.ID, Kind: k.Kind, Hint: k.Hint, CreatedAt: k.CreatedAt}
		switch k.Kind {
		case api.KindAnthropic:
			anthropic = append(anthropic, v)
		case api.KindBinance:
			if k.Label == "live" {
				v.Mode = "live"
			} else {
				v.Mode = "testnet"
			}
			binance = append(binance, v)
		}
	}
	newest := func(a, b keyView) int { return b.CreatedAt.Compare(a.CreatedAt) }
	slices.SortFunc(anthropic, newest)
	slices.SortFunc(binance, newest)
	return anthropic, binance, nil
}

// setup sends the user to the first step they have not done.
func (s *Server) setup(w http.ResponseWriter, r *http.Request) {
	v := current(r)
	ctx, cancel := callCtx(r)
	defer cancel()
	anthropic, binance, err := s.keys(ctx, v.User.TenantID.String())
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if len(anthropic) == 0 || len(binance) == 0 {
		http.Redirect(w, r, "/setup/keys", http.StatusSeeOther)
		return
	}
	agents, err := s.Engine.Agents(ctx, v.User.TenantID.String())
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if len(agents) == 0 {
		http.Redirect(w, r, "/setup/agent", http.StatusSeeOther)
		return
	}
	if s.Notifier != nil {
		if chats, err := s.Notifier.Chats(ctx, v.User.TenantID.String()); err == nil && len(chats) == 0 {
			http.Redirect(w, r, "/telegram?setup=1", http.StatusSeeOther)
			return
		}
	}
	http.Redirect(w, r, "/agents", http.StatusSeeOther)
}

type keysForm struct {
	CSRF         string
	Action       string
	HasAnthropic bool
	Binance      []keyView
	LiveAllowed  bool
	Setup        bool
}

func (s *Server) keysFormData(r *http.Request) (keysForm, error) {
	v := current(r)
	ctx, cancel := callCtx(r)
	defer cancel()
	anthropic, binance, err := s.keys(ctx, v.User.TenantID.String())
	if err != nil {
		return keysForm{}, err
	}
	t, _ := tiers.Get(v.User.Tier)
	setup := strings.HasPrefix(r.URL.Path, "/setup/")
	action := "/settings/keys"
	if setup {
		action = "/setup/keys"
	}
	return keysForm{CSRF: v.CSRF, Action: action, HasAnthropic: len(anthropic) > 0, Binance: binance, LiveAllowed: t.Plan.Live, Setup: setup}, nil
}

func (s *Server) keysForm(w http.ResponseWriter, r *http.Request) {
	f, err := s.keysFormData(r)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	s.render(w, r, "setup_keys.html", http.StatusOK, page{Title: "Your keys", Data: f})
}

func secretJSON(fields map[string]string) json.RawMessage {
	raw, _ := json.Marshal(fields)
	return raw
}

// addKeys passes the keys in the form to the engine, which tries them
// before storing them. Nothing of them stays here.
func (s *Server) addKeys(w http.ResponseWriter, r *http.Request) {
	v := current(r)
	tenant := v.User.TenantID.String()
	t, _ := tiers.Get(v.User.Tier)
	setup := strings.HasPrefix(r.URL.Path, "/setup/")
	anthropic := strings.TrimSpace(r.PostFormValue("anthropic_key"))
	binanceKey := strings.TrimSpace(r.PostFormValue("binance_key"))
	binanceSecret := strings.TrimSpace(r.PostFormValue("binance_secret"))
	mode := r.PostFormValue("binance_mode")
	ctx, cancel := callCtx(r)
	defer cancel()
	var problems []string
	stored := 0
	if anthropic != "" {
		_, err := s.Engine.AddKey(ctx, tenant, api.NewKey{Kind: api.KindAnthropic, Label: "default", Secret: secretJSON(map[string]string{"key": anthropic})})
		if err != nil {
			problems = append(problems, "Anthropic key: "+userMessage(err))
		} else {
			stored++
		}
	}
	switch {
	case binanceKey == "" && binanceSecret == "":
	case binanceKey == "" || binanceSecret == "":
		problems = append(problems, "Enter both the Binance key and its secret.")
	case mode != "testnet" && mode != "live":
		problems = append(problems, "Choose where the Binance key trades.")
	case mode == "live" && !t.Plan.Live:
		problems = append(problems, "Your tier does not include real-money trading; add a testnet key.")
	default:
		_, err := s.Engine.AddKey(ctx, tenant, api.NewKey{Kind: api.KindBinance, Label: mode, Mode: mode, Secret: secretJSON(map[string]string{"key": binanceKey, "secret": binanceSecret})})
		if err != nil {
			problems = append(problems, "Binance key: "+userMessage(err))
		} else {
			stored++
		}
	}
	if stored == 0 && len(problems) == 0 {
		problems = append(problems, "Enter a key.")
	}
	if len(problems) > 0 {
		if setup {
			f, err := s.keysFormData(r)
			if err != nil {
				s.fail(w, r, err)
				return
			}
			s.render(w, r, "setup_keys.html", http.StatusUnprocessableEntity, page{Title: "Your keys", Error: strings.Join(problems, " "), Data: f})
			return
		}
		s.settingsPage(w, r, http.StatusUnprocessableEntity, strings.Join(problems, " "))
		return
	}
	s.flash(w, "Keys stored.")
	if setup {
		http.Redirect(w, r, "/setup", http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, "/settings", http.StatusSeeOther)
}

type coinOption struct {
	Symbol  string
	Coin    string
	Checked bool
}

type agentForm struct {
	Action    string
	Tier      tiers.Tier
	Coins     []coinOption
	Intervals []float64
	Binance   []keyView
	Name      string
	Capital   string
	Interval  float64
	Binance1  string
	Setup     bool
}

var intervalChoices = []float64{1, 2, 4, 8, 12, 24}

func (s *Server) agentFormData(r *http.Request, ctx context.Context) (agentForm, error) {
	v := current(r)
	t, _ := tiers.Get(v.User.Tier)
	anthropic, binance, err := s.keys(ctx, v.User.TenantID.String())
	if err != nil {
		return agentForm{}, err
	}
	if len(anthropic) == 0 || len(binance) == 0 {
		return agentForm{}, errNeedKeys
	}
	f := agentForm{Tier: t, Binance: binance, Capital: "100", Interval: 4, Action: "/agents", Setup: strings.HasPrefix(r.URL.Path, "/setup/")}
	if f.Setup {
		f.Action = "/setup/agent"
	}
	for _, sym := range t.Plan.Symbols {
		f.Coins = append(f.Coins, coinOption{Symbol: sym, Coin: tiers.Coin(sym), Checked: sym == "BTCUSDT" || sym == "ETHUSDT"})
	}
	for _, h := range intervalChoices {
		if h >= t.Plan.MinDecisionIntervalHours {
			f.Intervals = append(f.Intervals, h)
		}
	}
	if f.Interval < t.Plan.MinDecisionIntervalHours {
		f.Interval = f.Intervals[0]
	}
	return f, nil
}

var errNeedKeys = errors.New("keys first")

func (s *Server) agentForm(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := callCtx(r)
	defer cancel()
	f, err := s.agentFormData(r, ctx)
	if errors.Is(err, errNeedKeys) {
		s.flash(w, "Add your keys first.")
		http.Redirect(w, r, "/setup/keys", http.StatusSeeOther)
		return
	}
	if err != nil {
		s.fail(w, r, err)
		return
	}
	s.render(w, r, "agent_form.html", http.StatusOK, page{Title: "New agent", Data: f})
}

var namePattern = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_-]{0,39}$`)

// createAgent builds the agent's config from the form and the engine's
// defaults, creates it and starts it.
func (s *Server) createAgent(w http.ResponseWriter, r *http.Request) {
	v := current(r)
	tenant := v.User.TenantID.String()
	ctx, cancel := callCtx(r)
	defer cancel()
	f, err := s.agentFormData(r, ctx)
	if errors.Is(err, errNeedKeys) {
		s.flash(w, "Add your keys first.")
		http.Redirect(w, r, "/setup/keys", http.StatusSeeOther)
		return
	}
	if err != nil {
		s.fail(w, r, err)
		return
	}
	f.Name = strings.TrimSpace(r.PostFormValue("name"))
	f.Capital = strings.TrimSpace(r.PostFormValue("capital"))
	f.Binance1 = r.PostFormValue("binance_key")
	chosen := r.PostForm["symbols"]
	for i := range f.Coins {
		f.Coins[i].Checked = slices.Contains(chosen, f.Coins[i].Symbol)
	}
	interval, _ := strconv.ParseFloat(r.PostFormValue("interval"), 64)
	capital, capErr := strconv.ParseFloat(f.Capital, 64)
	var key keyView
	for _, k := range f.Binance {
		if k.ID == f.Binance1 || (f.Binance1 == "" && len(f.Binance) == 1) {
			key = k
		}
	}
	problem := ""
	switch {
	case !namePattern.MatchString(f.Name):
		problem = "Name the agent with 1–40 letters, digits, dashes or underscores."
	case len(chosen) == 0:
		problem = "Choose at least one coin."
	case !subset(chosen, f.Tier.Plan.Symbols):
		problem = "Choose coins from your tier."
	case capErr != nil || capital < engine.MinCapitalUSD || capital > 1e6:
		problem = fmt.Sprintf("Capital must be %d–1,000,000 USDT.", engine.MinCapitalUSD)
	case !slices.Contains(f.Intervals, interval):
		problem = "Choose a decision interval your tier allows."
	case key.ID == "":
		problem = "Choose the Binance key the agent trades with."
	}
	if problem != "" {
		f.Interval = interval
		s.render(w, r, "agent_form.html", http.StatusUnprocessableEntity, page{Title: "New agent", Error: problem, Data: f})
		return
	}
	anthropic, _, err := s.keys(ctx, tenant)
	if err != nil || len(anthropic) == 0 {
		s.fail(w, r, fmt.Errorf("keys: %v", err))
		return
	}
	cfg := engine.AgentConfig(engine.Settings{Name: f.Name, Symbols: chosen, CapitalUSD: capital, DecisionIntervalHours: interval, MaxEventDecisionsPerDay: f.Tier.Plan.MaxEventDecisionsPerDay})
	a, err := s.Engine.CreateAgent(ctx, tenant, api.NewAgent{Name: f.Name, Mode: key.Mode, Config: cfg, Keys: []string{anthropic[0].ID, key.ID}})
	if err != nil {
		f.Interval = interval
		s.render(w, r, "agent_form.html", http.StatusUnprocessableEntity, page{Title: "New agent", Error: userMessage(err), Data: f})
		return
	}
	if _, err := s.Engine.Start(ctx, tenant, a.ID); err != nil {
		s.flash(w, "The agent was created but did not start: "+userMessage(err))
	} else {
		s.flash(w, "The agent starts within half a minute.")
	}
	http.Redirect(w, r, "/agents/"+a.ID, http.StatusSeeOther)
}

func subset(items, allowed []string) bool {
	for _, it := range items {
		if !slices.Contains(allowed, it) {
			return false
		}
	}
	return true
}

type agentRow struct {
	api.Agent
	Equity  string
	Trading string
	Problem string
}

type agentsPage struct {
	Agents []agentRow
	CanAdd bool
	Tier   tiers.Tier
}

func (s *Server) agents(w http.ResponseWriter, r *http.Request) {
	v := current(r)
	tenant := v.User.TenantID.String()
	ctx, cancel := callCtx(r)
	defer cancel()
	agents, err := s.Engine.Agents(ctx, tenant)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	t, _ := tiers.Get(v.User.Tier)
	p := agentsPage{Tier: t, CanAdd: len(agents) < t.Plan.MaxAgents}
	for _, a := range agents {
		row := agentRow{Agent: a}
		st, err := s.Engine.Status(ctx, tenant, a.ID)
		switch {
		case err == nil:
			row.Equity, row.Trading = st.Equity, st.Trading
		case isCode(err, "no_account"):
			row.Problem = "not started yet"
		default:
			row.Problem = "status unavailable"
		}
		p.Agents = append(p.Agents, row)
	}
	s.render(w, r, "agents.html", http.StatusOK, page{Title: "Agents", Data: p})
}

type statusView struct {
	Agent        api.Agent
	Status       *api.Status
	Problem      string
	Message      string
	ClearConfirm bool
}

func (s *Server) statusView(ctx context.Context, tenant string, a api.Agent, message string) statusView {
	v := statusView{Agent: a, Message: message}
	st, err := s.Engine.Status(ctx, tenant, a.ID)
	switch {
	case err == nil:
		v.Status = &st
	case isCode(err, "no_account"):
		v.Problem = "Not started yet. Start the agent to set aside its capital."
	default:
		s.Logf("status of %s: %v", a.ID, err)
		v.Problem = userMessage(err)
	}
	return v
}

type eventView struct {
	Seq     int64
	At      time.Time
	Type    string
	Summary string
	Raw     json.RawMessage
}

func (s *Server) eventViews(ctx context.Context, tenant, agent string) ([]eventView, error) {
	events, err := s.Events.Recent(ctx, tenant, agent, 50)
	if err != nil {
		return nil, err
	}
	views := make([]eventView, 0, len(events))
	for _, e := range events {
		views = append(views, eventView{Seq: e.Seq, At: e.At, Type: e.Type, Summary: engine.Summary(e), Raw: e.Payload})
	}
	return views, nil
}

type agentPage struct {
	Agent  api.Agent
	Status statusView
	Events []eventView
	Chart  chartView
}

// agentOf finds the agent in the path, within the user's tenant.
func (s *Server) agentOf(w http.ResponseWriter, r *http.Request, ctx context.Context) (api.Agent, string, bool) {
	tenant := current(r).User.TenantID.String()
	a, err := s.Engine.Agent(ctx, tenant, r.PathValue("id"))
	if isCode(err, "not_found") || isCode(err, "bad_request") {
		s.notFound(w, r)
		return a, tenant, false
	}
	if err != nil {
		s.fail(w, r, err)
		return a, tenant, false
	}
	return a, tenant, true
}

func (s *Server) agent(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := callCtx(r)
	defer cancel()
	a, tenant, ok := s.agentOf(w, r, ctx)
	if !ok {
		return
	}
	p := agentPage{Agent: a, Status: s.statusView(ctx, tenant, a, "")}
	if events, err := s.eventViews(ctx, tenant, a.ID); err == nil {
		p.Events = events
	} else {
		s.Logf("events of %s: %v", a.ID, err)
	}
	if points, err := s.Events.Equity(ctx, tenant, a.ID); err == nil {
		p.Chart = buildChart(points)
	}
	s.render(w, r, "agent.html", http.StatusOK, page{Title: a.Name, Data: p})
}

func (s *Server) agentStatus(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := callCtx(r)
	defer cancel()
	a, tenant, ok := s.agentOf(w, r, ctx)
	if !ok {
		return
	}
	s.partial(w, "status", http.StatusOK, s.statusView(ctx, tenant, a, ""))
}

func (s *Server) agentEvents(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := callCtx(r)
	defer cancel()
	a, tenant, ok := s.agentOf(w, r, ctx)
	if !ok {
		return
	}
	events, err := s.eventViews(ctx, tenant, a.ID)
	if err != nil {
		s.Logf("events of %s: %v", a.ID, err)
	}
	s.partial(w, "events", http.StatusOK, events)
}

func (s *Server) agentChart(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := callCtx(r)
	defer cancel()
	a, tenant, ok := s.agentOf(w, r, ctx)
	if !ok {
		return
	}
	points, err := s.Events.Equity(ctx, tenant, a.ID)
	if err != nil {
		s.Logf("equity of %s: %v", a.ID, err)
	}
	s.partial(w, "chart", http.StatusOK, buildChart(points))
}

func (s *Server) sellAllForm(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := callCtx(r)
	defer cancel()
	a, _, ok := s.agentOf(w, r, ctx)
	if !ok {
		return
	}
	if r.URL.Query().Get("cancel") != "" {
		w.WriteHeader(http.StatusOK)
		return
	}
	s.partial(w, "sellall", http.StatusOK, a)
}

// command sends decide, stop_trading, resume_trading or sell_all. Selling
// everything needs the confirm step.
func (s *Server) command(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := callCtx(r)
	defer cancel()
	a, tenant, ok := s.agentOf(w, r, ctx)
	if !ok {
		return
	}
	cmd := r.PostFormValue("command")
	switch cmd {
	case api.Decide, api.StopTrading, api.ResumeTrading:
	case api.SellAll:
		if r.PostFormValue("confirm") != "yes" {
			s.partial(w, "status", http.StatusBadRequest, s.statusView(ctx, tenant, a, "Confirm selling everything first."))
			return
		}
	default:
		http.Error(w, "Unknown command.", http.StatusBadRequest)
		return
	}
	res, err := s.Engine.Command(ctx, tenant, a.ID, api.Command{Command: cmd, Via: "app"})
	message := res.Message
	if err != nil {
		message = userMessage(err)
	}
	v := s.statusView(ctx, tenant, a, message)
	v.ClearConfirm = true
	s.partial(w, "status", http.StatusOK, v)
}

func (s *Server) startAgent(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := callCtx(r)
	defer cancel()
	a, tenant, ok := s.agentOf(w, r, ctx)
	if !ok {
		return
	}
	message := "Starting; the agent runs within half a minute."
	if updated, err := s.Engine.Start(ctx, tenant, a.ID); err != nil {
		message = userMessage(err)
	} else {
		a = updated
	}
	s.partial(w, "status", http.StatusOK, s.statusView(ctx, tenant, a, message))
}

func (s *Server) stopAgent(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := callCtx(r)
	defer cancel()
	a, tenant, ok := s.agentOf(w, r, ctx)
	if !ok {
		return
	}
	message := "Stopping; holdings are kept."
	if updated, err := s.Engine.Stop(ctx, tenant, a.ID); err != nil {
		message = userMessage(err)
	} else {
		a = updated
	}
	s.partial(w, "status", http.StatusOK, s.statusView(ctx, tenant, a, message))
}

type telegramPage struct {
	Configured bool
	Chats      []notifier.Chat
	Problem    string
	Setup      bool
}

func (s *Server) telegram(w http.ResponseWriter, r *http.Request) {
	v := current(r)
	p := telegramPage{Configured: s.Notifier != nil, Setup: r.URL.Query().Get("setup") == "1"}
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

type settingsPage struct {
	Tier      tiers.Tier
	Anthropic []keyView
	Binance   []keyView
	Form      keysForm
}

func (s *Server) settingsPage(w http.ResponseWriter, r *http.Request, status int, problem string) {
	v := current(r)
	ctx, cancel := callCtx(r)
	defer cancel()
	anthropic, binance, err := s.keys(ctx, v.User.TenantID.String())
	if err != nil {
		s.fail(w, r, err)
		return
	}
	t, _ := tiers.Get(v.User.Tier)
	p := settingsPage{Tier: t, Anthropic: anthropic, Binance: binance,
		Form: keysForm{CSRF: v.CSRF, Action: "/settings/keys", HasAnthropic: len(anthropic) > 0, Binance: binance, LiveAllowed: t.Plan.Live}}
	s.render(w, r, "settings.html", status, page{Title: "Settings", Error: problem, Data: p})
}

func (s *Server) settings(w http.ResponseWriter, r *http.Request) {
	s.settingsPage(w, r, http.StatusOK, "")
}

func (s *Server) revokeKey(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := callCtx(r)
	defer cancel()
	res, err := s.Engine.RevokeKey(ctx, current(r).User.TenantID.String(), r.PathValue("id"))
	switch {
	case err != nil:
		s.flash(w, userMessage(err))
	case len(res.Stopped) > 0:
		s.flash(w, fmt.Sprintf("Key revoked; %d agent(s) stopped without it.", len(res.Stopped)))
	default:
		s.flash(w, "Key revoked.")
	}
	http.Redirect(w, r, "/settings", http.StatusSeeOther)
}

func (s *Server) changePassword(w http.ResponseWriter, r *http.Request) {
	v := current(r)
	current, next, again := r.PostFormValue("current"), r.PostFormValue("password"), r.PostFormValue("again")
	problem := ""
	switch {
	case bcrypt.CompareHashAndPassword([]byte(v.User.PasswordHash), []byte(current)) != nil:
		problem = "The current password is wrong."
	case next != again:
		problem = "The new passwords differ."
	default:
		problem = checkPassword(next)
	}
	if problem != "" {
		s.settingsPage(w, r, http.StatusUnprocessableEntity, problem)
		return
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(next), bcrypt.DefaultCost)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if err := s.Store.SetPassword(r.Context(), v.User.ID, string(hash)); err != nil {
		s.fail(w, r, err)
		return
	}
	if err := s.Store.DeleteOtherSessions(r.Context(), v.User.ID, v.Token); err != nil {
		s.Logf("database: %v", err)
	}
	if err := s.sendPasswordNotice(r.Context(), v.User); err != nil {
		s.Logf("password notice: %v", err)
	}
	s.flash(w, "Password changed; other browsers were logged out.")
	http.Redirect(w, r, "/settings", http.StatusSeeOther)
}
