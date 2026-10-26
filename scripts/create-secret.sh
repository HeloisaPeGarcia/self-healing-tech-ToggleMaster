#!/usr/bin/env bash
# Cria o Secret com o token do webhook (fora do Git). Idempotente: só gera outro
# token com --rotate. O token NUNCA é impresso; leia-o com o comando indicado ao final.
set -euo pipefail
NS="${NS:-togglemaster}"
NAME="togglemaster-self-healing"

if kubectl -n "$NS" get secret "$NAME" >/dev/null 2>&1 && [[ "${1:-}" != "--rotate" ]]; then
  echo "Secret $NAME já existe em $NS (use --rotate para trocar o token)."
  exit 0
fi

TOKEN="$(openssl rand -hex 32)"
kubectl -n "$NS" create secret generic "$NAME" \
  --from-literal=WEBHOOK_TOKEN="$TOKEN" \
  --dry-run=client -o yaml | kubectl apply -f -
unset TOKEN

cat <<MSG
Secret $NAME configurado em $NS.

Para colar o token no contact point do Grafana (Credentials do Authorization Header):
  kubectl -n $NS get secret $NAME -o jsonpath='{.data.WEBHOOK_TOKEN}' | base64 -d; echo

Se rotacionou, reinicie o receptor para ele ler o novo valor:
  kubectl -n $NS rollout restart deployment/self-healing
MSG
