# app

The Candylario app: the public pages (the radar, its alerts and their records, the legal texts), accounts, and connecting a Telegram chat to an account. Server-rendered Go pages with htmx and its own Postgres. Accounts get a tenant in the engine, reached only through the [engine API](https://github.com/candylario/trader/blob/main/api/openapi.yaml); Telegram chats connect through the notifier's API.

- **Accounts:** email and password. The address is confirmed by an emailed link before anything else, and then the account gets an engine tenant. A forgotten password is reset by email, and a changed one is reported by email. Mail goes through Mailjet, whose bounce, blocked and spam reports (`/webhooks/mailjet`, basic auth `mailjet:MAILJET_WEBHOOK_SECRET`) show on the confirm page.
- **Telegram:** a one-time link connects a private chat to the account; `/telegram` lists and disconnects chats.
- **Admin:** `/admin` lists every account with its address. Admins are the confirmed accounts whose address is in `ADMIN_EMAILS`; nothing in the app can make anyone else one.
- **Deleting an account** closes its engine tenant, disconnects its chats and forgets it.

## Run

```sh
cp .env.example .env     # ENGINE_API_TOKEN as in the engine; NOTIFIER_API_TOKEN as in the notifier; POSTGRES_PASSWORD
docker compose up -d --build                      # needs the shared network: docker network create candylario
open http://127.0.0.1:3000
```

On a server prepared by [candylario/infra](https://github.com/candylario/infra): `./deploy.sh deploy@SERVER_IP` deploys the pushed commit, and `./deploy.sh --rollback deploy@SERVER_IP` runs the previous image again. `/healthz` reports the commit each service runs. For HTTPS, point a domain's A record at the server and set `APP_DOMAIN`, `APP_BASE_URL=https://DOMAIN` and `COMPOSE_PROFILES=public` in its settings; Caddy gets the certificate and redirects `www.`.

The engine's `api` package is vendored (`go mod vendor`) so the image builds without access to the private module. Tests need Postgres: `TEST_DATABASE_URL=postgres://... go test ./...`
