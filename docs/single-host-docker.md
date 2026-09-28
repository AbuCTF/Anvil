# Single-host Docker deployment

This profile runs Anvil, PostgreSQL, and HTTP challenge instances on one Docker host. It is intended for demonstrations, small private events, development, and installations that do not need Kubernetes scheduling.

It does not expose the Docker API over TCP. The API talks to the local Unix socket, and an optional Traefik process discovers only containers that Anvil explicitly labels.

## Capabilities

- Platform API, web application, PostgreSQL, and persistent storage through Docker Compose
- Legacy single-container Docker challenges
- Per-user or per-team dynamic flags
- CPU, memory, reset, extension, and expiry controls
- Deterministic wildcard hostnames for HTTP challenges
- Automatic route recovery after the challenge router restarts

The current Docker provider does not yet implement Kubernetes-style multi-container challenge specifications or public raw-TCP routing. The admin UI should not advertise those capabilities for a Docker-only installation until provider capability discovery is implemented.

## Requirements

- A Linux host with Docker Engine and Docker Compose
- A DNS name for the platform
- A separate wildcard DNS namespace for HTTP challenge instances
- A TLS certificate covering both names
- A reverse proxy already listening on public ports 80 and 443, or permission to install one
- Images compatible with the host CPU architecture
- At least 4 vCPU, 8 GiB RAM, and enough disk for the selected challenge images

Example names:

```text
Platform:  demo.example.org
Instances: *.instances.demo.example.org
```

The wildcard certificate must explicitly cover the instance namespace. A certificate for `*.example.org` does not cover `*.instances.example.org`.

## 1. Create the configuration

```bash
cp .env.example .env
chmod 600 .env
```

Generate independent database, JWT, and instance-identity secrets. Do not copy production signing keys into a clone.

Set at least:

```dotenv
ANVIL_DATABASE_PASSWORD=<generated database password>
ANVIL_JWT_SECRET=<generated JWT signing secret>
ANVIL_INSTANCER_HMAC_SECRET=<generated instance identity secret>
PUBLIC_API_URL=https://demo.example.org

ANVIL_API_BIND=127.0.0.1:18080
ANVIL_WEB_BIND=127.0.0.1:13000
ANVIL_POSTGRES_BIND=127.0.0.1:15432

ANVIL_CONTAINER_NETWORK_NAME=anvil-demo-challenges
ANVIL_CONTAINER_NETWORK_SUBNET=172.29.0.0/24
ANVIL_CONTAINER_NETWORK_INTERNAL=true
ANVIL_CONTAINER_HTTP_ROUTING=true
ANVIL_CONTAINER_HTTP_BASE_DOMAIN=instances.demo.example.org
ANVIL_CONTAINER_HTTP_EXTERNAL_PORT=443
ANVIL_CONTAINER_HTTP_EXTERNAL_SCHEME=https
ANVIL_CONTAINER_ROUTER_LISTEN=127.0.0.1:18082

ANVIL_VPN_ENABLED=false
ANVIL_SSO_ENABLED=false
ANVIL_DISCORD_ENABLED=false
ANVIL_WEBVERSE_ENABLED=false
```

Use a subnet that does not overlap the host, VPN, Compose, or organization networks. `network_internal=true` is appropriate for challenges that do not need outbound access.

## 2. Start the private stack

Validate the rendered configuration before starting anything:

```bash
docker compose --profile http-routing config --quiet
```

Build and start the platform on loopback-only ports:

```bash
docker compose --profile http-routing up -d --build
docker compose ps
curl --fail http://127.0.0.1:18080/health
```

Do not add the public reverse-proxy or DNS configuration until the database has been initialized or restored and authentication has been checked.

## 3. Configure the public reverse proxy

The platform hostname sends `/api` to the API and everything else to the web application. The instance wildcard sends HTTP traffic to the local challenge router.

Example Nginx upstream behavior:

```nginx
server {
    listen 443 ssl;
    server_name demo.example.org;

    location /api/ {
        proxy_pass http://127.0.0.1:18080;
        proxy_set_header Host $host;
        proxy_set_header X-Forwarded-Proto https;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
    }

    location / {
        proxy_pass http://127.0.0.1:13000;
        proxy_set_header Host $host;
        proxy_set_header X-Forwarded-Proto https;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
    }
}

server {
    listen 443 ssl;
    server_name *.instances.demo.example.org;

    location / {
        proxy_pass http://127.0.0.1:18082;
        proxy_set_header Host $host;
        proxy_set_header X-Forwarded-Proto https;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
    }
}
```

Add matching DNS records only after `nginx -t` and local Host-header checks pass.

## 4. Challenge image requirements

An HTTP challenge must declare an exposed port with `service: http` or `protocol: http`. Anvil converts that application-level protocol to Docker TCP and emits a Traefik route for the deterministic instance hostname.

Every challenge must specify realistic CPU and memory limits. For example:

```yaml
cpu_limit: "0.5"
memory_limit: 256Mi
exposed_ports:
  - { port: 8080, protocol: http, service: http }
```

Before a demo or event, verify the image architecture:

```bash
docker buildx imagetools inspect --format '{{json .Image}}' registry.example.org/challenge:tag
```

Rebuild the selected image for the host architecture or publish a multi-platform manifest. Emulation is a fallback, not a capacity plan.

## 5. Restore rules

A database clone contains participant information and authentication material. Before public exposure:

1. Restore into an isolated database volume.
2. Delete sessions, refresh tokens, access-token revocations, SSO nonces, team tokens, and invite codes.
3. Clear copied password hashes and identity-provider bindings.
4. Remove or rotate KotH, graded, webhook, and reset secrets.
5. Stop copied runtime rows and clear infrastructure resource identifiers.
6. Use a new JWT secret and new integration credentials.
7. Create a dedicated demo administrator and demo participants.
8. Compare expected row counts and preserve a checksum manifest.

Keep the original export private and mode `0600`. Use a separately generated sanitized export for repeatable demonstrations.

## 6. Acceptance checks

```bash
docker compose ps
curl --fail http://127.0.0.1:18080/health
curl --fail -H 'Host: <instance-hostname>' http://127.0.0.1:18082/
docker ps --filter label=managed-by=anvil
docker inspect <challenge-container> --format '{{json .HostConfig.Resources}}'
```

Validate two separate teams, dynamic flag isolation, route cleanup after stop and expiry, router restart recovery, stack restart persistence, and the health of unrelated services on the host.

## 7. Stop and roll back

Stop the demo without deleting its database:

```bash
docker compose --profile http-routing down
```

Do not add `--volumes` unless the database has been backed up and deletion is explicitly intended.

Remove the new DNS records and Nginx server blocks to end public access. This deployment is independent of a Kubernetes deployment and does not require a production cutover.
