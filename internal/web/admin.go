package web

import (
	"net/http"
	"slices"

	"github.com/candylario/app/internal/store"
)

// The platform admin's page: every account with its address. Admins are
// the confirmed accounts whose address is in ADMIN_EMAILS; nothing in the
// app can make anyone else one. An admin runs the platform and does not
// trade on it: no tenant and no Telegram.

func (s *Server) isAdmin(u store.User) bool {
	return u.Verified() && s.adminAddress(u.Email)
}

func (s *Server) adminAddress(email string) bool { return slices.Contains(s.Admins, email) }

type adminRow struct {
	User  store.User
	Admin bool
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
	rows := make([]adminRow, len(users))
	for i, u := range users {
		rows[i] = adminRow{User: u, Admin: s.adminAddress(u.Email)}
	}
	s.render(w, r, "admin.html", status, page{Title: "Admin", Error: problem, Data: rows})
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
