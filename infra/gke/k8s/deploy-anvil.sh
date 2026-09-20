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

# stable JWT: reuse the existing one if the secret already exists.
if kubectl get secret anvil-secrets -n anvil >/dev/null 2>&1; then
  JWT=$(kubectl get secret anvil-secrets -n anvil -o jsonpath='{.data.ANVIL_JWT_SECRET}' | base64 -d)
else
  JWT=$(openssl rand -hex 32)
fi

kubectl create secret generic anvil-secrets -n anvil \
  --from-literal=ANVIL_JWT_SECRET="$JWT" \
  --from-literal=ANVIL_DATABASE_PASSWORD="$DB_PASS" \
  --dry-run=client -o yaml | kubectl apply -f -

kubectl create configmap anvil-config -n anvil \
  --from-literal=ANVIL_ENVIRONMENT=production \
  --from-literal=ANVIL_ENV=production \
  --from-literal=ANVIL_SERVER_HOST=0.0.0.0 \
  --from-literal=ANVIL_SERVER_PORT=8080 \
  --from-literal=ANVIL_DATABASE_HOST="$DB_HOST" \
  --from-literal=ANVIL_DATABASE_PORT=5432 \
  --from-literal=ANVIL_DATABASE_USER=anvil \
  --from-literal=ANVIL_DATABASE_DATABASE=anvil \
  --from-literal=ANVIL_DATABASE_SSL_MODE=disable \
  --from-literal=ANVIL_REDIS_HOST="$REDIS_HOST" \
  --from-literal=ANVIL_REDIS_PORT=6379 \
  --from-literal=ANVIL_PLATFORM_NAME=H7CTF \
  --from-literal=ANVIL_PLATFORM_REGISTRATION_MODE=open \
  --from-literal=ANVIL_PLATFORM_SCORING_ENABLED=true \
  --from-literal=ANVIL_PLATFORM_SCOREBOARD_ENABLED=true \
  --from-literal=ANVIL_PLATFORM_REGISTER_URL=https://2026.h7tex.com \
  --from-literal=ANVIL_ZEROPOOL_BASE_URL=https://app.h7tex.com \
  --from-literal=ANVIL_ZEROPOOL_EVENT_SLUG=h7ctf-2026 \
  --from-literal=ANVIL_VPN_ENABLED=false \
  --dry-run=client -o yaml | kubectl apply -f -

kubectl apply -f k8s/anvil/anvil.yaml
kubectl -n anvil rollout restart deploy/anvil-api deploy/anvil-web
kubectl -n anvil rollout status deploy/anvil-api --timeout=180s
kubectl -n anvil rollout status deploy/anvil-web --timeout=180s
echo "--- anvil pods ---"
kubectl get pods -n anvil
