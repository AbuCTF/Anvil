# Anvil on GKE — infrastructure (Terraform)

The H7CTF'26 platform + challenge infrastructure as code. Runs on the GCP free
trial for dev/rehearsal; bursts on credits for the live event. Full reasoning:
the "CTF Infrastructure: State of the Art → Anvil" design doc + `CTF26/infra-buildlog.md`.

## Where it runs

Terraform runs **on atom** (`ssh atom`), where `gcloud` is authed as the project
owner. Files live in the version-controlled Anvil repo; atom pulls and applies.

## Layout (built phase by phase, each checkpointed before apply)

| File | Phase | What |
| --- | --- | --- |
| `versions.tf` | P0 | terraform + provider pins, backend |
| `variables.tf` / `terraform.tfvars` | P0 | project, region, the IP plan |
| `apis.tf` | P0 | enable required GCP APIs |
| `network.tf` | P0 | VPC, subnet (VPC-native), Cloud NAT, firewall |
| `gke.tf` (next) | P1 | zonal cluster (Dataplane V2, private nodes) |
| `nodepools.tf` (next) | P1 | platform (on-demand) + challenges (Spot, highmem) |
| `data.tf` (next) | P3 | Cloud SQL (Postgres) + Memorystore (Redis) |

## IP plan

- Nodes: `10.128.0.0/20` · Pods: `100.64.0.0/14` · Services: `100.68.0.0/20`.
- Pods/services sit in `100.64.0.0/10` on purpose — they must not collide with
  the atom-side WireGuard peers (`10.64/12`) or per-team VM subnets (`10.80/12`).
- External IPs: 3 in use at event time (shared ingress, WireGuard, Cloud NAT),
  5 of the trial's 8 held in reserve.

## Running it (P0 foundation — free, no cluster yet)

```bash
# on atom, one-time: install terraform
# (arm64 build) then:
cd ~/Anvil/infra/gke
terraform init
terraform plan -out tfplan     # review — this is the checkpoint
terraform apply tfplan         # network + NAT + firewall + APIs (all free)
```

State starts local; once we create the `h7ctf26-tfstate` GCS bucket we migrate
the backend (uncomment `backend "gcs"` in `versions.tf`, then `terraform init -migrate-state`).

## Secrets (never in this repo)

- Cloudflare DNS token: `atom:~/.secrets/cf-dns-token` → becomes a K8s Secret for
  cert-manager (P2). Pass to the CF provider via `CLOUDFLARE_API_TOKEN` env, never tfvars.
- DB passwords, SSO shared secret: generated/stored as K8s Secrets, not in state where avoidable.

## Rule

Nothing is `apply`-ed without a reviewed `terraform plan` first. The foundation
(network/APIs) is free; the cluster (P1) is the first line that spends credits.
