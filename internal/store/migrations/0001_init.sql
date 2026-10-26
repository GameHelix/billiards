-- Accounts and their sessions. Each account owns one engine tenant, chosen
-- by the app so that creating it can be retried.

create table users (
  id uuid primary key,
  email text not null unique,
  password_hash text not null,
  tenant_id uuid not null unique,
  tenant_ready boolean not null default false,
  tier text not null,
  created_at timestamptz not null default now()
);

-- Only a hash of each session token is kept.
create table sessions (
  id bytea primary key,
  user_id uuid not null references users (id) on delete cascade,
  csrf text not null,
  created_at timestamptz not null default now(),
  expires_at timestamptz not null
);
create index sessions_user on sessions (user_id);
