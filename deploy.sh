#!/usr/bin/env bash
# Deploys the app to a server prepared by candylario/infra: builds the image
# for the server's CPU, copies it over SSH with compose.yaml, the Caddyfile
# and its settings, and restarts the stack. Its database stays on the server.
#
#   ./deploy.sh deploy@SERVER
# shellcheck disable=SC2029 # $dir is meant to expand here, before ssh.
set -euo pipefail
host=${1:?usage: ./deploy.sh deploy@SERVER}
dir=/srv/app
cd "$(dirname "$0")"
# The settings come from the one file next to the repos when infra is there.
if [ -x ../infra/env.sh ]; then
  settings=$(../infra/env.sh app)
elif [ -f .env ]; then
  settings=$(cat .env)
else
  echo "no settings: neither ../infra/env.sh nor .env" >&2
  exit 1
fi

case $(ssh "$host" uname -m) in
x86_64) platform=linux/amd64 ;;
aarch64 | arm64) platform=linux/arm64 ;;
*)
  echo "unsupported server CPU" >&2
  exit 1
  ;;
esac

docker buildx build --platform "$platform" --tag app:deploy --load .
ssh "$host" "mkdir -p $dir"
docker save app:deploy | gzip | ssh "$host" 'gunzip | docker load --quiet && docker tag app:deploy app:local'
scp -q compose.yaml Caddyfile "$host:$dir/"
ssh "$host" "umask 077 && cat >$dir/.env" <<<"$settings"
ssh "$host" "docker network inspect candylario >/dev/null 2>&1 || docker network create candylario >/dev/null"
ssh "$host" "cd $dir && docker compose up -d --no-build --remove-orphans && docker image prune -f >/dev/null"
echo "Deployed to $host. Logs: ssh $host 'cd $dir && docker compose logs -f app'"
