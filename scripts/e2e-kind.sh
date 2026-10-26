#!/usr/bin/env bash
# Teste ponta a ponta do self-healing em um cluster DESCARTÁVEL (kind).
#
# Prova, com o RBAC e os manifests reais, que:
#   1. token errado        -> 401
#   2. alerta firing       -> o Deployment auth-service é reiniciado (pod substituído)
#   3. segundo firing      -> bloqueado pelo cooldown (sem 2º restart)
#   4. resolved / outro    -> ignorados
#   5. outro Deployment    -> intocado
#   6. RBAC                -> só get/patch em auth-service
#
# Uso local:
#   kind create cluster --name sh-e2e
#   docker build -t self-healing:ci ./self-healing && kind load docker-image self-healing:ci --name sh-e2e
#   scripts/e2e-kind.sh
#   kind delete cluster --name sh-e2e
#
# NÃO rode em cluster real: ele cria/recria o namespace togglemaster.
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
NS="${NS:-togglemaster}"
IMAGE="${IMAGE:-self-healing:ci}"
PORT="${PORT:-18090}"
SA="system:serviceaccount:${NS}:self-healing"
LOG_OUT="${LOG_OUT:-$ROOT/e2e-self-healing.log}"
PF_PID=""

dump() {
  echo "::group::diagnóstico: pods e deployments"
  kubectl -n "$NS" get pods,deploy -o wide || true
  echo "::endgroup::"

  echo "::group::diagnóstico: estado do container (motivo de não ficar pronto)"
  kubectl -n "$NS" get pods -l app.kubernetes.io/name=self-healing \
    -o jsonpath='{range .items[*]}{.metadata.name}{" imagem="}{.spec.containers[0].image}{" waiting="}{.status.containerStatuses[0].state.waiting.reason}{" | "}{.status.containerStatuses[0].state.waiting.message}{" terminated="}{.status.containerStatuses[0].lastState.terminated.reason}{" exit="}{.status.containerStatuses[0].lastState.terminated.exitCode}{"\n"}{end}' || true
  echo "::endgroup::"

  echo "::group::diagnóstico: eventos do pod (ImagePull, probes, OOM...)"
  kubectl -n "$NS" describe pod -l app.kubernetes.io/name=self-healing | sed -n '/^Events:/,$p' || true
  kubectl -n "$NS" get events --sort-by=.lastTimestamp | tail -30 || true
  echo "::endgroup::"

  echo "::group::diagnóstico: logs do receptor (atual e anterior)"
  kubectl -n "$NS" logs deploy/self-healing --tail=200 || true
  echo "--- logs da execução anterior (se houve crash) ---"
  kubectl -n "$NS" logs deploy/self-healing --previous --tail=200 2>&1 || true
  echo "::endgroup::"

  echo "::group::diagnóstico: imagens no nó do kind"
  docker exec "$(kind get clusters | head -n1)-control-plane" crictl images 2>/dev/null | grep -E 'self-healing|IMAGE' || echo "(não consegui listar imagens do nó)"
  echo "::endgroup::"
}
fail() { echo "::error::FALHOU: $*"; dump; exit 1; }
ok()   { echo "OK  $*"; }
cleanup() { if [[ -n "$PF_PID" ]]; then kill "$PF_PID" 2>/dev/null || true; fi; }
trap cleanup EXIT

# --- 0. segurança: só roda em kind ------------------------------------------------
CTX="$(kubectl config current-context)"
[[ "$CTX" == kind-* ]] || fail "contexto '$CTX' não é kind; recusando rodar em cluster real"

# --- 1. ambiente ------------------------------------------------------------------
kubectl create namespace "$NS" --dry-run=client -o yaml | kubectl apply -f -

# "auth-service" e "flag-service" de mentira: só precisam existir como Deployments.
for d in auth-service flag-service; do
  cat <<EOF | kubectl apply -f -
apiVersion: apps/v1
kind: Deployment
metadata:
  name: ${d}
  namespace: ${NS}
spec:
  replicas: 1
  selector:
    matchLabels:
      app: ${d}
  template:
    metadata:
      labels:
        app: ${d}
    spec:
      containers:
      - name: app
        image: busybox:1.36
        command: ["sleep", "3600"]
EOF
done

TOKEN="$(openssl rand -hex 16)"
kubectl -n "$NS" create secret generic togglemaster-self-healing \
  --from-literal=WEBHOOK_TOKEN="$TOKEN" --dry-run=client -o yaml | kubectl apply -f -

# Manifests REAIS do GitOps; só a imagem é trocada pela que o CI carregou no kind.
kubectl kustomize "$ROOT/gitops/services/self-healing" \
  | sed -E "s#image: .*togglemaster-self-healing:.*#image: ${IMAGE}#" \
  | kubectl apply -f -

kubectl -n "$NS" rollout status deploy/auth-service --timeout=120s
kubectl -n "$NS" rollout status deploy/flag-service --timeout=120s
kubectl -n "$NS" rollout status deploy/self-healing --timeout=120s \
  || fail "self-healing não ficou pronto"
ok "receptor e deployments de teste no ar"

# --- 2. acesso ao receptor --------------------------------------------------------
kubectl -n "$NS" port-forward svc/self-healing "${PORT}:8090" >/dev/null 2>&1 &
PF_PID=$!
for _ in $(seq 1 30); do
  curl -fsS "http://127.0.0.1:${PORT}/healthz" >/dev/null 2>&1 && break
  sleep 1
done
curl -fsS "http://127.0.0.1:${PORT}/healthz" >/dev/null || fail "receptor não respondeu em /healthz"

post() { # $1 = token, $2 = arquivo de samples/ ; grava corpo em /tmp/sh-resp, imprime o HTTP code
  curl -s -o /tmp/sh-resp -w '%{http_code}' --max-time 15 \
    -X POST "http://127.0.0.1:${PORT}/webhook" \
    -H "Authorization: Bearer $1" -H "Content-Type: application/json" \
    --data @"$ROOT/samples/$2"
}
restarted_at() { # annotation que o rollout restart grava
  kubectl -n "$NS" get deploy "$1" \
    -o "jsonpath={.spec.template.metadata.annotations.kubectl\\.kubernetes\\.io/restartedAt}"
}

[[ -z "$(restarted_at auth-service)" ]] || fail "auth-service já tinha restartedAt antes do teste"

# --- 3. token errado --------------------------------------------------------------
code="$(post "token-errado" grafana-firing-auth.json)"
[[ "$code" == "401" ]] || fail "token errado deveria dar 401, deu $code"
[[ -z "$(restarted_at auth-service)" ]] || fail "token errado não pode reiniciar nada"
ok "token errado -> 401, nada reiniciado"

# --- 4. firing -> restart ---------------------------------------------------------
OLD_POD="$(kubectl -n "$NS" get pods -l app=auth-service -o name | head -n1)"
code="$(post "$TOKEN" grafana-firing-auth.json)"
[[ "$code" == "200" ]] || fail "firing deveria dar 200, deu $code ($(cat /tmp/sh-resp))"
grep -q '"action":"restarted"' /tmp/sh-resp || fail "resposta sem action=restarted: $(cat /tmp/sh-resp)"

FIRST="$(restarted_at auth-service)"
[[ -n "$FIRST" ]] || fail "annotation restartedAt não foi gravada em auth-service"
kubectl -n "$NS" rollout status deploy/auth-service --timeout=120s || fail "rollout do auth-service não concluiu"
kubectl -n "$NS" wait --for=delete "$OLD_POD" --timeout=90s || fail "pod antigo ($OLD_POD) não foi substituído"
ok "firing -> auth-service reiniciado (pod antigo substituído)"

# --- 5. cooldown ------------------------------------------------------------------
code="$(post "$TOKEN" grafana-firing-auth.json)"
[[ "$code" == "200" ]] || fail "2º firing deveria dar 200, deu $code"
grep -q 'cooldown' /tmp/sh-resp || fail "2º firing deveria cair em cooldown: $(cat /tmp/sh-resp)"
[[ "$(restarted_at auth-service)" == "$FIRST" ]] || fail "cooldown não impediu o 2º restart"
ok "2º firing -> cooldown, sem novo restart"

# --- 6. resolved e alerta fora da allowlist --------------------------------------
code="$(post "$TOKEN" grafana-resolved-auth.json)"
[[ "$code" == "200" ]] && grep -q 'ignored' /tmp/sh-resp || fail "resolved deveria ser ignorado: $(cat /tmp/sh-resp)"
code="$(post "$TOKEN" grafana-other-alert.json)"
[[ "$code" == "200" ]] && grep -q 'not_in_allowlist' /tmp/sh-resp || fail "alerta fora da allowlist deveria ser ignorado: $(cat /tmp/sh-resp)"
[[ "$(restarted_at auth-service)" == "$FIRST" ]] || fail "resolved/other não podem reiniciar"
ok "resolved e alerta fora da allowlist -> ignorados"

# --- 7. isolamento ----------------------------------------------------------------
[[ -z "$(restarted_at flag-service)" ]] || fail "flag-service foi tocado indevidamente"
ok "outros Deployments intocados"

# --- 8. RBAC mínimo ---------------------------------------------------------------
can() { kubectl auth can-i "$@" --as="$SA" -n "$NS" 2>/dev/null || true; }
expect() { # $1 = esperado, resto = args do can-i
  local want="$1"; shift
  local got; got="$(can "$@")"
  [[ "$got" == "$want" ]] || fail "RBAC: 'can-i $*' deveria ser '$want', veio '$got'"
}
expect yes patch  deployments.apps/auth-service
expect yes get    deployments.apps/auth-service
expect no  patch  deployments.apps/flag-service
expect no  delete deployments.apps/auth-service
expect no  create deployments.apps
expect no  get    secrets
expect no  list   pods
ok "RBAC: só get/patch em auth-service"

# --- 9. evidência -----------------------------------------------------------------
kubectl -n "$NS" logs deploy/self-healing > "$LOG_OUT" || true
for ev in webhook_unauthorized rollout_restart_triggered action_skipped alert_ignored; do
  grep -q "\"event\":\"$ev\"" "$LOG_OUT" || fail "log do receptor sem o evento '$ev'"
done
ok "log estruturado contém os eventos esperados (salvo em $LOG_OUT)"

echo
echo "TODOS OS TESTES E2E PASSARAM"
