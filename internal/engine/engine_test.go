package engine

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/candylario/trader/api"
)

// The engine requires every field of its config except model and
// data_dir; these are the fields of trader/config/agent.json.
var configFields = strings.Fields(`name symbols quote_asset capital_usd max_position_weight max_invested_weight rebalance_band
	min_trade_usd decision_interval_hours move_trigger_1h_pct move_trigger_since_decision_pct event_cooldown_minutes poll_seconds
	news_feeds news_alert_keywords max_event_decisions_per_day news_items score_horizon_hours daily_loss_limit_pct max_drawdown_pct
	effort max_output_tokens max_turns samples refusal_fallback input_usd_per_million output_usd_per_million
	cache_write_usd_per_million cache_read_usd_per_million max_daily_llm_usd max_monthly_llm_usd`)

func TestAgentConfigHasEveryFieldTheEngineRequires(t *testing.T) {
	raw := AgentConfig(Settings{Name: "btc-eth", Symbols: []string{"BTCUSDT"}, CapitalUSD: 100, DecisionIntervalHours: 4, MaxEventDecisionsPerDay: 4})
	var got map[string]any
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	for _, f := range configFields {
		if _, ok := got[f]; !ok {
			t.Errorf("missing %s", f)
		}
	}
	if len(got) != len(configFields) {
		t.Errorf("%d fields, want %d", len(got), len(configFields))
	}
	if got["capital_usd"] != 100.0 || got["decision_interval_hours"] != 4.0 || got["max_event_decisions_per_day"] != 4.0 || got["name"] != "btc-eth" {
		t.Errorf("choices not applied: %v", got)
	}
	if _, has := got["data_dir"]; has {
		t.Error("data_dir must be left to the engine")
	}
}

func TestSummaryReadsKnownEvents(t *testing.T) {
	cases := []struct{ typ, payload, want string }{
		{"notice", `{"text":"🧪 Decision 3: hold BTC"}`, "🧪 Decision 3: hold BTC"},
		{"decision.finished", `{"signals":{"ETHUSDT":{"rating":"buy"},"BTCUSDT":{"rating":"strong_buy"}},"cost_usd":0.0598}`, "BTCUSDT strong buy · ETHUSDT buy · $0.06"},
		{"equity.hourly", `{"equity":"100","buy_and_hold":"100.35"}`, "equity 100, buy and hold 100.35"},
		{"command.received", `{"name":"decide","via":"app"}`, "decide via app"},
		{"trading.stopped", `{"kind":"daily_loss","reason":"lost 10% today"}`, "daily_loss: lost 10% today"},
		{"model.call", `{"purpose":"decide","status":"settled","cost_usd":0.0123,"latency_ms":3100}`, "decide · settled · $0.0123 · 3.1 s"},
		{"trade.booked", `{"trade":{"side":"BUY","symbol":"BTCUSDT","qty":"0.001","avg_price":"67000","quote_qty":"67"}}`, "BUY 0.001 BTCUSDT at 67000 for 67 USDT"},
		{"call.graded", `{"symbol":"BTCUSDT","rating":"strong_buy","return":0.0123,"hit":true}`, "BTCUSDT strong buy: right, price +1.23%"},
		{"lesson.recorded", `{"lesson":{"text":"Fade spikes on thin weekend volume."}}`, "Fade spikes on thin weekend volume."},
		{"agent.stopped", `{"cash":"100","by":"shutdown"}`, "cash 100 · by shutdown"},
		{"something.new", `{"b":2,"a":"x"}`, "a x · b 2"},
	}
	for _, c := range cases {
		if got := Summary(api.Event{Type: c.typ, Payload: json.RawMessage(c.payload)}); got != c.want {
			t.Errorf("%s: got %q, want %q", c.typ, got, c.want)
		}
	}
}

// fakeLog serves total events, equity.hourly every tenth and model.call
// otherwise, honouring after, before, limit and type.
func fakeLog(t *testing.T, total int) (*httptest.Server, *int) {
	calls := 0
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Header.Get("Authorization") != "Bearer tok" {
			w.WriteHeader(401)
			return
		}
		q := r.URL.Query()
		after, _ := strconv.Atoi(q.Get("after"))
		before, err := strconv.Atoi(q.Get("before"))
		if err != nil {
			before = total + 1
		}
		limit, _ := strconv.Atoi(q.Get("limit"))
		var out api.EventList
		for seq := after + 1; seq <= total && seq < before; seq++ {
			typ := "model.call"
			if seq%10 == 0 {
				typ = "equity.hourly"
			}
			if q.Get("type") != "" && !slices.Contains(strings.Split(q.Get("type"), ","), typ) {
				continue
			}
			out.Events = append(out.Events, api.Event{Seq: int64(seq), Type: typ, At: time.Unix(int64(seq)*3600, 0),
				Payload: json.RawMessage(`{"equity":"` + strconv.Itoa(seq) + `","buy_and_hold":"100"}`)})
		}
		if len(out.Events) > limit {
			if q.Has("before") {
				out.Events = out.Events[len(out.Events)-limit:]
			} else {
				out.Events = out.Events[:limit]
			}
		}
		out.Events = append([]api.Event{}, out.Events...)
		json.NewEncoder(w).Encode(out)
	})), &calls
}

func TestEventsReadFromEitherEndInOneCall(t *testing.T) {
	srv, calls := fakeLog(t, 2345)
	defer srv.Close()
	e := &Events{Client: &api.Client{BaseURL: srv.URL, Token: "tok"}}
	latest, err := e.Latest(t.Context(), "t", "a", 0, nil, 50)
	if err != nil || len(latest) != 50 || latest[0].Seq != 2345 || latest[49].Seq != 2296 {
		t.Fatalf("latest: %d events %v", len(latest), err)
	}
	older, err := e.Latest(t.Context(), "t", "a", 2296, []string{"equity.hourly"}, 3)
	if err != nil || len(older) != 3 || older[0].Seq != 2290 || older[2].Seq != 2270 {
		t.Fatalf("older equity: %+v %v", older, err)
	}
	newer, err := e.Since(t.Context(), "t", "a", 2340, nil, 50)
	if err != nil || len(newer) != 5 || newer[0].Seq != 2345 || newer[4].Seq != 2341 {
		t.Fatalf("newer: %+v %v", newer, err)
	}
	if *calls != 3 {
		t.Errorf("%d calls for three reads", *calls)
	}
}

func TestEquityPagesThroughTheLogAndKeepsGoingFromTheEnd(t *testing.T) {
	srv, calls := fakeLog(t, 23450)
	defer srv.Close()
	e := &Events{Client: &api.Client{BaseURL: srv.URL, Token: "tok"}}
	points, err := e.Equity(t.Context(), "t", "a")
	if err != nil {
		t.Fatal(err)
	}
	if len(points) != 2345 || points[0].Equity != 10 || points[2344].Equity != 23450 {
		t.Fatalf("equity: %d points", len(points))
	}
	if *calls != 3 {
		t.Errorf("%d calls to read 2345 points, want 3", *calls)
	}
	if _, err := e.Equity(t.Context(), "t", "a"); err != nil {
		t.Fatal(err)
	}
	if *calls != 4 {
		t.Errorf("a second read made %d calls, want 1", *calls-3)
	}
}

func TestEventTypesHaveNamesGroupsAndAlerts(t *testing.T) {
	for _, g := range Groups {
		for _, typ := range g.Types {
			if Label(typ) == typ || GroupOf(typ) != g.Name {
				t.Errorf("%s: label %q, group %q", typ, Label(typ), GroupOf(typ))
			}
		}
	}
	if Label("something.new") != "something.new" || GroupOf("something.new") != "" {
		t.Error("unknown types keep their name and have no group")
	}
	for _, c := range []struct {
		typ, payload string
		alert        bool
	}{
		{"notice", `{"text":"🧪 Decision 3: hold BTC"}`, false},
		{"notice", `{"text":"🧪 ⚠️ Price data unavailable","alert":"prices"}`, true},
		{"notice", `{"text":"🛑 Trading stopped: daily loss"}`, true},
		{"decision.failed", `{"error":"budget"}`, true},
		{"order.result", `{"outcome":"filled"}`, false},
		{"order.result", `{"outcome":"rejected","error":"-2010"}`, true},
		{"trade.booked", `{}`, false},
	} {
		if got := Alert(api.Event{Type: c.typ, Payload: json.RawMessage(c.payload)}); got != c.alert {
			t.Errorf("%s %s: alert %v", c.typ, c.payload, got)
		}
	}
}
