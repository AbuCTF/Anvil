# Finals production deployment

This profile runs the finals control plane on the private Anvil host. OCI forwards
public TCP 443 at layer 4 to `10.60.200.10:443`; the bundled Nginx service
terminates TLS and routes `/api/` to Anvil and all other requests to the web app.
PostgreSQL has no host port. The raw flag-submission service is bound to
`10.60.200.10:4141` and must be reachable only through the approved VPN path.

This is independent of the Atom demo deployment. Do not reuse its database,
volumes, certificates, signing keys or environment file.

## Repository contract

| Path | Purpose |
|---|---|
| `deployments/finals/compose.yaml` | Postgres, one API/game controller, web and TLS ingress |
| `deployments/finals/game.yaml` | Versioned finals game settings |
| `deployments/finals/settings.sql` | Idempotent finals database settings |
| `deployments/finals/production.env.example` | Names of host-owned settings and secrets |
| `deployments/finals/preflight.sh` | Secret, certificate, address and Compose checks |
| `deployments/finals/backup.sh` | PostgreSQL dump plus storage archive |
| `deployments/finals/restore.sh` | Guarded restore into an empty replacement target |
| `deployments/finals/anvil-finals*.{service,timer}` | Boot and hourly-backup units |

The API container does not receive the Docker socket, host PID namespace, SSH
keys or cloud credentials. It receives only its database password, JWT signing
secret, encryption key and game internal token. TLS keys are mounted only into
Nginx. PostgreSQL and Nginx base images are pinned by manifest digest.

This revision deliberately keeps Anvil VPN enrollment disabled. Production
commissioning is blocked until the external peer-control release is on the base
branch and a versioned follow-up sets `vpn.enabled=true` and
`vpn.peer_control=external`. Enabling the current production-mode peer writer
would require the host PID namespace and is forbidden by this profile.

Validate a reviewed checkout before packaging it:

```bash
deployments/finals/test.sh
go test ./internal/config
```

The deployment test renders Compose, checks the privilege boundary, validates
the systemd units, starts the hardened Nginx image, and exercises backup and
empty-target restore with an isolated command fixture. It does not start a
production stack.

## Host preparation

The host needs Docker Engine, the Docker Compose plugin with `up --wait`,
OpenSSL, GNU tar, `findmnt`, `lsblk`, `blkid`, `mkfs.ext4` and systemd. Keep the
repository at an immutable reviewed commit under
`/opt/anvil/releases/<git-sha>` and point `/opt/anvil/current` at that release.
Do not edit the checkout on the server.

### Durable volume

The Terraform `database` block volume holds PostgreSQL, application storage and
local backup sets. The fixed mount point is `/srv/anvil`; the boot volume is not
an acceptable substitute.

On the new host, match the attached device to the `database` volume attachment
in OCI before running any filesystem command:

```bash
lsblk --output NAME,PATH,SIZE,FSTYPE,UUID,MOUNTPOINTS,SERIAL
sudo blkid
```

Stop if the selected device has an unexpected filesystem, UUID or data. Only
for a verified new and blank `database` volume, create the filesystem once:

```bash
sudo mkfs.ext4 -L anvil-data /dev/<verified-database-device>
```

Use `blkid` to put that exact UUID in `/etc/fstab`; never use a changing `/dev`
name there:

```fstab
UUID=<database-volume-filesystem-uuid> /srv/anvil ext4 defaults,nofail 0 2
```

Mount and verify that the source differs from the root filesystem before
creating state directories:

```bash
sudo install -d -o root -g root -m 0750 /srv/anvil
sudo mount /srv/anvil
findmnt --target /srv/anvil
test "$(findmnt -n -o SOURCE -T /srv/anvil)" != "$(findmnt -n -o SOURCE -T /)"
sudo install -d -o 70 -g 70 -m 0700 /srv/anvil/postgres
sudo install -d -o root -g root -m 0700 /srv/anvil/storage /srv/anvil/backups
```

UID/GID 70 is the `postgres` account in the pinned Alpine image. Recheck it when
updating that image digest. Preflight refuses a root-owned production start when
any durable path resolves to the boot filesystem or a different mount. The
systemd units also refuse to start unless `/srv/anvil` is a mount point.

Create the remaining host-owned paths:

```bash
sudo install -d -o root -g root -m 0700 /etc/anvil /etc/anvil/tls
sudo install -o root -g root -m 0600 \
  deployments/finals/production.env.example /etc/anvil/finals.env
```

Set `ANVIL_DATABASE_HOST_PATH=/srv/anvil/postgres`,
`ANVIL_STORAGE_HOST_PATH=/srv/anvil/storage` and
`ANVIL_BACKUP_DIR=/srv/anvil/backups`. The three values are required and are
bind-mounted directly; Docker named volumes are not used for finals state.

### TLS and commissioning egress

Issue the `finals.h7tex.com` certificate on a controlled operator workstation,
not on the private Anvil host. With Certbot installed, run:

```bash
install -d -m 0700 ./finals-certbot/{config,work,logs}
certbot certonly --manual --preferred-challenges dns --key-type ecdsa \
  --config-dir "$PWD/finals-certbot/config" \
  --work-dir "$PWD/finals-certbot/work" --logs-dir "$PWD/finals-certbot/logs" \
  --agree-tos --no-eff-email -m <organizer-address> -d finals.h7tex.com
```

Add the prompted `_acme-challenge.finals.h7tex.com` TXT value in Cloudflare,
wait for authoritative DNS to return it, and continue. The existing A record
stays DNS-only. The Cloudflare token remains on the operator side. Copy the
result through the approved SSH/bastion path, then install it on the host:

```bash
scp -o ProxyJump=<bastion> \
  "$PWD/finals-certbot/config/live/finals.h7tex.com/fullchain.pem" \
  "$PWD/finals-certbot/config/live/finals.h7tex.com/privkey.pem" \
  <admin>@10.60.200.10:/home/<admin>/
ssh -J <bastion> <admin>@10.60.200.10
sudo install -o root -g root -m 0644 /home/<admin>/fullchain.pem /etc/anvil/tls/fullchain.pem
sudo install -o root -g root -m 0600 /home/<admin>/privkey.pem /etc/anvil/tls/privkey.pem
openssl x509 -in /etc/anvil/tls/fullchain.pem -noout -subject -issuer -dates \
  -checkhost finals.h7tex.com
shred -u /home/<admin>/privkey.pem
rm /home/<admin>/fullchain.pem
```

Delete the staged copies and the temporary DNS challenge value. The certificate
must remain valid for at least seven days when preflight runs. Renewals replace
both files atomically, run preflight, and reload `anvil-finals.service`.

The service subnet normally has no egress. During commissioning only, enable the
reviewed temporary NAT path, record its start time and operator, pull the pinned
PostgreSQL/Nginx manifests, and build the exact reviewed Anvil release. Disable
the NAT path afterward and prove a preflight, service restart and health check
with egress blocked. Every later release must either use the offline mirror or
repeat that controlled window; never leave general egress enabled for the event.

Generate four independent values; do not derive one from another:

```bash
openssl rand -hex 32 # database password
openssl rand -hex 32 # JWT signing secret
openssl rand -hex 32 # stored-secret encryption key
openssl rand -hex 32 # internal game API token
```

Put the values only in `/etc/anvil/finals.env` and keep the file and TLS key mode
`0600`, owned by root. The file uses simple `NAME=value` shell syntax; generated
hex values avoid quoting ambiguity. Never commit, print or copy it into Terraform,
cloud-init, a container image or a backup manifest.

The infrastructure and host firewalls must allow only the reviewed gateway path
to TCP 443, the VPN path to TCP 4141, and the management path to SSH. Do not open
5432, 8080 or 3000. The host has no public address.

## Install and start

Install the units from the reviewed release:

```bash
sudo install -o root -g root -m 0644 \
  deployments/finals/anvil-finals.service /etc/systemd/system/
sudo install -o root -g root -m 0644 \
  deployments/finals/anvil-finals-backup.service \
  deployments/finals/anvil-finals-backup.timer /etc/systemd/system/
sudo systemctl daemon-reload
```

Run the preflight before the first start and after every configuration or
certificate change:

```bash
sudo /opt/anvil/current/deployments/finals/preflight.sh
sudo systemctl enable --now anvil-finals.service
sudo systemctl enable --now anvil-finals-backup.timer
```

Anvil applies embedded database migrations during API startup. Migrations are
serialized with a PostgreSQL advisory lock and their names and checksums are
validated. A migration failure keeps the API unhealthy and prevents Nginx from
becoming ready. Never bypass that failure or edit `schema_migrations`; retain the
old release, logs and pre-upgrade backup for diagnosis.

After the API is healthy, apply the idempotent participant-facing finals settings:

```bash
sudo docker compose --project-name anvil-finals \
  --project-directory /opt/anvil/current/deployments/finals \
  --env-file /etc/anvil/finals.env \
  -f /opt/anvil/current/deployments/finals/compose.yaml \
  exec -T postgres psql -v ON_ERROR_STOP=1 -U anvil -d anvil \
  < /opt/anvil/current/deployments/finals/settings.sql
```

Exactly one API process in this profile owns game ticks and TCP submission. Do
not scale `api` without first moving the controller role to one explicit process.
Starting the stack does not start the event: staff must finish the admin
readiness review before changing `platform_settings.game.state` to `running`.

## Health and readiness

Check the stack locally before enabling or changing DNS:

```bash
sudo docker compose --project-name anvil-finals \
  --project-directory /opt/anvil/current/deployments/finals \
  --env-file /etc/anvil/finals.env \
  -f /opt/anvil/current/deployments/finals/compose.yaml ps
curl --fail --resolve finals.h7tex.com:443:10.60.200.10 \
  https://finals.h7tex.com/api/health
```

The public health route gates on PostgreSQL. Docker separately checks Postgres,
the API, web and ingress. Health does not replace readiness: an administrator
must also review `/api/v1/admin/readiness`, authenticate through the UI, exercise
one test team, submit through both HTTPS and TCP, and verify a full tick without
making the game live.

## Backups

The timer runs hourly with up to five minutes of jitter. It writes a
custom-format PostgreSQL dump, a gzip archive of application storage, and a
SHA-256 manifest under `/srv/anvil/backups/anvil-<UTC timestamp>`. The local
operational RPO is therefore at most 65 minutes. It never deletes an older set;
retain every rehearsal and event set through at least seven days after the
event and verify capacity for that retention before enabling the timer.

Run and inspect one backup before every release that may migrate the database:

```bash
sudo systemctl start anvil-finals-backup.service
sudo systemctl status anvil-finals-backup.service
sudo find /srv/anvil/backups -maxdepth 2 -type f -printf '%M %u:%g %p\n'
```

The local sets contain participant records, password hashes, tokens and event
state. They remain root-only. Encrypt them before off-host transfer; the
encryption key must remain outside the archive location and Terraform state.
Application storage is archived live, so avoid administrative uploads during a
manual pre-release backup.

Local sets share the data volume and do not cover loss of that volume. Before
production, the Terraform storage stack must attach a custom hourly OCI block
volume backup policy to the `database` volume with at least seven-day retention.
Record one successful scheduled backup and one restore to a replacement volume.
This is a hard commissioning gate. GCP archive copies are an additional
encrypted off-site layer, not a live-platform dependency.

## Restore rehearsal and recovery

Never restore over the only production copy. Provision a replacement host or
new data volume, install the same release and secrets, create empty storage and
backup directories, and copy one verified backup set there. Do not start the
platform service.

On the empty target only:

```bash
sudo env ANVIL_RESTORE_CONFIRM=empty-target \
  /opt/anvil/current/deployments/finals/restore.sh \
  /srv/anvil/backups/anvil-<UTC timestamp>
sudo /opt/anvil/current/deployments/finals/preflight.sh
sudo systemctl start anvil-finals.service
```

The restore command verifies the manifest, refuses a running API/web/ingress,
refuses a non-empty database or storage directory, restores both state sets and
leaves commissioning to the operator. After startup, Anvil validates migration
history and applies only newer migrations. Validate login, users, teams, tick
history, scores, release candidates and stored files before switching the
private ingress to the replacement host.

Perform this rehearsal before the event and record the backup timestamp,
release SHA, duration and validation result outside the repository.

## Upgrade and rollback

For an upgrade:

1. Fetch and verify the reviewed commit on an operator machine.
2. Place that exact commit in a new `/opt/anvil/releases/<git-sha>` directory.
3. Run a backup and verify its manifest.
4. Point `/opt/anvil/current` at the new release.
5. Run preflight, then `sudo systemctl reload anvil-finals.service`.
6. Run health, admin readiness and the test-team flow.

If application checks fail, point `current` back to the prior release and reload.
Do not roll the database backward in place. Repository migrations are additive;
if a data recovery is genuinely required, restore the saved set to a new target,
validate it, and switch ingress only after approval.

Stopping the service preserves the database volume and host storage:

```bash
sudo systemctl stop anvil-finals.service
```

Never run `docker compose down --volumes` on a finals host.
