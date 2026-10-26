-- An account's email is confirmed by a link sent to it. email_problem is the
-- last delivery failure the mail provider reported for the address.
alter table users add column email_verified_at timestamptz;
alter table users add column email_problem text not null default '';

-- Links sent by email: confirming the address, or setting a new password.
-- Only a hash of each token is kept, and a token works once, until it
-- expires.
create table email_tokens (
  id bytea primary key,
  user_id uuid not null references users (id) on delete cascade,
  purpose text not null check (purpose in ('verify', 'reset')),
  email text not null,
  expires_at timestamptz not null,
  used_at timestamptz,
  created_at timestamptz not null default now()
);
create index email_tokens_user on email_tokens (user_id, purpose);
