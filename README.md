# app

The Candylario app: accounts, tiers, onboarding and dashboards for the trading agents. Server-rendered Go pages with htmx and its own Postgres. Agents, keys and plans live in the engine, reached only through the [engine API](https://github.com/candylario/trader/blob/main/api/openapi.yaml); Telegram chats connect through the notifier's API.

- **Accounts:** email and password. The address is confirmed by an emailed link before anything else, and then the account gets an engine tenant on the trial tier's plan. A forgotten password is reset by email, and a changed one is reported by email. Mail goes through Mailjet, whose bounce, blocked and spam reports (`/webhooks/mailjet`, basic auth `mailjet:MAILJET_WEBHOOK_SECRET`) show on the confirm page.
- **Tiers:** trial (test funds, BTC and ETH, 1 agent, a decision every 4h), standard (real money allowed) and pro (more coins, 3 agents, hourly). Set by hand until payments exist: `docker compose run --rm app tier set --email EMAIL --tier pro`.
- **Onboarding:** keys (an Anthropic API key, and a Binance key for the testnet or real money) go straight to the engine's vault, which tries them; then a first agent with its coins, capital and schedule; then a Telegram chat.
- **Dashboard:** each agent's equity against buy and hold, positions, the last decision, hourly equity, the audit log, and the controls: decide now, stop or resume trading, sell everything, start or stop the agent.

## Run

```sh
cp .env.example .env     # ENGINE_API_TOKEN as in the engine; NOTIFIER_API_TOKEN as in the notifier; POSTGRES_PASSWORD
docker compose up -d --build                      # needs the shared network: docker network create candylario
open http://127.0.0.1:3000
```

On a server prepared by [candylario/infra](https://github.com/candylario/infra): `./deploy.sh deploy@SERVER_IP`. For HTTPS, point a domain's A record at the server and set `APP_DOMAIN`, `APP_BASE_URL=https://DOMAIN` and `COMPOSE_PROFILES=public` in its settings; Caddy gets the certificate and redirects `www.`.

The engine's `api` package is vendored (`go mod vendor`) so the image builds without access to the private module. Tests need Postgres: `TEST_DATABASE_URL=postgres://... go test ./...`
