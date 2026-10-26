#!/usr/bin/env bash
# Liga/desliga a falha controlada em TODOS os pods do auth-service.
# (O estado é por pod; com 2 réplicas é preciso afetar as duas, senão o alerta
# oscila e a demo fica imprevisível.)
#
# Uso:  scripts/chaos.sh on [errors|unhealthy] [ttl_segundos]
#       scripts/chaos.sh off
#       scripts/chaos.sh status
set -euo pipefail
NS="${NS:-togglemaster}"
ACTION="${1:-status}"
MODE="${2:-errors}"
TTL="${3:-0}"

MASTER_KEY="$(kubectl -n "$NS" get secret togglemaster-runtime-secrets -o jsonpath='{.data.MASTER_KEY}' | base64 -d)"
PODS="$(kubectl -n "$NS" get pods -l app.kubernetes.io/name=auth-service -o jsonpath='{.items[*].metadata.name}')"
[[ -n "$PODS" ]] || { echo "nenhum pod auth-service em $NS"; exit 1; }

PORT=18001
for pod in $PODS; do
  kubectl -n "$NS" port-forward "pod/$pod" "$PORT:8001" >/dev/null 2>&1 &
  PF=$!
  sleep 2
  printf '%s -> ' "$pod"
  case "$ACTION" in
    on)     curl -sS --max-time 5 -X POST "http://127.0.0.1:$PORT/admin/chaos" \
              -H "Authorization: Bearer $MASTER_KEY" \
              -d "{\"enabled\":true,\"mode\":\"$MODE\",\"ttl_seconds\":$TTL}" ;;
    off)    curl -sS --max-time 5 -X DELETE "http://127.0.0.1:$PORT/admin/chaos" \
              -H "Authorization: Bearer $MASTER_KEY" ;;
    status) curl -sS --max-time 5 "http://127.0.0.1:$PORT/admin/chaos" \
              -H "Authorization: Bearer $MASTER_KEY" ;;
    *) kill $PF 2>/dev/null || true; echo "ação desconhecida: $ACTION"; exit 1 ;;
  esac
  echo
  kill $PF 2>/dev/null || true
  wait $PF 2>/dev/null || true
  PORT=$((PORT + 1))
done
