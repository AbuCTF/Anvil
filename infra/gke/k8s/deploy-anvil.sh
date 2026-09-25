#!/usr/bin/env bash
# Deploy/refresh Anvil on GKE. Builds config from Terraform outputs, creates the
# config + secret, applies the manifests. Re-runnable (keeps the existing JWT).
#   ./deploy-anvil.sh
set -euo pipefail
cd "$(dirname "$0")/.."   # -> infra/gke
export GOOGLE_OAUTH_ACCESS_TOKEN="$(gcloud auth print-access-token)"

DB_HOST=$(terraform output -raw db_host)
DB_PASS=$(terraform output -raw db_password)
REDIS_HOST=$(terraform output -raw redis_host)

kubectl create namespace anvil --dry-run=client -o yaml | kubectl apply -f -

# the secret also carries keys added by hand (sso, discord, instancer hmac,
# zeropool api key); only create it when missing so a re-run can't drop them.
if ! kubectl get secret anvil-secrets -n anvil >/dev/null 2>&1; then
  kubectl create secret generic anvil-secrets -n anvil \
    --from-literal=ANVIL_JWT_SECRET="$(openssl rand -hex 32)" \
    --from-literal=ANVIL_DATABASE_PASSWORD="$DB_PASS"
fi

kubectl create configmap anvil-config -n anvil \
  --from-literal=ANVIL_ENVIRONMENT=production \
  --from-literal=ANVIL_ENV=production \
  --from-literal=ANVIL_SERVER_HOST=0.0.0.0 \
  --from-literal=ANVIL_SERVER_PORT=8080 \
  --from-literal=ANVIL_SERVER_TRUSTED_PROXIES=100.64.0.0/14 \
  --from-literal=ANVIL_DATABASE_HOST="$DB_HOST" \
  --from-literal=ANVIL_DATABASE_PORT=5432 \
  --from-literal=ANVIL_DATABASE_USER=anvil \
  --from-literal=ANVIL_DATABASE_DATABASE=anvil \
  --from-literal=ANVIL_DATABASE_SSL_MODE=disable \
  --from-literal=ANVIL_DATABASE_MAX_OPEN_CONNS=40 \
  --from-literal=ANVIL_DATABASE_MAX_IDLE_CONNS=5 \
  --from-literal=ANVIL_REDIS_HOST="$REDIS_HOST" \
  --from-literal=ANVIL_REDIS_PORT=6379 \
  --from-literal=ANVIL_JWT_ACCESS_EXPIRY=12h \
  --from-literal=ANVIL_RATE_LIMIT_REQUESTS_PER_MINUTE=1200 \
  --from-literal=ANVIL_RATE_LIMIT_BURST_SIZE=300 \
  --from-literal=ANVIL_RATE_LIMIT_INSTANCE_START_REQUESTS=10 \
  --from-literal=ANVIL_PLATFORM_NAME=H7CTF \
  --from-literal=ANVIL_PLATFORM_REGISTRATION_MODE=disabled \
  --from-literal=ANVIL_PLATFORM_SCORING_ENABLED=true \
  --from-literal=ANVIL_PLATFORM_SCOREBOARD_ENABLED=true \
  --from-literal=ANVIL_PLATFORM_REGISTER_URL=https://2026.h7tex.com \
  --from-literal=ANVIL_ZEROPOOL_BASE_URL=https://app.h7tex.com \
  --from-literal=ANVIL_ZEROPOOL_EVENT_SLUG=h7ctf-2026 \
  --from-literal=ANVIL_SSO_ENABLED=true \
  --from-literal=ANVIL_DISCORD_ENABLED=true \
  --from-literal=ANVIL_DISCORD_CLIENT_ID=1547022816937771028 \
  --from-literal=ANVIL_DISCORD_REDIRECT_URI=https://ctf.h7tex.com/auth/discord/callback \
  --from-literal=ANVIL_INSTANCER_BACKEND=k8s \
  --from-literal=ANVIL_INSTANCER_BASE_DOMAIN=h7tex.com \
  --from-literal=ANVIL_INSTANCER_TIMEOUT=1h \
  --from-literal=ANVIL_VPN_ENABLED=false \
  --dry-run=client -o yaml | kubectl apply -f -

kubectl apply -f k8s/anvil/anvil.yaml
kubectl -n anvil rollout restart deploy/anvil-api deploy/anvil-web
kubectl -n anvil rollout status deploy/anvil-api --timeout=180s
kubectl -n anvil rollout status deploy/anvil-web --timeout=180s
echo "--- anvil pods ---"
kubectl get pods -n anvil
