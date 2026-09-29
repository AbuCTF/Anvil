#!/usr/bin/env bash
set -euo pipefail

registry="${ANVIL_REGISTRY_HOST:-asia-south1-docker.pkg.dev}"
config_dir="${ANVIL_DOCKER_CONFIG_DIR:-${HOME}/.docker}"
gcloud_bin="${ANVIL_GCLOUD_BIN:-${HOME}/google-cloud-sdk/bin/gcloud}"

if [[ ! -x "${gcloud_bin}" ]]; then
  gcloud_bin="$(command -v gcloud || true)"
fi
if [[ -z "${gcloud_bin}" || ! -x "${gcloud_bin}" ]]; then
  echo "gcloud executable not found" >&2
  exit 1
fi

install -d -m 700 "${config_dir}"
"${gcloud_bin}" auth print-access-token | docker --config "${config_dir}" login --username oauth2accesstoken --password-stdin "https://${registry}" >/dev/null
