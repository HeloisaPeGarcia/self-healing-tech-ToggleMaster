# Integração com o repo principal e com o time

## 1. Aplicar no `tech-challenge-feature-flags`

Na raiz do repo principal (branch própria, PR para `main`):

```bash
# copiar código e manifests
cp -r <este-repo>/self-healing                  ./self-healing
cp -r <este-repo>/gitops/services/self-healing  ./gitops/services/self-healing
cp    <este-repo>/gitops/argocd/self-healing.yaml ./gitops/argocd/self-healing.yaml
cp    <este-repo>/auth-service-chaos/chaos.go <este-repo>/auth-service-chaos/chaos_test.go ./auth-service/

# patches (todos verificados com git apply --check)
git apply <este-repo>/auth-service-chaos/main.go.patch   # rota /admin/chaos + middleware  (combinar com Gustavo)
git apply <este-repo>/integration/auth-deployment.patch  # CHAOS_ENABLED=true
git apply <este-repo>/integration/terraform-ecr.patch    # repositório ECR togglemaster-self-healing
git apply <este-repo>/integration/workflow-ci.patch      # build/push + sed da tag no deployment

# Action de teste + script E2E + payloads que ele usa
mkdir -p .github/workflows scripts samples
cp <este-repo>/.github/workflows/self-healing-test.yml ./.github/workflows/
cp <este-repo>/scripts/e2e-kind.sh ./scripts/ && chmod +x scripts/e2e-kind.sh
cp <este-repo>/samples/*.json ./samples/
```

Não commite `secret.example.yaml` com valor real.

**Se o Gustavo já mexeu em `auth-service/main.go`**, o `git apply` pode falhar. Nesse caso
aplique à mão — são só 2 mudanças: registrar `/admin/chaos` e trocar `mux` por
`chaosMiddleware(mux)` no `ListenAndServe`. (Se ele envolver o mux com o middleware do OTel,
use `otelhttp.NewHandler(chaosMiddleware(mux), ...)` para o chaos ficar **dentro** do trace.)

## 1b. Action de teste

`self-healing-test.yml` roda em PR/push que toque o receptor, os manifests ou o auth-service, e manualmente (*Actions → Self-Healing Tests → Run workflow*):

| Job | O que garante |
|---|---|
| `unit` | `gofmt`/`vet`/`go test -race` do receptor; testes de chaos do auth (se `auth-service/chaos.go` existir) |
| `manifests` | `kubectl kustomize` + `kubeconform` (schema) e política: sem ClusterRole, `resourceNames` presente, sem `delete/create/update/*`, `replicas: 1` |
| `e2e-kind` | Sobe um kind, aplica os **manifests reais** (só troca a imagem) e executa `scripts/e2e-kind.sh` |

Arquivos que o workflow usa e que precisam existir no repo: `samples/*.json`, `scripts/e2e-kind.sh`,
`gitops/services/self-healing/`. O log do receptor sai como artefato `self-healing-e2e-log`
(serve de evidência extra para o relatório).

Rodar o E2E na sua máquina (requer Docker + kind; **nunca** em cluster real — o script recusa contextos que não sejam `kind-*`):
```bash
kind create cluster --name sh-e2e
docker build -t self-healing:ci ./self-healing && kind load docker-image self-healing:ci --name sh-e2e
scripts/e2e-kind.sh
kind delete cluster --name sh-e2e
```

O E2E **não** cobre: Grafana/PagerDuty/Slack, o alerta real, o chaos HTTP do auth e o Argo CD.
Esses continuam no ensaio manual (`docs/roteiro-prova-real.md`).

## 1c. Preencher alertname/deployment/cooldown sem editar arquivo

Workflow **Self-Healing Config** (*Actions → Self-Healing Config → Run workflow*). Informe uma regra por linha:

```
alertname|deployment|label=valor,label2=valor
AuthHigh5xxRate|auth-service|service=auth-service
ToggleMasterHTTPHealthDown|auth-service|service=auth-service
```
e o cooldown (30–3600 s). Ele valida as entradas, reescreve `config.json` **e** o `resourceNames` do `rbac.yaml`
(sempre juntos), confere que o receptor aceita o resultado e **abre um PR** para revisão (`dry_run` só mostra o resultado).
Na prática: quando a Susana disser o nome do alerta, você roda o workflow, aprova o PR e o Argo CD aplica.

Mesmo gerador na sua máquina: `python3 scripts/render_config.py --rules "Alerta|auth-service" --cooldown 300 [--dry-run]`.
Requisitos no repo: `scripts/render_config.py`, `.github/workflows/self-healing-config.yml`, `self-healing/` e `gitops/services/self-healing/`.
Em *Settings → Actions → General*, habilite "Allow GitHub Actions to create and approve pull requests", senão o passo do PR falha.
**Checks no PR (opcional, recomendado):** PRs abertos com o `GITHUB_TOKEN` não disparam outros workflows. Para que o PR
rode `CI DevSecOps` e `self-healing-test`, crie o secret `PR_TOKEN` (*Settings → Secrets and variables → Actions*) com um PAT
fine-grained restrito a **este repositório** e permissões *Contents: Read and write* e *Pull requests: Read and write*.
Sem o secret, o workflow continua funcionando e emite um aviso (basta fechar e reabrir o PR para rodar os checks).
Quem pode rodar o workflow define o que o receptor pode reiniciar; por isso a mudança sai em PR, não direto na `main`.

## 2. Ordem de subida

1. `terraform apply` (cria o ECR `togglemaster-self-healing`; IMMUTABLE tags).
2. Merge na `main` → o workflow builda/pusha a imagem e reescreve a tag em `deployment.yaml`.
3. `scripts/create-secret.sh` (cria o Secret **antes** de o pod subir; sem ele o pod fica em `CreateContainerConfigError`).
4. `kubectl apply -f gitops/argocd/self-healing.yaml` (ou acrescentar o bloco em `applications.yaml`, que o bootstrap aplica).
5. Conferir: `kubectl -n argocd get applications` → `togglemaster-self-healing` **Synced/Healthy**;
   `kubectl -n togglemaster logs deploy/self-healing` → evento `service_started`.
6. `scripts/test-webhook.sh firing` e depois `twice`, `resolved`, `other`, `badtoken`.
   Para ensaiar **sem derrubar nada**, ponha `DRY_RUN: "true"` no deployment.

## 3. O que combinar com cada pessoa

### Susana (alertas / PagerDuty / Slack)
- Alerta com `alertname` **exatamente** igual ao de `config.json` (hoje `AuthHigh5xxRate`) e label `service=auth-service`.
- Contact point **Webhook** no Grafana:
  - URL: `http://self-healing.togglemaster.svc.cluster.local:8090/webhook`
  - Método POST; *Authorization Header* → Scheme `Bearer`, Credentials = token do Secret
    (`kubectl -n togglemaster get secret togglemaster-self-healing -o jsonpath='{.data.WEBHOOK_TOKEN}' | base64 -d`).
- Mesma política de notificação envia o alerta também a PagerDuty/OpsGenie e Slack/Discord.
- Sugestão de alerta (Loki, datasource já provisionado), regra *Grafana-managed*:
  ```logql
  sum(rate({container="auth-service"} | json | status_code >= 500 [1m]))
  /
  sum(rate({container="auth-service"} | json | status_code > 0 [1m]))
  ```
  Condição: `> 0.05`, `for: 1m` (na gravação), *No data* → **OK**.
- Importante: o Alertmanager do Prometheus está **desligado** no repo; as regras de
  `prometheus/values.yaml` (ex.: `ToggleMasterHTTPHealthDown`) **não notificam ninguém**. Para usá-las
  como plano B, recrie-as como alertas gerenciados pelo Grafana (datasource Prometheus).
- `severity=critical` + *repeat interval* longo. O cooldown protege, mas menos ruído é melhor.

### Gustavo (OTel / APM / auth-service)
- Alinhar o `main.go` do auth (ver nota acima) e o `go.mod` (se adicionar otelhttp).
- O chaos gera log `chaos_fault_injected` (status 500) e a requisição aparece como erro no trace — bom para mostrar no APM.

### João (OTel Collector)
- Nada a mudar: o receptor emite logs JSON em stdout, já coletados pelo Alloy. Se o Collector também
  ler stdout dos pods, haverá **duplicidade** (ponto já levantado no handoff).

## 4. Segurança (para o relatório)
- Token em Secret, comparação em tempo constante, 401 sem token/inválido.
- Role mínima com `resourceNames`; ServiceAccount própria.
- Container non-root, `readOnlyRootFilesystem`, `drop: ALL`, seccomp `RuntimeDefault`.
- `CHAOS_ENABLED` só ligado no laboratório; remova do deployment depois do vídeo.
