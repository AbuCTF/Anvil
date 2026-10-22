#!/usr/bin/env bash
set -euo pipefail

script_dir=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
env_file=${1:-/etc/anvil/finals.env}

fail() {
    echo "preflight: $*" >&2
    exit 1
}

[[ -f "$env_file" ]] || fail "environment file not found: $env_file"
env_mode=$(stat -c '%a' "$env_file")
(( (8#$env_mode & 077) == 0 )) || fail "$env_file must not be readable by group or other users"
if (( EUID == 0 )); then
    [[ $(stat -c '%u' "$env_file") == 0 ]] || fail "$env_file must be owned by root"
fi

set -a
# shellcheck disable=SC1090
source "$env_file"
set +a

required=(
    ANVIL_PRIVATE_ADDRESS
    PUBLIC_API_URL
    ANVIL_IMAGE_REVISION
    ANVIL_DATABASE_PASSWORD
    ANVIL_JWT_SECRET
    ANVIL_SECRETS_ENCRYPTION_KEY
    ANVIL_GAME_INTERNAL_TOKEN
    ANVIL_VPN_ENABLED
    ANVIL_TLS_CERT_PATH
    ANVIL_TLS_KEY_PATH
    ANVIL_DATABASE_HOST_PATH
    ANVIL_STORAGE_HOST_PATH
    ANVIL_BACKUP_DIR
)
for name in "${required[@]}"; do
    [[ -n ${!name:-} ]] || fail "$name is required"
done

[[ $ANVIL_IMAGE_REVISION =~ ^[0-9a-f]{40}$ ]] ||
    fail "ANVIL_IMAGE_REVISION must be exactly 40 lowercase hexadecimal characters"

[[ $ANVIL_VPN_ENABLED == true || $ANVIL_VPN_ENABLED == false ]] ||
    fail "ANVIL_VPN_ENABLED must be true or false"
[[ -z ${ANVIL_VPN_PRIVATE_KEY:-} ]] ||
    fail "ANVIL_VPN_PRIVATE_KEY must stay on the VPN gateway"
if [[ $ANVIL_VPN_ENABLED == true ]]; then
    [[ ${ANVIL_VPN_PEER_CONTROL:-} == external ]] ||
        fail "ANVIL_VPN_PEER_CONTROL must be external when VPN is enabled"
    [[ ${ANVIL_VPN_PUBLIC_ENDPOINT:-} == vpn.h7tex.com:443 ]] ||
        fail "ANVIL_VPN_PUBLIC_ENDPOINT must be vpn.h7tex.com:443 when VPN is enabled"
    [[ ${ANVIL_VPN_ALLOWED_IPS:-} == 10.60.0.0/15 ]] ||
        fail "ANVIL_VPN_ALLOWED_IPS must be 10.60.0.0/15 when VPN is enabled"
    [[ ${ANVIL_VPN_PUBLIC_KEY:-} =~ ^[A-Za-z0-9+/]{43}=$ ]] ||
        fail "ANVIL_VPN_PUBLIC_KEY must be a base64-encoded 32-byte WireGuard public key"
    vpn_public_key_size=$(printf '%s' "$ANVIL_VPN_PUBLIC_KEY" | base64 --decode 2>/dev/null | wc -c) ||
        fail "ANVIL_VPN_PUBLIC_KEY is not valid base64"
    [[ $vpn_public_key_size -eq 32 ]] ||
        fail "ANVIL_VPN_PUBLIC_KEY must decode to 32 bytes"
fi

declare -A seen_secrets=()
for name in ANVIL_DATABASE_PASSWORD ANVIL_JWT_SECRET ANVIL_SECRETS_ENCRYPTION_KEY ANVIL_GAME_INTERNAL_TOKEN; do
    value=${!name}
    [[ ${#value} -ge 32 && $value != replace-* ]] || fail "$name must be an independent secret of at least 32 characters"
    [[ -z ${seen_secrets[$value]:-} ]] || fail "$name must not reuse ${seen_secrets[$value]}"
    seen_secrets[$value]=$name
done

[[ $PUBLIC_API_URL == https://finals.h7tex.com ]] || fail "PUBLIC_API_URL must be https://finals.h7tex.com"
ip -o -4 address show | awk '{print $4}' | cut -d/ -f1 | grep -Fxq "$ANVIL_PRIVATE_ADDRESS" ||
    fail "ANVIL_PRIVATE_ADDRESS is not configured on this host"

[[ -r "$ANVIL_TLS_CERT_PATH" ]] || fail "TLS certificate is not readable"
[[ -r "$ANVIL_TLS_KEY_PATH" ]] || fail "TLS private key is not readable"
key_mode=$(stat -c '%a' "$ANVIL_TLS_KEY_PATH")
(( (8#$key_mode & 077) == 0 )) || fail "TLS private key must not be readable by group or other users"
openssl x509 -in "$ANVIL_TLS_CERT_PATH" -noout -checkend 604800 >/dev/null 2>&1 ||
    fail "TLS certificate is invalid or expires within seven days"
openssl x509 -in "$ANVIL_TLS_CERT_PATH" -noout -checkhost finals.h7tex.com >/dev/null 2>&1 ||
    fail "TLS certificate does not cover finals.h7tex.com"
cert_key=$(openssl x509 -in "$ANVIL_TLS_CERT_PATH" -pubkey -noout | openssl sha256) ||
    fail "TLS certificate public key could not be read"
private_key=$(openssl pkey -in "$ANVIL_TLS_KEY_PATH" -pubout 2>/dev/null | openssl sha256) ||
    fail "TLS private key is invalid or encrypted"
[[ $cert_key == "$private_key" ]] || fail "TLS certificate and private key do not match"

for path in "$ANVIL_DATABASE_HOST_PATH" "$ANVIL_STORAGE_HOST_PATH" "$ANVIL_BACKUP_DIR"; do
    [[ -d $path && -w $path ]] || fail "durable path is missing or not writable: $path"
done
if (( EUID == 0 )); then
    root_source=$(findmnt --noheadings --output SOURCE --target /)
    data_source=$(findmnt --noheadings --output SOURCE --target "$ANVIL_DATABASE_HOST_PATH")
    [[ $data_source != "$root_source" ]] || fail "database path is on the boot filesystem"
    for path in "$ANVIL_STORAGE_HOST_PATH" "$ANVIL_BACKUP_DIR"; do
        [[ $(findmnt --noheadings --output SOURCE --target "$path") == "$data_source" ]] ||
            fail "$path is not on the database block volume"
    done
    findmnt --noheadings --output OPTIONS --target "$ANVIL_DATABASE_HOST_PATH" | tr ',' '\n' | grep -Fxq rw ||
        fail "database block volume is not mounted read-write"
fi

docker info >/dev/null 2>&1 || fail "Docker daemon is not available"
docker compose up --help | grep -q -- '--wait' || fail "Docker Compose does not support up --wait"
for image in \
    "anvil-finals-api:$ANVIL_IMAGE_REVISION" \
    "anvil-finals-web:$ANVIL_IMAGE_REVISION"; do
    docker image inspect "$image" >/dev/null 2>&1 || fail "prebuilt image is missing: $image"
done
docker compose \
    --project-name anvil-finals \
    --project-directory "$script_dir" \
    --env-file "$env_file" \
    -f "$script_dir/compose.yaml" \
    config --quiet

echo "preflight: ok"
