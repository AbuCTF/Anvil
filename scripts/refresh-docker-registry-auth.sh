#!/usr/bin/env bash
# Write a fresh Artifact Registry login into the docker config the API mounts.
# gcloud returns its cached access token until it is nearly expired, so a plain
# print-access-token can hand back a token with minutes left. Swarm workers then
# fail to pull ("No such image") once it lapses. Evict any cached token with less
# than MIN_TTL seconds left so the written token always outlives the next run.
set -euo pipefail

registry="${ANVIL_REGISTRY_HOST:-asia-south1-docker.pkg.dev}"
config_dir="${ANVIL_DOCKER_CONFIG_DIR:-${HOME}/.docker}"
gcloud_bin="${ANVIL_GCLOUD_BIN:-${HOME}/google-cloud-sdk/bin/gcloud}"
min_ttl="${ANVIL_REGISTRY_TOKEN_MIN_TTL:-900}"
token_cache="${CLOUDSDK_CONFIG:-${HOME}/.config/gcloud}/access_tokens.db"

if [[ ! -x "${gcloud_bin}" ]]; then
  gcloud_bin="$(command -v gcloud || true)"
fi
if [[ -z "${gcloud_bin}" || ! -x "${gcloud_bin}" ]]; then
  echo "gcloud executable not found" >&2
  exit 1
fi

if [[ -f "${token_cache}" ]] && command -v python3 >/dev/null; then
  python3 - "${token_cache}" "${min_ttl}" <<'PY'
import datetime, sqlite3, sys
path, min_ttl = sys.argv[1], int(sys.argv[2])
cutoff = datetime.datetime.utcnow() + datetime.timedelta(seconds=min_ttl)
db = sqlite3.connect(path, timeout=10)
with db:
    db.execute("DELETE FROM access_tokens WHERE token_expiry IS NULL OR token_expiry < ?",
               (cutoff.strftime("%Y-%m-%d %H:%M:%S"),))
db.close()
PY
fi

install -d -m 700 "${config_dir}"
"${gcloud_bin}" auth print-access-token | docker --config "${config_dir}" login --username oauth2accesstoken --password-stdin "https://${registry}" >/dev/null
