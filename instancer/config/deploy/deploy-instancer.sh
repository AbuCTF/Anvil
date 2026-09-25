#!/usr/bin/env bash
# Deploy / update the Anvil instancer operator. Run from the instancer/ dir on a
# box with kubectl pointed at the GKE cluster (atom). Idempotent.
set -euo pipefail
cd "$(dirname "$0")/../.."

# The CRD embeds a full PodSpec schema, so its annotations blow past kubectl's
# client-side apply limit — server-side apply is required.
kubectl apply --server-side --force-conflicts -f config/crd/
kubectl apply -f config/rbac/role.yaml
kubectl apply -f config/deploy/traefik-tls.yaml
kubectl apply -f config/deploy/flowschema.yaml
kubectl apply -f config/deploy/operator.yaml
# raw-TCP half-close proxy + its L4 NLB (routes pool ports to instances).
# one pool range, read from the proxy manifest; the operator must agree.
pool=$(sed -n 's/.*PROXY_POOL_RANGE, value: "\([0-9]*-[0-9]*\)".*/\1/p' config/deploy/tcpproxy.yaml)
grep -q "value: \"$pool\"" config/deploy/operator.yaml || { echo "operator pool range != $pool" >&2; exit 1; }
kubectl apply -f config/deploy/tcpproxy.yaml
# thousands of ports: server-side apply (no last-applied annotation size limit).
bash config/deploy/tcpproxy-service.sh "$pool" | kubectl apply --server-side --force-conflicts -f -

# :latest can be a no-op to the Deployment if the digest changed; force a pull.
kubectl -n anvil-instancer rollout restart deploy/instancer deploy/tcpproxy
kubectl -n anvil-instancer rollout status deploy/instancer --timeout=120s
kubectl -n anvil-instancer rollout status deploy/tcpproxy --timeout=120s
