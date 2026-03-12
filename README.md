Self-hosted B2R/AD-CTF platform with VM & container support.

`Latest Release`
- v0.1.0 [`in dev`]
  - Docker container challenges
  - Full VM support (OVA/VMDK/QCOW2)
  - WireGuard VPN integration
  - Multi-flag challenges
  - Dynamic scoring

#### **Configure**

| File | What |
|------|------|
| `.env` | Secrets, endpoints, passwords |
| `docker-compose.yml` | Build targets, port bindings, env passthrough |
| `config/config.yaml` | Platform tuning, rate limits, scoring mode |

In `docker-compose.yml`:
- Web build target: `production`
- `PUBLIC_API_URL`: `https://your-domain.com`
- Bind postgres/redis/api/web to `127.0.0.1`
- API container: `privileged: true`, `pid: "host"` (nsenter for VPN peer management)

#### **WireGuard**

On the host, not in Docker.

```bash
sudo apt install wireguard-tools
HOST_INTERFACE=$(ip route show default | awk '/default/ {print $5; exit}')
```

`/etc/wireguard/wg0.conf`:
```ini
[Interface]
PrivateKey = <server private key>
Address = 10.10.0.1/16
ListenPort = 51820
PostUp = iptables -t raw -I PREROUTING 1 -i %i -d 172.20.0.0/16 -j ACCEPT
PostUp = iptables -I FORWARD 1 -i %i -d 172.20.0.0/16 -j ACCEPT
PostUp = iptables -I FORWARD 1 -s 172.20.0.0/16 -o %i -j ACCEPT
PostUp = iptables -A FORWARD -i %i -j ACCEPT
PostUp = iptables -A FORWARD -o %i -j ACCEPT
PostUp = iptables -t nat -I POSTROUTING 1 -s 172.20.0.0/16 -d 10.10.0.0/16 -j RETURN
PostUp = iptables -t nat -A POSTROUTING -o <uplink interface> -j MASQUERADE
PostDown = iptables -t raw -D PREROUTING -i %i -d 172.20.0.0/16 -j ACCEPT
PostDown = iptables -D FORWARD -i %i -d 172.20.0.0/16 -j ACCEPT
PostDown = iptables -D FORWARD -s 172.20.0.0/16 -o %i -j ACCEPT
PostDown = iptables -D FORWARD -i %i -j ACCEPT
PostDown = iptables -D FORWARD -o %i -j ACCEPT
PostDown = iptables -t nat -D POSTROUTING -s 172.20.0.0/16 -d 10.10.0.0/16 -j RETURN
PostDown = iptables -t nat -D POSTROUTING -o <uplink interface> -j MASQUERADE
```

The `raw` table rule is required for Docker bridge networks on `iptables-nft` hosts. Docker installs a direct-access drop in `PREROUTING` for bridge IPs, so VPN traffic for `anvil-challenges` must be accepted before that rule. The `POSTROUTING ... RETURN` rule prevents Docker from masquerading challenge replies back to VPN clients.

```bash
echo 'net.ipv4.ip_forward = 1' | sudo tee -a /etc/sysctl.conf && sudo sysctl -p
sudo systemctl enable --now wg-quick@wg0
```

#### **VPN Sync**

```bash
chmod +x scripts/wg-status-sync.sh
sed -i 's/\r$//' scripts/wg-status-sync.sh
sudo cp scripts/wg-status-sync.service /etc/systemd/system/
sudo cp scripts/wg-status-sync.timer /etc/systemd/system/
sudo systemctl daemon-reload
sudo systemctl enable --now wg-status-sync.timer
```

#### **Nginx**

- `/api/` → `127.0.0.1:8080`
- `/` → `127.0.0.1:3000`
- `set_real_ip_from` Cloudflare ranges + `real_ip_header CF-Connecting-IP`

For large uploads (VM images, bypasses Cloudflare 100MB limit):
- `upload.your-domain.com` A record → server IP, **DNS only**
- Separate nginx block: `client_max_body_size 0`, `proxy_request_buffering off`
- `sudo certbot --nginx -d upload.your-domain.com`

#### **Launch**

```bash
sudo mkdir -p /var/lib/anvil/images/uploads /var/lib/anvil/images/templates
docker compose up -d --build
curl https://your-domain.com/api/health
```

```bash
docker exec -it anvil-postgres psql -U anvil -d anvil -c \
  "INSERT INTO users (username, email, password_hash, role, status) \
   VALUES ('admin', 'admin@example.com', crypt('password', gen_salt('bf', 10)), 'admin', 'active');"
```

#### **OCI notes**

```bash
sudo iptables -I INPUT -p tcp --dport 80 -j ACCEPT
sudo iptables -I INPUT -p tcp --dport 443 -j ACCEPT
sudo iptables -I INPUT -p udp --dport 51820 -j ACCEPT
sudo netfilter-persistent save
```

#### **Architecture**

```
User → WireGuard VPN (10.10.x.x) → Host
                                      ├── Nginx → API (8080) + Web (3000)
                                      ├── PostgreSQL + Redis
                                      └── anvil-challenges (172.20.0.0/16)
                                           ├── container-1 (172.20.x.x)
                                           ├── container-2 (172.20.x.x)
                                           └── ...
```

No host port bindings or NAT. VPN routes both `172.20.0.0/16` (containers) and `10.100.0.0/16` (VMs) through the tunnel. Players hit resource IPs and native ports directly with per-user access control.

If you change `container.network_subnet` in `config/config.yaml`, update both the WireGuard firewall rules above and the routed client `AllowedIPs` to match.

#### **Stack**

| Component | Tech |
|-----------|------|
| API | Go, Gin, PGX |
| Frontend | SvelteKit, Tailwind |
| Database | PostgreSQL 16 |
| Cache | Redis 7 |
| VPN | WireGuard |
| Containers | Docker |
| VMs | libvirt/QEMU |
