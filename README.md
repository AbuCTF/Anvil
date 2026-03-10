Self-hosted B2R/AD-CTF platform. VMs, containers, WireGuard. One box.

`Anvil`

v0.1.0 [`forging`]
- Docker container challenges
- Full VM support (OVA/VMDK/QCOW2)
- WireGuard VPN integration
- Multi-flag challenges
- Dynamic scoring
- SvelteKit frontend


#### **Deploy**

Edit these:
- `.env` — secrets, endpoints, passwords
- `docker-compose.yml` — production targets, build args, port bindings
- `config/config.yaml` — VPN keys, platform tuning

Generate secrets:
```bash
openssl rand -base64 32                              # db password
openssl rand -hex 64                                 # jwt secret
wg genkey | tee /tmp/wg-priv | wg pubkey > /tmp/wg-pub  # vpn keys
```

Key changes in `docker-compose.yml`:
- web target: `development` → `production`
- `PUBLIC_API_URL`: `https://your-domain.com`
- bind postgres/redis/api/web to `127.0.0.1`
- pipe all env vars from `.env`

WireGuard (on host, not in Docker):
```bash
sudo apt install wireguard-tools
# write /etc/wireguard/wg0.conf with your private key
# PostUp: iptables FORWARD + MASQUERADE on your interface
sudo systemctl enable --now wg-quick@wg0
```

Nginx reverse proxy:
- `/api/` → `127.0.0.1:8080`
- `/` → `127.0.0.1:3000`
- `set_real_ip_from` all Cloudflare ranges + `real_ip_header CF-Connecting-IP`
- gzip on, static asset caching 1yr

Cloudflare:
- DNS: A record → server IP, **proxied** (orange cloud)
- SSL/TLS mode: **Flexible** (origin nginx listens on 80 only)
  - If you want Full/Strict: add a self-signed or origin cert + nginx 443 listener
  - 521 error = Cloudflare can't reach origin. Check: SSL mode, port 80/443 open, nginx running

OCI (Oracle Cloud):
- Security List: allow TCP 80, 443 + UDP 51820
- OS iptables: `iptables -I INPUT` for same ports (OCI Ubuntu drops by default)
- `sudo netfilter-persistent save`

Launch:
```bash
sudo mkdir -p /var/lib/anvil/images/uploads /var/lib/anvil/images/templates
docker compose up -d --build
```

Verify:
```bash
curl https://your-domain.com/api/health
```

First admin (inject directly into DB):
```bash
docker exec -it anvil-postgres psql -U anvil -d anvil -c \
  "INSERT INTO users (username, email, password_hash, role, status) \
   VALUES ('abu', 'admin@abu.rocks', crypt('', gen_salt('bf', 10)), 'admin', 'active');"
```

To promote an existing user instead:
```bash
docker exec -it anvil-postgres psql -U anvil -d anvil -c \
  "UPDATE users SET role = 'admin' WHERE username = 'youruser';"
```

#### **VM Support**

Upload OVA/VMDK/QCOW2 for challenges requiring:
- Kernel exploits (DirtyCOW, DirtyPipe)
- Systemd abuse
- Full network stack
- Active Directory labs

#### **Environment Variables**

| Variable | Description |
|----------|-------------|
| `ANVIL_DATABASE_PASSWORD` | PostgreSQL password |
| `ANVIL_JWT_SECRET` | JWT signing secret |
| `ANVIL_VPN_PRIVATE_KEY` | WireGuard server private key |
| `ANVIL_VPN_PUBLIC_KEY` | WireGuard server public key |
| `ANVIL_VPN_PUBLIC_ENDPOINT` | Server public IP for VPN clients |
| `PUBLIC_API_URL` | Frontend → API URL (build arg) |

full list in `.env.example`

#### **Roadmap**

- [x] Core platform (challenges, users, instances)
- [x] VPN connectivity (WireGuard)
- [x] VM support (OVA/VMDK/QCOW2)
- [x] Scoreboard + dynamic scoring
- [x] SvelteKit frontend
- [ ] Multi-cloud (AWS, Azure)
- [ ] Active Directory labs
- [ ] Attack-Defense mode
