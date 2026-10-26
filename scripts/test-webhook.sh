#!/usr/bin/env bash
# Envia um payload de exemplo ao receptor (via port-forward) e mostra a resposta.
# Uso: scripts/test-webhook.sh firing | resolved | other | badtoken | twice
set -euo pipefail
NS="${NS:-togglemaster}"
CASE="${1:-firing}"
DIR="$(cd "$(dirname "$0")/.." && pwd)/samples"
PORT="${PORT:-18090}"

TOKEN="$(kubectl -n "$NS" get secret togglemaster-self-healing -o jsonpath='{.data.WEBHOOK_TOKEN}' | base64 -d)"

kubectl -n "$NS" port-forward svc/self-healing "$PORT:8090" >/dev/null 2>&1 &
PF=$!
trap 'kill $PF 2>/dev/null || true' EXIT
sleep 2

send() { # $1 = arquivo, $2 = token
  curl -sS -o /dev/stderr -w "\nHTTP %{http_code}\n" --max-time 15 \
    -X POST "http://127.0.0.1:$PORT/webhook" \
    -H "Authorization: Bearer $2" -H "Content-Type: application/json" \
    --data @"$DIR/$1"
}

case "$CASE" in
  firing)   send grafana-firing-auth.json "$TOKEN" ;;
  resolved) send grafana-resolved-auth.json "$TOKEN" ;;
  other)    send grafana-other-alert.json "$TOKEN" ;;
  badtoken) send grafana-firing-auth.json "token-errado" ;;
  twice)    send grafana-firing-auth.json "$TOKEN"; echo "--- segundo envio (deve cair no cooldown) ---"; send grafana-firing-auth.json "$TOKEN" ;;
  *) echo "caso desconhecido: $CASE"; exit 1 ;;
esac
