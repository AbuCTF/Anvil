#!/usr/bin/env bash
# Prints the tcpproxy LoadBalancer Service for a pool range, e.g. 40000-49999.
# k8s services have no port ranges, so every pool port is listed; the GKE L4 NLB
# folds them back into one forwarding-rule range and one firewall range.
set -euo pipefail
start=${1%-*} end=${1#*-}
cat <<EOF
apiVersion: v1
kind: Service
metadata:
  name: tcpproxy
  namespace: anvil-instancer
  annotations:
    # external passthrough L4 NLB (preserves TCP half-close)
    cloud.google.com/l4-rbs: "enabled"
spec:
  type: LoadBalancer
  # reserved static address (anvil-tcpproxy-ip): pwn/web3.h7tex.com point here.
  loadBalancerIP: 34.180.1.168
  externalTrafficPolicy: Cluster
  # the NLB delivers to node ips (GCE_VM_IP NEG), so no node ports are needed;
  # allocating them would cap the pool at the 2768-port NodePort range.
  allocateLoadBalancerNodePorts: false
  selector: { app: tcpproxy }
  ports:
EOF
for ((p = start; p <= end; p++)); do
  printf '    - { name: t%d, port: %d, targetPort: %d, protocol: TCP }\n' "$p" "$p" "$p"
done
