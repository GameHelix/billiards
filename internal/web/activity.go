package web

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"time"

	"github.com/candylario/trader/api"

	"github.com/candylario/app/internal/engine"
)

// eventView is an entry of an agent's audit log as the pages show it.
type eventView struct {
	Seq     int64
	At      time.Time
	Type    string
	Label   string
	Group   string
	Summary string
	Alert   bool
	Hash    string // the start of the hash that chains the entry to the one before
	Raw     json.RawMessage
}

func eventViews(events []api.Event) []eventView {
	views := make([]eventView, 0, len(events))
	for _, e := range events {
		views = append(views, eventView{Seq: e.Seq, At: e.At, Type: e.Type, Label: engine.Label(e.Type), Group: engine.GroupOf(e.Type),
			Summary: engine.Summary(e), Alert: engine.Alert(e), Hash: e.Hash[:min(16, len(e.Hash))], Raw: e.Payload})
	}
	return views
}

// updatesView is the agent page's latest messages from the agent, which
// tell all that matters; the rest is in the activity log.
type updatesView struct {
	Agent  string
	Events []eventView
}

const updatesShown = 5

func (s *Server) updates(ctx context.Context, tenant, agent string) updatesView {
	events, err := s.Events.Latest(ctx, tenant, agent, 0, []string{"notice"}, updatesShown)
	if err != nil {
		s.Logf("updates of %s: %v", agent, err)
	}
	return updatesView{Agent: agent, Events: eventViews(events)}
}

// link is a choice on a page, such as a filter.
type link struct {
	Title   string
	URL     string
	Current bool
}

type activityPage struct {
	Agent    api.Agent
	Agents   []link // every agent's log, when there are several
	Filters  []link
	Filtered bool
	Problem  string
	Rows     activityRows
}

// activityRows are entries of the log, newest first, and where to ask for
// more: newer ones on a timer, older ones on request.
type activityRows struct {
	Events []eventView
	Newer  string
	Older  string
	Start  bool // the entries reach back to the start
}

const entriesShown = 50

// logURL is the activity log of an agent, all of it or one group of
// events, with a cursor: "after" or "before" a seq, or none.
func logURL(agent, group, cursor string, seq int64) string {
	q := url.Values{"agent": {agent}}
	if group != "" {
		q.Set("show", group)
	}
	if cursor != "" {
		q.Set(cursor, strconv.FormatInt(seq, 10))
	}
	return "/activity?" + q.Encode()
}

// activity shows an agent's audit log, newest first: all of it, or one
// group of events. On the page, htmx asks for newer entries every 20
// seconds (after) and for older ones on request (before).
func (s *Server) activity(w http.ResponseWriter, r *http.Request) {
	tenant := current(r).User.TenantID.String()
	ctx, cancel := callCtx(r)
	defer cancel()
	agents, err := s.Engine.Agents(ctx, tenant)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if len(agents) == 0 {
		s.render(w, r, "activity.html", http.StatusOK, page{Title: "Activity", Data: activityPage{}})
		return
	}
	q := r.URL.Query()
	a := agents[0]
	if id := q.Get("agent"); id != "" {
		i := slices.IndexFunc(agents, func(a api.Agent) bool { return a.ID == id })
		if i < 0 {
			s.notFound(w, r)
			return
		}
		a = agents[i]
	}
	var group engine.Group
	if i := slices.IndexFunc(engine.Groups, func(g engine.Group) bool { return g.Name == q.Get("show") }); i >= 0 {
		group = engine.Groups[i]
	}
	more := r.Header.Get("HX-Request") == "true"

	if more && q.Has("after") {
		after, _ := strconv.ParseInt(q.Get("after"), 10, 64)
		events, err := s.Events.Since(ctx, tenant, a.ID, after, group.Types, entriesShown)
		if err != nil {
			s.Logf("activity of %s: %v", a.ID, err)
		}
		if len(events) > 0 {
			after = events[0].Seq
		}
		s.partial(w, "activity-rows", http.StatusOK, activityRows{Events: eventViews(events), Newer: logURL(a.ID, group.Name, "after", after)})
		return
	}
	before, _ := strconv.ParseInt(q.Get("before"), 10, 64)
	if !more {
		before = 0
	}
	events, err := s.Events.Latest(ctx, tenant, a.ID, before, group.Types, entriesShown+1)
	if err != nil {
		s.Logf("activity of %s: %v", a.ID, err)
		if before > 0 {
			http.Error(w, "The log is not available right now.", http.StatusServiceUnavailable)
			return
		}
	}
	rows := activityRows{Start: err == nil}
	if len(events) > entriesShown {
		events, rows.Start = events[:entriesShown], false
		rows.Older = logURL(a.ID, group.Name, "before", events[entriesShown-1].Seq)
	}
	rows.Events = eventViews(events)
	if before > 0 {
		s.partial(w, "activity-rows", http.StatusOK, rows)
		return
	}

	p := activityPage{Agent: a, Filtered: group.Name != "", Rows: rows,
		Filters: []link{{Title: "All", URL: logURL(a.ID, "", "", 0), Current: group.Name == ""}}}
	for _, g := range engine.Groups {
		p.Filters = append(p.Filters, link{Title: g.Title, URL: logURL(a.ID, g.Name, "", 0), Current: g.Name == group.Name})
	}
	if len(agents) > 1 {
		for _, o := range agents {
			p.Agents = append(p.Agents, link{Title: o.Name, URL: logURL(o.ID, group.Name, "", 0), Current: o.ID == a.ID})
		}
	}
	if err != nil {
		p.Problem = "The log is not available right now; try again in a minute."
	} else {
		var newest int64
		if len(events) > 0 {
			newest = events[0].Seq
		}
		p.Rows.Newer = logURL(a.ID, group.Name, "after", newest)
	}
	s.render(w, r, "activity.html", http.StatusOK, page{Title: "Activity", Data: p})
}
