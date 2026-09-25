#!/usr/bin/env bash
# Prints a DaemonSet that pulls every challenge image onto each challenge node
# (label prepull=true), so the first launch on a fresh node doesn't wait on a pull.
# stdin: one image ref per line. A container that can't run `sleep` just restarts;
# its image is already cached, which is all this is for.
#   bash prepull.sh < images.txt | kubectl apply -f -
set -euo pipefail
cat <<EOF
apiVersion: apps/v1
kind: DaemonSet
metadata:
  name: prepull
  namespace: capacity
spec:
  selector:
    matchLabels: { app: prepull }
  template:
    metadata:
      labels: { app: prepull }
    spec:
      nodeSelector: { prepull: "true" }
      runtimeClassName: gvisor
      automountServiceAccountToken: false
      enableServiceLinks: false
      terminationGracePeriodSeconds: 0
      containers:
EOF
n=0
while read -r img; do
  [ -n "$img" ] || continue
  cat <<EOF
        - name: i$n
          image: $img
          imagePullPolicy: IfNotPresent
          command: ["/bin/sh", "-c", "exec sleep 2147483647"]
          resources:
            requests: { cpu: 1m, memory: 4Mi }
            limits: { cpu: 10m, memory: 32Mi }
EOF
  n=$((n + 1))
done
