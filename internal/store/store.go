// Package store keeps the app's accounts and sessions in its own Postgres
// database. Keys and agents live in the engine; the app only remembers
// which tenant is whose.
package store

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"embed"
	"encoding/base64"
	"errors"
	"fmt"
	"io/fs"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

//go:embed migrations/*.sql
var migrations embed.FS

var (
	ErrNotFound   = errors.New("not found")
	ErrEmailTaken = errors.New("an account with this email already exists")
)

type Store struct{ pool *pgxpool.Pool }

func Open(ctx context.Context, url string) (*Store, error) {
	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		return nil, errors.New("invalid DATABASE_URL")
	}
	return &Store{pool: pool}, nil
}

func (s *Store) Close() { s.pool.Close() }

func (s *Store) Ping(ctx context.Context) error { return s.pool.Ping(ctx) }

// Migrate applies the embedded migrations in order, once each.
func Migrate(ctx context.Context, url string) error {
	conn, err := pgx.Connect(ctx, url)
	if err != nil {
		return fmt.Errorf("database: %w", err)
	}
	defer conn.Close(ctx)
	if _, err := conn.Exec(ctx, "select pg_advisory_lock(hashtext('app-migrations'))"); err != nil {
		return err
	}
	if _, err := conn.Exec(ctx, "create table if not exists schema_migrations (version int primary key, applied_at timestamptz not null default now())"); err != nil {
		return err
	}
	names, err := fs.Glob(migrations, "migrations/*.sql")
	if err != nil {
		return err
	}
	sort.Strings(names)
	for _, name := range names {
		version, err := strconv.Atoi(strings.SplitN(strings.TrimPrefix(name, "migrations/"), "_", 2)[0])
		if err != nil {
			return fmt.Errorf("migration %s: bad name", name)
		}
		sql, err := migrations.ReadFile(name)
		if err != nil {
			return err
		}
		err = pgx.BeginFunc(ctx, conn, func(tx pgx.Tx) error {
			tag, err := tx.Exec(ctx, "insert into schema_migrations (version) values ($1) on conflict do nothing", version)
			if err != nil || tag.RowsAffected() == 0 {
				return err
			}
			_, err = tx.Exec(ctx, string(sql))
			return err
		})
		if err != nil {
			return fmt.Errorf("migration %s: %w", name, err)
		}
	}
	return nil
}

// User is an account. TenantID is its engine tenant, created by the app
// with that id; TenantReady says the engine has confirmed it.
type User struct {
	ID           uuid.UUID
	Email        string
	PasswordHash string
	TenantID     uuid.UUID
	TenantReady  bool
	Tier         string
	CreatedAt    time.Time
}

const userCols = "id, email, password_hash, tenant_id, tenant_ready, tier, created_at"

func scanUser(r pgx.Row) (User, error) {
	var u User
	err := r.Scan(&u.ID, &u.Email, &u.PasswordHash, &u.TenantID, &u.TenantReady, &u.Tier, &u.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return u, ErrNotFound
	}
	return u, err
}

func newID() uuid.UUID {
	id, err := uuid.NewV7()
	if err != nil {
		return uuid.New()
	}
	return id
}

// CreateUser adds an account on the tier, with a new tenant id the engine
// has not seen yet.
func (s *Store) CreateUser(ctx context.Context, email, passwordHash, tier string) (User, error) {
	u := User{ID: newID(), Email: email, PasswordHash: passwordHash, TenantID: newID(), Tier: tier}
	err := s.pool.QueryRow(ctx, "insert into users (id, email, password_hash, tenant_id, tier) values ($1, $2, $3, $4, $5) returning created_at",
		u.ID, u.Email, u.PasswordHash, u.TenantID, u.Tier).Scan(&u.CreatedAt)
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" {
		return u, ErrEmailTaken
	}
	return u, err
}

func (s *Store) UserByEmail(ctx context.Context, email string) (User, error) {
	return scanUser(s.pool.QueryRow(ctx, "select "+userCols+" from users where email = $1", email))
}

func (s *Store) UserByID(ctx context.Context, id uuid.UUID) (User, error) {
	return scanUser(s.pool.QueryRow(ctx, "select "+userCols+" from users where id = $1", id))
}

func (s *Store) Users(ctx context.Context) ([]User, error) {
	rows, err := s.pool.Query(ctx, "select "+userCols+" from users order by created_at")
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (User, error) { return scanUser(r) })
}

func (s *Store) SetTenantReady(ctx context.Context, id uuid.UUID) error {
	_, err := s.pool.Exec(ctx, "update users set tenant_ready = true where id = $1", id)
	return err
}

func (s *Store) SetTier(ctx context.Context, id uuid.UUID, tier string) error {
	_, err := s.pool.Exec(ctx, "update users set tier = $2 where id = $1", id, tier)
	return err
}

func (s *Store) SetPassword(ctx context.Context, id uuid.UUID, passwordHash string) error {
	_, err := s.pool.Exec(ctx, "update users set password_hash = $2 where id = $1", id, passwordHash)
	return err
}

// Session is a logged-in browser. CSRF is the token its forms must carry.
type Session struct {
	UserID    uuid.UUID
	CSRF      string
	ExpiresAt time.Time
}

func random() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

func hashToken(token string) []byte {
	h := sha256.Sum256([]byte(token))
	return h[:]
}

// CreateSession starts a session and returns its token, which the browser
// holds, and its CSRF token.
func (s *Store) CreateSession(ctx context.Context, user uuid.UUID, ttl time.Duration) (token, csrf string, err error) {
	if token, err = random(); err != nil {
		return "", "", err
	}
	if csrf, err = random(); err != nil {
		return "", "", err
	}
	_, err = s.pool.Exec(ctx, "insert into sessions (id, user_id, csrf, expires_at) values ($1, $2, $3, $4)", hashToken(token), user, csrf, time.Now().Add(ttl))
	return token, csrf, err
}

// Session finds a live session by its token, with its user.
func (s *Store) Session(ctx context.Context, token string) (Session, User, error) {
	var sess Session
	var u User
	err := s.pool.QueryRow(ctx, `select s.user_id, s.csrf, s.expires_at, u.id, u.email, u.password_hash, u.tenant_id, u.tenant_ready, u.tier, u.created_at
		from sessions s join users u on u.id = s.user_id where s.id = $1 and s.expires_at > now()`, hashToken(token)).
		Scan(&sess.UserID, &sess.CSRF, &sess.ExpiresAt, &u.ID, &u.Email, &u.PasswordHash, &u.TenantID, &u.TenantReady, &u.Tier, &u.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return sess, u, ErrNotFound
	}
	return sess, u, err
}

func (s *Store) DeleteSession(ctx context.Context, token string) error {
	_, err := s.pool.Exec(ctx, "delete from sessions where id = $1", hashToken(token))
	return err
}

// DeleteOtherSessions logs the user out everywhere but the session with
// token, for example after a password change.
func (s *Store) DeleteOtherSessions(ctx context.Context, user uuid.UUID, token string) error {
	_, err := s.pool.Exec(ctx, "delete from sessions where user_id = $1 and id <> $2", user, hashToken(token))
	return err
}
