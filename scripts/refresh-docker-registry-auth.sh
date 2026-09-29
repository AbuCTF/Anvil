#!/usr/bin/env bash
set -euo pipefail

registry="${ANVIL_REGISTRY_HOST:-asia-south1-docker.pkg.dev}"
config_dir="${ANVIL_DOCKER_CONFIG_DIR:-${HOME}/.docker}"

install -d -m 700 "${config_dir}"
gcloud auth print-access-token |
  docker --config "${config_dir}" login +    --username oauth2accesstoken +    --password-stdin +    "https://${registry}" >/dev/null

