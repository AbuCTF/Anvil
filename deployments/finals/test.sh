#!/usr/bin/env bash
set -euo pipefail

script_dir=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
repo_root=$(cd -- "$script_dir/../.." && pwd)
test_dir=$(mktemp -d)
nginx_container=
cleanup() {
    if [[ -n $nginx_container ]]; then
        docker container rm --force "$nginx_container" >/dev/null 2>&1 || true
    fi
    [[ -n ${test_dir:-} && -d $test_dir ]] && rm -rf -- "$test_dir"
}
trap cleanup EXIT

for script in preflight.sh backup.sh restore.sh test.sh; do
    bash -n "$script_dir/$script"
done

for dockerfile in \
    "$repo_root/deployments/docker/Dockerfile.api" \
    "$repo_root/deployments/docker/Dockerfile.web"; do
    while read -r image; do
        if [[ ! $image =~ @sha256:[[:xdigit:]]{64}$ ]]; then
            echo "test: unpinned deployable base image in $dockerfile: $image" >&2
            exit 1
        fi
    done < <(awk 'toupper($1) == "FROM" { print $2 }' "$dockerfile")
done

if [[ $(awk 'toupper($1) == "FROM" && $2 ~ /^node:/ { print $2 }' \
    "$repo_root/deployments/docker/Dockerfile.web" | sort -u | wc -l) != 1 ]]; then
    echo "test: web stages do not share one pinned Node base image" >&2
    exit 1
fi

while read -r image; do
    if [[ ! $image =~ @sha256:[[:xdigit:]]{64}$ ]]; then
        echo "test: unpinned finals runtime image: $image" >&2
        exit 1
    fi
done < <(awk '$1 == "image:" { print $2 }' "$script_dir/compose.yaml")

for unit in anvil-finals.service anvil-finals-backup.service anvil-finals-backup.timer; do
    sed "s#/opt/anvil/current#$repo_root#g" "$script_dir/$unit" >"$test_dir/$unit"
done
systemd-analyze verify \
    "$test_dir/anvil-finals.service" \
    "$test_dir/anvil-finals-backup.service" \
    "$test_dir/anvil-finals-backup.timer"

mkdir -p "$test_dir/database" "$test_dir/storage" "$test_dir/backups" "$test_dir/tls"
openssl req -x509 -newkey rsa:2048 -nodes -days 8 \
    -keyout "$test_dir/tls/privkey.pem" \
    -out "$test_dir/tls/fullchain.pem" \
    -subj '/CN=finals.h7tex.com' \
    -addext 'subjectAltName=DNS:finals.h7tex.com' >/dev/null 2>&1
chmod 600 "$test_dir/tls/privkey.pem"

env_file="$test_dir/finals.env"
cat >"$env_file" <<EOF
ANVIL_PRIVATE_ADDRESS=127.0.0.1
PUBLIC_API_URL=https://finals.h7tex.com
ANVIL_DATABASE_PASSWORD=11111111111111111111111111111111
ANVIL_JWT_SECRET=22222222222222222222222222222222
ANVIL_SECRETS_ENCRYPTION_KEY=33333333333333333333333333333333
ANVIL_GAME_INTERNAL_TOKEN=44444444444444444444444444444444
ANVIL_TLS_CERT_PATH=$test_dir/tls/fullchain.pem
ANVIL_TLS_KEY_PATH=$test_dir/tls/privkey.pem
ANVIL_DATABASE_HOST_PATH=$test_dir/database
ANVIL_STORAGE_HOST_PATH=$test_dir/storage
ANVIL_BACKUP_DIR=$test_dir/backups
EOF
chmod 600 "$env_file"

"$script_dir/preflight.sh" "$env_file"

duplicate_env="$test_dir/duplicate.env"
sed 's/^ANVIL_JWT_SECRET=.*/ANVIL_JWT_SECRET=11111111111111111111111111111111/' "$env_file" >"$duplicate_env"
chmod 600 "$duplicate_env"
if "$script_dir/preflight.sh" "$duplicate_env" >/dev/null 2>&1; then
    echo "test: preflight accepted a reused secret" >&2
    exit 1
fi

rendered="$test_dir/compose.yaml"
docker compose \
    --project-name anvil-finals-test \
    --project-directory "$script_dir" \
    --env-file "$env_file" \
    -f "$script_dir/compose.yaml" config >"$rendered"
! grep -Fq '/var/run/docker.sock' "$rendered"
! grep -Eq '^[[:space:]]+privileged:[[:space:]]+true$' "$rendered"
! grep -Eq '^[[:space:]]+pid:[[:space:]]+host$' "$rendered"
! grep -Eq 'published:[[:space:]]+"?5432"?' "$rendered"
[[ $(grep -c 'published:' "$rendered") == 2 ]]
grep -Eq 'published:[[:space:]]+"?443"?' "$rendered"
grep -Eq 'published:[[:space:]]+"?4141"?' "$rendered"
grep -Fq "context: $repo_root" "$rendered"
grep -Fq "source: $test_dir/database" "$rendered"
grep -Fq 'postgres:16-alpine@sha256:cf78e76683b9ca8c5733cbbdce6c9262b45b6767934dd0a95e671f9a0fc20685' "$rendered"
grep -Fq 'nginx:1.28-alpine@sha256:a8b39bd9cf0f83869a2162827a0caf6137ddf759d50a171451b335cecc87d236' "$rendered"

nginx_options=(
    --read-only
    --security-opt no-new-privileges:true
    --tmpfs /var/cache/nginx
    --tmpfs /var/run
    --tmpfs /tmp
    --add-host api:127.0.0.1
    --add-host web:127.0.0.1
    --volume "$script_dir/nginx.conf:/etc/nginx/nginx.conf:ro"
    --volume "$test_dir/tls/fullchain.pem:/run/anvil/tls/fullchain.pem:ro"
    --volume "$test_dir/tls/privkey.pem:/run/anvil/tls/privkey.pem:ro"
)
nginx_image=nginx:1.28-alpine@sha256:a8b39bd9cf0f83869a2162827a0caf6137ddf759d50a171451b335cecc87d236
docker run --rm "${nginx_options[@]}" --entrypoint nginx "$nginx_image" -t

nginx_container="anvil-finals-nginx-test-$BASHPID"
docker create --name "$nginx_container" "${nginx_options[@]}" "$nginx_image" >/dev/null
docker start "$nginx_container" >/dev/null
[[ $(docker inspect --format '{{.State.Running}}' "$nginx_container") == true ]] || {
    echo "test: hardened Nginx container did not remain running" >&2
    exit 1
}
docker container rm --force "$nginx_container" >/dev/null
nginx_container=

if ANVIL_ENV_FILE="$env_file" "$script_dir/restore.sh" "$test_dir/missing" >/dev/null 2>&1; then
    echo "test: restore accepted a request without confirmation" >&2
    exit 1
fi

mkdir -p "$test_dir/bin"
cat >"$test_dir/bin/docker" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
args=" $* "
case "$args" in
    *" ps postgres --status running --quiet "*) echo postgres-running ;;
    *" ps --services --status running "*) ;;
    *" exec -T postgres pg_dump "*) printf 'test database dump\n' ;;
    *" exec -T postgres pg_restore --list "*) cat >/dev/null ;;
    *" exec -T postgres pg_restore "*) cat >/dev/null ;;
    *" up -d --wait postgres "*) ;;
    *"SELECT count(*) FROM pg_tables"*) echo 0 ;;
    *"SELECT count(*) FROM schema_migrations"*) echo 1 ;;
    *) echo "unexpected docker invocation: $*" >&2; exit 1 ;;
esac
EOF
chmod 755 "$test_dir/bin/docker"
printf 'stored artifact\n' >"$test_dir/storage/artifact.txt"
backup_set=$(ANVIL_ENV_FILE="$env_file" PATH="$test_dir/bin:$PATH" "$script_dir/backup.sh")
(
    cd -- "$backup_set"
    sha256sum --check --strict SHA256SUMS >/dev/null
)
mv -- "$test_dir/storage" "$test_dir/storage-source"
mkdir "$test_dir/storage"
ANVIL_RESTORE_CONFIRM=empty-target ANVIL_ENV_FILE="$env_file" PATH="$test_dir/bin:$PATH" \
    "$script_dir/restore.sh" "$backup_set" >/dev/null
cmp "$test_dir/storage-source/artifact.txt" "$test_dir/storage/artifact.txt"

echo "finals deployment tests: ok"
