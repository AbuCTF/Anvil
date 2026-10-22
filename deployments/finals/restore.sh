#!/usr/bin/env bash
set -euo pipefail
umask 077

script_dir=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
env_file=${ANVIL_ENV_FILE:-/etc/anvil/finals.env}
backup_set=${1:-}

fail() {
    echo "restore: $*" >&2
    exit 1
}

[[ ${ANVIL_RESTORE_CONFIRM:-} == empty-target ]] || fail "set ANVIL_RESTORE_CONFIRM=empty-target after verifying this is a new target"
[[ -n $backup_set && -d $backup_set ]] || fail "usage: restore.sh /path/to/anvil-TIMESTAMP"
[[ -f "$env_file" ]] || fail "environment file not found: $env_file"
set -a
# shellcheck disable=SC1090
source "$env_file"
set +a
: "${ANVIL_STORAGE_HOST_PATH:?ANVIL_STORAGE_HOST_PATH is required}"
[[ -d "$ANVIL_STORAGE_HOST_PATH" ]] || fail "storage directory is missing"
[[ -z $(find "$ANVIL_STORAGE_HOST_PATH" -mindepth 1 -print -quit) ]] || fail "storage target is not empty"

for file in database.dump storage.tar.gz SHA256SUMS; do
    [[ -f "$backup_set/$file" ]] || fail "backup is missing $file"
done
(
    cd -- "$backup_set"
    sha256sum --check --strict SHA256SUMS
) || fail "backup checksum verification failed"
if tar -tzf "$backup_set/storage.tar.gz" | grep -Eq '(^/|(^|/)\.\.(/|$))'; then
    fail "storage archive contains an unsafe path"
fi

compose=(docker compose --project-name anvil-finals --project-directory "$script_dir" --env-file "$env_file" -f "$script_dir/compose.yaml")
running=$("${compose[@]}" ps --services --status running)
for service in api web ingress; do
    ! grep -Fxq "$service" <<<"$running" || fail "$service must be stopped before restore"
done

"${compose[@]}" up -d --wait postgres
table_count=$("${compose[@]}" exec -T postgres psql --username anvil --dbname anvil --tuples-only --no-align \
    --command "SELECT count(*) FROM pg_tables WHERE schemaname = 'public';")
[[ $table_count == 0 ]] || fail "database target is not empty"

"${compose[@]}" exec -T postgres pg_restore \
    --username anvil --dbname anvil --exit-on-error --no-owner --no-privileges <"$backup_set/database.dump"
tar --extract --gzip --file "$backup_set/storage.tar.gz" --directory "$ANVIL_STORAGE_HOST_PATH" \
    --no-same-owner --no-same-permissions
"${compose[@]}" exec -T postgres psql --username anvil --dbname anvil --tuples-only --no-align \
    --command "SELECT count(*) FROM schema_migrations;" >/dev/null

echo "restore: complete; run preflight and start Anvil only after reviewing this target"
