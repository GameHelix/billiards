package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/candylario/trader/api"

	"github.com/candylario/app/internal/store"
)

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
	return url
}

func TestTierSetGivesTheEngineTheNewPlan(t *testing.T) {
	url := testDatabase(t)
	var plans []api.Plan
	engine := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer tok" {
			w.WriteHeader(401)
			return
		}
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/v1/tenants":
			var in api.NewTenant
			json.NewDecoder(r.Body).Decode(&in)
			plans = append(plans, in.Plan)
			w.WriteHeader(201)
			json.NewEncoder(w).Encode(api.Tenant{ID: in.ID, Name: in.Name, Status: "active", Plan: in.Plan})
		case r.Method == http.MethodPut && strings.HasSuffix(r.URL.Path, "/plan"):
			var p api.Plan
			json.NewDecoder(r.Body).Decode(&p)
			plans = append(plans, p)
			json.NewEncoder(w).Encode(api.PlanChange{Plan: p, Stopped: []api.Stopped{{AgentID: "a1", Reason: "the plan allows 1 agent"}}})
		default:
			w.WriteHeader(404)
		}
	}))
	defer engine.Close()
	t.Setenv("DATABASE_URL", url)
	t.Setenv("ENGINE_API_URL", engine.URL)
	t.Setenv("ENGINE_API_TOKEN", "tok")
	ctx := context.Background()
	if err := store.Migrate(ctx, url); err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if _, err := st.CreateUser(ctx, "jo@example.com", "x", "trial"); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := run(ctx, []string{"tier", "set", "--email", "jo@example.com", "--tier", "pro"}, &out); err != nil {
		t.Fatal(err)
	}
	// The tenant comes once the address is confirmed, on the tier set by then.
	u, _ := st.UserByEmail(ctx, "jo@example.com")
	if len(plans) != 0 || u.Tier != "pro" {
		t.Fatalf("before the tenant: plans %+v, tier %s", plans, u.Tier)
	}
	if err := st.SetTenantReady(ctx, u.ID); err != nil {
		t.Fatal(err)
	}
	if err := run(ctx, []string{"tier", "set", "--email", "jo@example.com", "--tier", "standard"}, &out); err != nil {
		t.Fatal(err)
	}
	if len(plans) != 1 || plans[0].MaxAgents != 1 || !plans[0].Live || !strings.Contains(out.String(), "stopped agent a1") {
		t.Fatalf("plan: %+v\n%s", plans, out.String())
	}
	u, _ = st.UserByEmail(ctx, "jo@example.com")
	if u.Tier != "standard" || !u.TenantReady {
		t.Fatalf("user: %+v", u)
	}
	if err := run(ctx, []string{"tier", "set", "--email", "jo@example.com", "--tier", "gold"}, &out); err == nil {
		t.Fatal("unknown tier accepted")
	}
	out.Reset()
	if err := run(ctx, []string{"users"}, &out); err != nil || !strings.Contains(out.String(), "jo@example.com\tunconfirmed\tstandard") {
		t.Fatalf("users: %v %s", err, out.String())
	}
}
