#!/usr/bin/env bash
set -euo pipefail
umask 077

script_dir=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
env_file=${ANVIL_ENV_FILE:-/etc/anvil/finals.env}

[[ -f "$env_file" ]] || { echo "backup: environment file not found: $env_file" >&2; exit 1; }
set -a
# shellcheck disable=SC1090
source "$env_file"
set +a
: "${ANVIL_BACKUP_DIR:?ANVIL_BACKUP_DIR is required}"
: "${ANVIL_STORAGE_HOST_PATH:?ANVIL_STORAGE_HOST_PATH is required}"
[[ -d "$ANVIL_BACKUP_DIR" && -w "$ANVIL_BACKUP_DIR" ]] || {
    echo "backup: backup directory is missing or not writable" >&2
    exit 1
}
[[ -d "$ANVIL_STORAGE_HOST_PATH" ]] || { echo "backup: storage directory is missing" >&2; exit 1; }

compose=(docker compose --project-name anvil-finals --project-directory "$script_dir" --env-file "$env_file" -f "$script_dir/compose.yaml")
[[ $("${compose[@]}" ps postgres --status running --quiet) ]] || {
    echo "backup: postgres is not running" >&2
    exit 1
}

timestamp=$(date -u +%Y%m%dT%H%M%SZ)
partial=$(mktemp -d "$ANVIL_BACKUP_DIR/.anvil-$timestamp.XXXXXX")
final="$ANVIL_BACKUP_DIR/anvil-$timestamp"
cleanup() { rm -rf -- "$partial"; }
trap cleanup EXIT

"${compose[@]}" exec -T postgres pg_dump \
    --username anvil --dbname anvil --format custom --no-owner --no-privileges >"$partial/database.dump"
"${compose[@]}" exec -T postgres pg_restore --list <"$partial/database.dump" >/dev/null
tar --create --gzip --file "$partial/storage.tar.gz" --directory "$ANVIL_STORAGE_HOST_PATH" .
(
    cd -- "$partial"
    sha256sum database.dump storage.tar.gz >SHA256SUMS
)

chmod -R go-rwx "$partial"
mv --no-target-directory -- "$partial" "$final"
trap - EXIT
echo "$final"
