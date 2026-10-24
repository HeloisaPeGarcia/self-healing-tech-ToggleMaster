#!/usr/bin/env bash
# Salva, com timestamp, as evidências textuais do self-healing em evidence/.
# Rode logo APÓS a demonstração (antes de os logs rotacionarem).
set -euo pipefail
NS="${NS:-togglemaster}"
OUT="$(cd "$(dirname "$0")/.." && pwd)/evidence/$(date -u +%Y%m%dT%H%M%SZ)"
mkdir -p "$OUT"

kubectl -n "$NS" logs deploy/self-healing --since=30m > "$OUT/self-healing.log" || true
kubectl -n "$NS" get pods -l app.kubernetes.io/name=auth-service -o wide > "$OUT/auth-pods.txt" || true
kubectl -n "$NS" get deploy auth-service \
  -o jsonpath='restartedAt={.spec.template.metadata.annotations.kubectl\.kubernetes\.io/restartedAt}{"\n"}' \
  > "$OUT/auth-restartedAt.txt" || true
kubectl -n "$NS" rollout history deploy/auth-service > "$OUT/auth-rollout-history.txt" || true
kubectl -n "$NS" get events --sort-by=.lastTimestamp | tail -40 > "$OUT/events.txt" || true

echo "Evidências salvas em: $OUT"
grep -E 'alert_received|rollout_restart_triggered|action_skipped|rollout_restart_failed' "$OUT/self-healing.log" || true
