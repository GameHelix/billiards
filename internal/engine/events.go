package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/candylario/trader/api"
)

// Events reads agents' audit logs and keeps what the dashboard shows: the
// recent events and the hourly equity of each agent, fetched from where
// the last fetch ended. The engine lists events oldest first.
type Events struct {
	Client *api.Client

	mu   sync.Mutex
	logs map[string]*log
}

type log struct {
	mu     sync.Mutex
	head   int64
	events []api.Event
	keep   int // 0 keeps everything
}

const (
	pageSize   = 1000
	keepRecent = 500
	equityType = "equity.hourly"
)

// Point is an agent's equity at one hour, against buying and holding.
type Point struct {
	At         time.Time
	Equity     float64
	BuyAndHold float64
}

func (e *Events) log(key string, keep int) *log {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.logs == nil {
		e.logs = map[string]*log{}
	}
	l := e.logs[key]
	if l == nil {
		l = &log{keep: keep}
		e.logs[key] = l
	}
	return l
}

// Recent returns the agent's last n events, newest first.
func (e *Events) Recent(ctx context.Context, tenant, agent string, n int) ([]api.Event, error) {
	events, err := e.fetch(ctx, e.log(agent, keepRecent), tenant, agent, "")
	if err != nil {
		return nil, err
	}
	if len(events) > n {
		events = events[len(events)-n:]
	}
	slices.Reverse(events)
	return events, nil
}

// Equity returns the agent's hourly equity, oldest first.
func (e *Events) Equity(ctx context.Context, tenant, agent string) ([]Point, error) {
	events, err := e.fetch(ctx, e.log(agent+"|"+equityType, 0), tenant, agent, equityType)
	if err != nil {
		return nil, err
	}
	points := make([]Point, 0, len(events))
	for _, ev := range events {
		var p struct {
			Equity     string `json:"equity"`
			BuyAndHold string `json:"buy_and_hold"`
		}
		if json.Unmarshal(ev.Payload, &p) != nil {
			continue
		}
		eq, err1 := strconv.ParseFloat(p.Equity, 64)
		bh, err2 := strconv.ParseFloat(p.BuyAndHold, 64)
		if err1 != nil || err2 != nil {
			continue
		}
		points = append(points, Point{At: ev.At, Equity: eq, BuyAndHold: bh})
	}
	return points, nil
}

// fetch brings the log up to date. With types, only those event types are
// kept; the engine filters by type too when it can.
func (e *Events) fetch(ctx context.Context, l *log, tenant, agent, types string) ([]api.Event, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	for {
		batch, err := e.list(ctx, tenant, agent, l.head, types)
		if err != nil {
			return nil, err
		}
		for _, ev := range batch {
			if types == "" || slices.Contains(strings.Split(types, ","), ev.Type) {
				l.events = append(l.events, ev)
			}
		}
		if len(batch) > 0 {
			l.head = batch[len(batch)-1].Seq
		}
		if len(batch) < pageSize {
			break
		}
	}
	if l.keep > 0 && len(l.events) > l.keep {
		l.events = slices.Clone(l.events[len(l.events)-l.keep:])
	}
	return slices.Clone(l.events), nil
}

func (e *Events) list(ctx context.Context, tenant, agent string, after int64, types string) ([]api.Event, error) {
	q := url.Values{"after": {strconv.FormatInt(after, 10)}, "limit": {strconv.Itoa(pageSize)}}
	if types != "" {
		q.Set("type", types)
	}
	u := strings.TrimRight(e.Client.BaseURL, "/") + "/v1/tenants/" + url.PathEscape(tenant) + "/agents/" + url.PathEscape(agent) + "/events?" + q.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+e.Client.Token)
	hc := e.Client.HTTP
	if hc == nil {
		hc = http.DefaultClient
	}
	resp, err := hc.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		var body struct {
			Error *api.Error `json:"error"`
		}
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<16))
		if json.Unmarshal(raw, &body) != nil || body.Error == nil {
			return nil, &api.Error{Status: resp.StatusCode, Code: "internal", Message: http.StatusText(resp.StatusCode)}
		}
		body.Error.Status = resp.StatusCode
		return nil, body.Error
	}
	var out api.EventList
	return out.Events, json.NewDecoder(resp.Body).Decode(&out)
}

// Summary is one line about an event, for the dashboard. Payload text is
// the model's or the news' and must be shown as plain text.
func Summary(e api.Event) string {
	var p map[string]any
	if json.Unmarshal(e.Payload, &p) != nil {
		return ""
	}
	str := func(k string) string {
		s, _ := p[k].(string)
		return s
	}
	switch e.Type {
	case "notice":
		return str("text")
	case "decision.started":
		return strings.TrimSpace(str("kind") + ": " + str("trigger"))
	case "decision.finished":
		var parts []string
		if signals, ok := p["signals"].(map[string]any); ok {
			for _, sym := range sortedKeys(signals) {
				if sig, ok := signals[sym].(map[string]any); ok {
					r, _ := sig["rating"].(string)
					parts = append(parts, sym+" "+strings.ReplaceAll(r, "_", " "))
				}
			}
		}
		if cost, ok := p["cost_usd"].(float64); ok {
			parts = append(parts, fmt.Sprintf("$%.2f", cost))
		}
		return strings.Join(parts, " · ")
	case "decision.failed":
		return str("error")
	case "order.intent":
		return strings.TrimSpace(fmt.Sprintf("%s %s %v", str("side"), str("symbol"), p["qty"]))
	case "order.result":
		return strings.TrimSpace(str("outcome") + " " + str("error"))
	case "trade.booked":
		if t, ok := p["trade"].(map[string]any); ok {
			s := fmt.Sprintf("%v %v %v at %v", t["side"], t["qty"], t["symbol"], t["avg_price"])
			if q, ok := t["quote_qty"].(string); ok {
				s += " for " + q + " USDT"
			}
			return s
		}
	case "trading.stopped":
		return strings.TrimSpace(str("kind") + ": " + str("reason"))
	case "trading.resumed":
		return "by " + str("by")
	case "command.received":
		s := str("name")
		if via := str("via"); via != "" {
			s += " via " + via
		}
		if p["ignored_as_stale"] == true {
			s += " (ignored, sent while offline)"
		}
		return s
	case "equity.hourly":
		return fmt.Sprintf("equity %v, buy and hold %v", p["equity"], p["buy_and_hold"])
	case "news.alert":
		if items, ok := p["items"].([]any); ok && len(items) > 0 {
			if it, ok := items[0].(map[string]any); ok {
				t, _ := it["title"].(string)
				return t
			}
		}
	case "agent.started", "agent.stopped":
		s := strings.TrimSpace(fmt.Sprintf("cash %v %s", p["cash"], str("halted")))
		if by := str("by"); by != "" {
			s += " · by " + by
		}
		return s
	case "model.call":
		s := strings.TrimSpace(str("purpose") + " · " + str("status"))
		if cost, ok := p["cost_usd"].(float64); ok {
			s += fmt.Sprintf(" · $%.4f", cost)
		}
		if ms, ok := p["latency_ms"].(float64); ok {
			s += fmt.Sprintf(" · %.1f s", ms/1000)
		}
		if err := str("error"); err != "" {
			s += " · " + err
		}
		return s
	case "call.graded":
		ret, _ := p["return"].(float64)
		verdict := "wrong"
		if p["hit"] == true {
			verdict = "right"
		}
		return fmt.Sprintf("%s %s: %s, price %+.2f%%", str("symbol"), strings.ReplaceAll(str("rating"), "_", " "), verdict, ret*100)
	case "lesson.recorded":
		if l, ok := p["lesson"].(map[string]any); ok {
			s, _ := l["text"].(string)
			return s
		}
	}
	return compact(p, sortedKeys(p)...)
}

func sortedKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// compact shows up to four scalar fields as "key value".
func compact(p map[string]any, keys ...string) string {
	var parts []string
	for _, k := range keys {
		switch v := p[k].(type) {
		case string, float64, bool:
			parts = append(parts, fmt.Sprintf("%s %v", k, v))
		}
		if len(parts) == 4 {
			break
		}
	}
	s := strings.Join(parts, " · ")
	if r := []rune(s); len(r) > 140 {
		s = string(r[:140]) + "…"
	}
	return s
}
