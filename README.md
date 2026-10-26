# ToggleMaster — Self-Healing (Tech Challenge Fase 4)
 receptor de webhook com RBAC, filtro e cooldown; falha controlada
(chaos) no `auth-service`; roteiro/ensaios da "prova real"; logs da automação como evidência.

Este diretório espelha a estrutura do repo principal
(`tech-challenge-feature-flags`) para ser **copiado/mesclado** nele — o Argo CD e o CI
só enxergam o que está nesse repo.

## Fluxo

```
Grafana Alerting ──(alerta Firing)──► Contact points
                                       ├─► PagerDuty/OpsGenie   (Susana)
                                       ├─► Slack/Discord        (Susana)
                                       └─► webhook ► self-healing (este repo)
                                                       │ token ✔  allowlist ✔  cooldown ✔
                                                       ▼
                                          PATCH deployment/auth-service (restartedAt)
                                                       ▼
                                          rolling update → pods novos (chaos some)
```

## Estrutura

| Pasta | O que é |
|---|---|
| `self-healing/` | Receptor em Go (só stdlib) + testes + Dockerfile |
| `auth-service-chaos/` | `chaos.go` + testes + `main.go.patch` para o auth-service |
| `gitops/services/self-healing/` | Deployment, Service, ServiceAccount, Role/RoleBinding, ConfigMap (allowlist) |
| `gitops/argocd/self-healing.yaml` | Application do Argo CD |
| `integration/` | Patches para o repo principal (ECR/Terraform, workflow, deployment do auth) |
| `scripts/` | `create-secret`, `test-webhook`, `chaos`, `traffic`, `collect-evidence`, `e2e-kind` |
| `.github/workflows/self-healing-test.yml` | Action de teste: unitários, manifests/RBAC e E2E em kind |
| `.github/workflows/self-healing-config.yml` + `scripts/render_config.py` | Preenche allowlist e RBAC a partir de inputs e abre PR |
| `samples/` | Payloads de webhook do Grafana (firing / resolved / outro alerta) |
| `docs/` | Arquitetura, integração, roteiro da prova real, evidências/relatório |

## Status

| Item | Estado |
|---|---|
| Receptor: auth por token, allowlist, cooldown, restart via API | Pronto, 10 testes passando |
| Chaos no auth (modos `errors`/`unhealthy`, TTL, só em memória) | Pronto, 5 testes passando |
| Manifests GitOps + RBAC mínimo | Pronto (YAML validado; **não aplicado em cluster**) |
| Patches no repo principal | Aplicam limpo em `main` (`git apply --check`) |
| Action de teste (`self-healing-test.yml`) + `scripts/e2e-kind.sh` | Escritos e validados estaticamente; **o job E2E em kind ainda não foi executado** (rode no GitHub) |
| Build da imagem / deploy no EKS / teste com Grafana real | **Pendente — precisa do cluster** |
| Alerta no Grafana, PagerDuty/OpsGenie, Slack | Depende da Susana (ver `docs/integracao.md`) |

Leia nesta ordem: `docs/integracao.md` → `docs/roteiro-prova-real.md` → `docs/evidencias-e-relatorio.md`.
