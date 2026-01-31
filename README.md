Self-hosted B2R/AD-CTF platform with VM & container support.

`Latest Release`
- v0.1.0 [`in development`]
  - Docker container challenges
  - Full VM support (OVA/VMDK/QCOW2)
  - WireGuard VPN integration
  - Multi-flag challenges
  - Dynamic scoring

---

#### **Quick Start**

```bash
git clone https://github.com/AbuCTF/anvil.git
cd anvil
docker-compose up --build
```

Access:
- **Frontend**: http://localhost:3000
- **API**: http://localhost:8080
- **Health**: http://localhost:8080/health

---

#### **Configuration**

```bash
cp config/config.yaml config/config.local.yaml
```

Key settings:
```yaml
platform:
  name: "Your CTF"
  registration_mode: open  # open, invite, token, disabled

container:
  default_timeout: 2h
  max_per_user: 2

vm:
  enabled: true
  max_per_user: 1

vpn:
  enabled: true
  public_endpoint: your-server.com
```

---

#### **VM Support**

Upload OVA/VMDK/QCOW2 for challenges requiring:
- Kernel exploits (DirtyCOW, DirtyPipe)
- Systemd abuse
- Full network stack
- Active Directory labs

---

#### **Production Deployment**

Docker Compose:
```bash
docker-compose -f docker-compose.prod.yml up -d
```

GCP Terraform: See [deployments/terraform/gcp/](deployments/terraform/gcp/)

---

#### **Environment Variables**

| Variable | Description |
|----------|-------------|
| `ANVIL_DATABASE_HOST` | PostgreSQL host |
| `ANVIL_DATABASE_PASSWORD` | PostgreSQL password |
| `ANVIL_JWT_SECRET` | JWT signing secret (required) |
| `ANVIL_VPN_PRIVATE_KEY` | WireGuard key (required for VPN) |

---

#### **Roadmap**

- [x] Core platform (challenges, users, instances)
- [x] VPN connectivity
- [x] VM support (OVA/VMDK)
- [x] Scoreboard
- [ ] Frontend UI (SvelteKit)
- [ ] Multi-cloud (AWS, Azure)
- [ ] Active Directory labs
- [ ] Attack-Defense mode
