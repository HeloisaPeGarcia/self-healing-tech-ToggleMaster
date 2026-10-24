#!/usr/bin/env bash
# Gera tráfego contínuo em /validate DENTRO do cluster (passa pelo Service, como
# um cliente real). Sem tráfego não há taxa de erro para o alerta calcular.
# Uso: scripts/traffic.sh start | stop
set -euo pipefail
NS="${NS:-togglemaster}"
case "${1:-start}" in
  start)
    kubectl -n "$NS" run chaos-traffic --restart=Never --image=curlimages/curl:8.10.1 --command -- \
      sh -c 'while true; do curl -s -o /dev/null -w "%{http_code}\n" -H "Authorization: Bearer demo" http://auth-service:8001/validate; sleep 0.3; done'
    echo "Tráfego iniciado. Ver códigos: kubectl -n $NS logs -f chaos-traffic"
    ;;
  stop)
    kubectl -n "$NS" delete pod chaos-traffic --ignore-not-found
    ;;
esac
