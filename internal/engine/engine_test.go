package engine

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
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
		{"something.new", `{"b":2,"a":"x"}`, "a x · b 2"},
	}
	for _, c := range cases {
		if got := Summary(api.Event{Type: c.typ, Payload: json.RawMessage(c.payload)}); got != c.want {
			t.Errorf("%s: got %q, want %q", c.typ, got, c.want)
		}
	}
}

// fakeLog serves 2,345 events, equity.hourly every tenth, honouring after,
// limit and type.
func fakeLog(t *testing.T, total int) (*httptest.Server, *int) {
	calls := 0
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Header.Get("Authorization") != "Bearer tok" {
			w.WriteHeader(401)
			return
		}
		after, _ := strconv.Atoi(r.URL.Query().Get("after"))
		limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
		types := r.URL.Query().Get("type")
		var out api.EventList
		for seq := after + 1; seq <= total && len(out.Events) < limit; seq++ {
			typ := "model.call"
			if seq%10 == 0 {
				typ = "equity.hourly"
			}
			if types != "" && typ != types {
				continue
			}
			out.Events = append(out.Events, api.Event{Seq: int64(seq), Type: typ, At: time.Unix(int64(seq)*3600, 0),
				Payload: json.RawMessage(`{"equity":"` + strconv.Itoa(seq) + `","buy_and_hold":"100"}`)})
		}
		out.Events = append([]api.Event{}, out.Events...)
		json.NewEncoder(w).Encode(out)
	})), &calls
}

func TestEventsPageThroughTheLogAndKeepGoingFromTheEnd(t *testing.T) {
	srv, calls := fakeLog(t, 2345)
	defer srv.Close()
	e := &Events{Client: &api.Client{BaseURL: srv.URL, Token: "tok"}}
	recent, err := e.Recent(t.Context(), "t", "a", 50)
	if err != nil {
		t.Fatal(err)
	}
	if len(recent) != 50 || recent[0].Seq != 2345 || recent[49].Seq != 2296 {
		t.Fatalf("recent: %d events, first seq %d", len(recent), recent[0].Seq)
	}
	if *calls != 3 {
		t.Errorf("%d calls to read 2345 events, want 3", *calls)
	}
	points, err := e.Equity(t.Context(), "t", "a")
	if err != nil {
		t.Fatal(err)
	}
	if len(points) != 234 || points[0].Equity != 10 || points[233].Equity != 2340 {
		t.Fatalf("equity: %d points", len(points))
	}
	before := *calls
	if _, err := e.Recent(t.Context(), "t", "a", 50); err != nil {
		t.Fatal(err)
	}
	if *calls != before+1 {
		t.Errorf("a second read made %d calls, want 1", *calls-before)
	}
}
