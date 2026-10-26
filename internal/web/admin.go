package web

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"

	"github.com/google/uuid"

	"github.com/candylario/trader/api"

	"github.com/candylario/app/internal/store"
	"github.com/candylario/app/internal/tiers"
)

// The platform admin's page: every account with its address, tier and
// agents, and tier changes by hand until payments exist. Admins are the
// confirmed accounts whose address is in ADMIN_EMAILS; nothing in the app
// can make anyone else one. An admin runs the platform and does not trade on
// it: no tenant, keys, agents or Telegram.

func (s *Server) isAdmin(u store.User) bool {
	return u.Verified() && s.adminAddress(u.Email)
}

func (s *Server) adminAddress(email string) bool { return slices.Contains(s.Admins, email) }

type adminRow struct {
	User    store.User
	Admin   bool
	Agents  int
	Running int
	Problem string
}

type adminPage struct {
	Rows  []adminRow
	Tiers []tiers.Tier
}

func (s *Server) admin(w http.ResponseWriter, r *http.Request) {
	s.adminPage(w, r, http.StatusOK, "")
}

func (s *Server) adminPage(w http.ResponseWriter, r *http.Request, status int, problem string) {
	users, err := s.Store.Users(r.Context())
	if err != nil {
		s.fail(w, r, err)
		return
	}
	ctx, cancel := callCtx(r)
	defer cancel()
	rows := make([]adminRow, len(users))
	for i, u := range users {
		rows[i].User, rows[i].Admin = u, s.adminAddress(u.Email)
		if rows[i].Admin || !u.TenantReady {
			continue
		}
		agents, err := s.Engine.Agents(ctx, u.TenantID.String())
		if err != nil {
			rows[i].Problem = userMessage(err)
			continue
		}
		rows[i].Agents = len(agents)
		for _, a := range agents {
			if a.Running {
				rows[i].Running++
			}
		}
	}
	s.render(w, r, "admin.html", status, page{Title: "Admin", Error: problem, Data: adminPage{Rows: rows, Tiers: tiers.All}})
}

func (s *Server) adminSetTier(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		s.notFound(w, r)
		return
	}
	t, ok := tiers.Get(r.PostFormValue("tier"))
	if !ok {
		s.notFound(w, r)
		return
	}
	u, err := s.Store.UserByID(r.Context(), id)
	if errors.Is(err, store.ErrNotFound) {
		s.notFound(w, r)
		return
	}
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if s.adminAddress(u.Email) {
		s.flash(w, "Admins have no tier; they do not trade.")
		http.Redirect(w, r, "/admin", http.StatusSeeOther)
		return
	}
	ctx, cancel := callCtx(r)
	defer cancel()
	stopped, err := ChangeTier(ctx, s.Store, s.Engine, u, t)
	switch {
	case err != nil:
		s.Logf("tier of %s: %v", u.Email, err)
		s.flash(w, userMessage(err))
	case len(stopped) > 0:
		s.flash(w, fmt.Sprintf("%s is on %s; %d agent(s) stopped.", u.Email, t.Title, len(stopped)))
	default:
		s.flash(w, fmt.Sprintf("%s is on %s.", u.Email, t.Title))
	}
	http.Redirect(w, r, "/admin", http.StatusSeeOther)
}

func (s *Server) adminChangePassword(w http.ResponseWriter, r *http.Request) {
	switch problem, err := s.setPassword(r); {
	case err != nil:
		s.fail(w, r, err)
	case problem != "":
		s.adminPage(w, r, http.StatusUnprocessableEntity, problem)
	default:
		s.flash(w, "Password changed; other browsers were logged out.")
		http.Redirect(w, r, "/admin", http.StatusSeeOther)
	}
}

// ChangeTier moves an account to a tier and gives its engine tenant the new
// plan, which stops any agent the plan no longer allows. An account without
// a tenant yet gets it on this tier once its address is confirmed.
func ChangeTier(ctx context.Context, st *store.Store, engine *api.Client, u store.User, t tiers.Tier) ([]api.Stopped, error) {
	var stopped []api.Stopped
	if u.TenantReady {
		change, err := engine.SetPlan(ctx, u.TenantID.String(), t.Plan)
		if err != nil {
			return nil, fmt.Errorf("engine: %w", err)
		}
		stopped = change.Stopped
	}
	return stopped, st.SetTier(ctx, u.ID, t.Name)
}
