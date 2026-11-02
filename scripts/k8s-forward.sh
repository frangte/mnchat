#!/usr/bin/env bash
# make k8s-forward: forward the MNChat Services in local k3s to the same localhost ports
# Compose publishes, so `make web` and the URLs in the README work unchanged.
#
# Ctrl-C stops every forward. A forward also ends when its pod restarts, and then this
# script stops the others and exits, so a dead forward is never silent. Run it again.
# Written for macOS's bash 3.2, so no `wait -n`.
set -eo pipefail

ctx="${KUBE_CONTEXT:-colima}"
pids=()
trap 'kill "${pids[@]}" 2>/dev/null || true' EXIT

for spec in api:8080 grafana:3000 prometheus:9090; do
  svc="${spec%%:*}"
  port="${spec##*:}"
  kubectl --context "$ctx" -n mnchat port-forward "svc/$svc" "$port:$port" >/dev/null &
  pids+=("$!")
done

echo "api http://localhost:8080 · Grafana http://localhost:3000 · Prometheus http://localhost:9090"
echo "UI: make web in another terminal. Ctrl-C stops the forwards."

while true; do
  for pid in "${pids[@]}"; do
    if ! kill -0 "$pid" 2>/dev/null; then
      echo "a port-forward stopped (a pod restarted, or the port is taken), stopping the others" >&2
      exit 1
    fi
  done
  sleep 1
done
