#!/usr/bin/env bash
set -euo pipefail

script_dir=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
repo_root=$(cd -- "$script_dir/../.." && pwd)
test_dir=$(mktemp -d)
nginx_container=
test_images=()
cleanup() {
    if [[ -n $nginx_container ]]; then
        docker container rm --force "$nginx_container" >/dev/null 2>&1 || true
    fi
    for image in "${test_images[@]}"; do
        docker image rm "$image" >/dev/null 2>&1 || true
    done
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
    if [[ $image == 'anvil-finals-api:${ANVIL_IMAGE_REVISION:?ANVIL_IMAGE_REVISION_required}' ||
        $image == 'anvil-finals-web:${ANVIL_IMAGE_REVISION:?ANVIL_IMAGE_REVISION_required}' ]]; then
        continue
    fi
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
[[ $(grep -Ec '^Exec(Start|Reload)=/opt/anvil/current/deployments/finals/preflight\.sh$' \
    "$script_dir/anvil-finals.service") == 2 ]]
[[ $(grep -Ec '^Exec(Start|Reload)=.* up .* --no-build .* --wait$' \
    "$script_dir/anvil-finals.service") == 2 ]]
! grep -Eq '^Exec(Start|Reload)=.* --build([[:space:]]|$)' "$script_dir/anvil-finals.service"

mkdir -p "$test_dir/database" "$test_dir/storage" "$test_dir/backups" "$test_dir/tls"
openssl req -x509 -newkey rsa:2048 -nodes -days 8 \
    -keyout "$test_dir/tls/privkey.pem" \
    -out "$test_dir/tls/fullchain.pem" \
    -subj '/CN=finals.h7tex.com' \
    -addext 'subjectAltName=DNS:finals.h7tex.com' >/dev/null 2>&1
chmod 600 "$test_dir/tls/privkey.pem"

image_revision=$(openssl rand -hex 20)
api_image="anvil-finals-api:$image_revision"
web_image="anvil-finals-web:$image_revision"
test_images=("$api_image" "$web_image")
nginx_image=nginx:1.28-alpine@sha256:a8b39bd9cf0f83869a2162827a0caf6137ddf759d50a171451b335cecc87d236
docker pull "$nginx_image" >/dev/null
docker image tag "$nginx_image" "$api_image"
docker image tag "$nginx_image" "$web_image"

env_file="$test_dir/finals.env"
cat >"$env_file" <<EOF
ANVIL_PRIVATE_ADDRESS=127.0.0.1
PUBLIC_API_URL=https://finals.h7tex.com
ANVIL_IMAGE_REVISION=$image_revision
ANVIL_DATABASE_PASSWORD=11111111111111111111111111111111
ANVIL_JWT_SECRET=22222222222222222222222222222222
ANVIL_SECRETS_ENCRYPTION_KEY=33333333333333333333333333333333
ANVIL_GAME_INTERNAL_TOKEN=44444444444444444444444444444444
ANVIL_VPN_ENABLED=false
ANVIL_VPN_PEER_CONTROL=external
ANVIL_VPN_PUBLIC_ENDPOINT=vpn.h7tex.com:443
ANVIL_VPN_PUBLIC_KEY=
ANVIL_VPN_ALLOWED_IPS=10.60.0.0/15
ANVIL_TLS_CERT_PATH=$test_dir/tls/fullchain.pem
ANVIL_TLS_KEY_PATH=$test_dir/tls/privkey.pem
ANVIL_DATABASE_HOST_PATH=$test_dir/database
ANVIL_STORAGE_HOST_PATH=$test_dir/storage
ANVIL_BACKUP_DIR=$test_dir/backups
EOF
chmod 600 "$env_file"

"$script_dir/preflight.sh" "$env_file"

expect_preflight_failure() {
    local candidate=$1
    local reason=$2
    chmod 600 "$candidate"
    if "$script_dir/preflight.sh" "$candidate" >/dev/null 2>&1; then
        echo "test: preflight accepted $reason" >&2
        exit 1
    fi
}

missing_revision_env="$test_dir/missing-revision.env"
grep -v '^ANVIL_IMAGE_REVISION=' "$env_file" >"$missing_revision_env"
expect_preflight_failure "$missing_revision_env" "a missing image revision"

for invalid_revision in abcdef ABCDEF0123456789ABCDEF0123456789ABCDEF01; do
    invalid_revision_env="$test_dir/invalid-revision-$invalid_revision.env"
    sed "s/^ANVIL_IMAGE_REVISION=.*/ANVIL_IMAGE_REVISION=$invalid_revision/" \
        "$env_file" >"$invalid_revision_env"
    expect_preflight_failure "$invalid_revision_env" "an invalid image revision"
done

missing_image_revision=$(openssl rand -hex 20)
while docker image inspect "anvil-finals-api:$missing_image_revision" >/dev/null 2>&1 ||
    docker image inspect "anvil-finals-web:$missing_image_revision" >/dev/null 2>&1; do
    missing_image_revision=$(openssl rand -hex 20)
done
missing_image_env="$test_dir/missing-image.env"
sed "s/^ANVIL_IMAGE_REVISION=.*/ANVIL_IMAGE_REVISION=$missing_image_revision/" \
    "$env_file" >"$missing_image_env"
expect_preflight_failure "$missing_image_env" "a revision without both prebuilt images"

missing_gate_env="$test_dir/missing-gate.env"
grep -v '^ANVIL_VPN_ENABLED=' "$env_file" >"$missing_gate_env"
expect_preflight_failure "$missing_gate_env" "a missing VPN activation gate"

invalid_gate_env="$test_dir/invalid-gate.env"
sed 's/^ANVIL_VPN_ENABLED=.*/ANVIL_VPN_ENABLED=yes/' "$env_file" >"$invalid_gate_env"
expect_preflight_failure "$invalid_gate_env" "a non-boolean VPN activation gate"

enabled_without_key_env="$test_dir/enabled-without-key.env"
sed 's/^ANVIL_VPN_ENABLED=.*/ANVIL_VPN_ENABLED=true/' "$env_file" >"$enabled_without_key_env"
expect_preflight_failure "$enabled_without_key_env" "enabled VPN without a server public key"

wireguard_public_key=$(openssl rand -base64 32)
enabled_env="$test_dir/enabled.env"
sed \
    -e 's/^ANVIL_VPN_ENABLED=.*/ANVIL_VPN_ENABLED=true/' \
    -e "s|^ANVIL_VPN_PUBLIC_KEY=.*|ANVIL_VPN_PUBLIC_KEY=$wireguard_public_key|" \
    "$env_file" >"$enabled_env"
chmod 600 "$enabled_env"
"$script_dir/preflight.sh" "$enabled_env"

while IFS='|' read -r variable value reason; do
    invalid_vpn_env="$test_dir/invalid-$variable.env"
    sed "s|^$variable=.*|$variable=$value|" "$enabled_env" >"$invalid_vpn_env"
    expect_preflight_failure "$invalid_vpn_env" "$reason"
done <<'EOF'
ANVIL_VPN_PEER_CONTROL|local|local VPN peer control
ANVIL_VPN_PUBLIC_ENDPOINT|vpn.h7tex.com:51820|an unexpected VPN endpoint
ANVIL_VPN_ALLOWED_IPS|10.80.0.0/16|an unexpected VPN allowed route
ANVIL_VPN_PUBLIC_KEY|not-a-wireguard-key|an invalid VPN server public key
EOF

for key_size in 31 33; do
    wrong_size_key=$(openssl rand "$key_size" | base64 | tr -d '\n')
    wrong_size_env="$test_dir/public-key-$key_size.env"
    sed "s|^ANVIL_VPN_PUBLIC_KEY=.*|ANVIL_VPN_PUBLIC_KEY=$wrong_size_key|" \
        "$enabled_env" >"$wrong_size_env"
    expect_preflight_failure "$wrong_size_env" "a base64 VPN public key that decodes to $key_size bytes"
done

private_key_env="$test_dir/private-key.env"
cp "$env_file" "$private_key_env"
printf '\nANVIL_VPN_PRIVATE_KEY=must-not-be-here\n' >>"$private_key_env"
expect_preflight_failure "$private_key_env" "a gateway private key"

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
grep -Fq "image: anvil-finals-api:$image_revision" "$rendered"
grep -Fq "image: anvil-finals-web:$image_revision" "$rendered"
[[ $(grep -c 'pull_policy: never' "$rendered") == 2 ]]
grep -Fq 'ANVIL_VPN_ENABLED: "false"' "$rendered"
grep -Fq 'ANVIL_VPN_PEER_CONTROL: external' "$rendered"
grep -Fq 'ANVIL_VPN_PUBLIC_ENDPOINT: vpn.h7tex.com:443' "$rendered"
grep -Fq 'ANVIL_VPN_ALLOWED_IPS: 10.60.0.0/15' "$rendered"
! grep -Fq 'ANVIL_VPN_PRIVATE_KEY' "$rendered"

default_env="$test_dir/default.env"
grep -v '^ANVIL_VPN_ENABLED=' "$env_file" >"$default_env"
default_rendered="$test_dir/default-compose.yaml"
env -u ANVIL_VPN_ENABLED docker compose \
    --project-name anvil-finals-test \
    --project-directory "$script_dir" \
    --env-file "$default_env" \
    -f "$script_dir/compose.yaml" config >"$default_rendered"
grep -Fq 'ANVIL_VPN_ENABLED: "false"' "$default_rendered"

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
