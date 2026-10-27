# Roteiro da prova real (vídeo) — ~6 a 8 min

Pré-requisitos: tráfego e alerta prontos (Susana), receptor `Synced/Healthy`, `CHAOS_ENABLED=true`.
Janela de alerta curta (`for: 1m`) só para a gravação.

## Layout de terminais
- **T1** `kubectl -n togglemaster get pods -l app.kubernetes.io/name=auth-service -w`
- **T2** `kubectl -n togglemaster logs -f deploy/self-healing`
- **T3** comandos do roteiro
- Navegador: Grafana (alerta + Explore/Loki), PagerDuty/OpsGenie, Slack.

## Passo a passo

| # | Ação | O que mostrar / dizer |
|---|---|---|
| 0 | `kubectl -n argocd get applications` | Todas `Synced/Healthy`, inclusive `togglemaster-self-healing`. |
| 1 | T1 | 2 pods do auth `Running` (anotar nomes/AGE). |
| 2 | `scripts/traffic.sh start` | Tráfego contínuo em `/validate` (sem tráfego não há taxa). |
| 3 | `scripts/chaos.sh on errors` | A falha entra nas **duas** réplicas. Log `chaos_enabled` no Loki (`{container="auth-service"}`). |
| 4 | Grafana → Alerting | Estado vai a *Pending* → **Firing**. Mostrar a taxa de 5xx > 5%. |
| 5 | PagerDuty/OpsGenie + Slack | Incidente aberto + notificação detalhada. |
| 6 | T2 e Grafana → Explore → Loki | `alert_received` → `rollout_restart_triggered` (no Loki: `{container="self-healing"}`; os logs chegam por Alloy → OTel Collector → Loki). |
| 7 | T1 | Pods novos nascendo (AGE baixo), antigos terminando. **Esta é a prova.** |
| 8 | Grafana | Taxa de 5xx volta a 0; alerta **Resolved** (o receptor loga `alert_ignored: status_resolved`). |
| 9 | (opcional) reenviar webhook | `scripts/test-webhook.sh twice` → segunda chamada em `cooldown` (mostra proteção anti-loop). |
| 10 | `scripts/traffic.sh stop` | Limpeza. |
| 11 | `scripts/collect-evidence.sh` | Salva logs/estado em `evidence/<timestamp>/`. |

Se o ensaio precisar repetir em menos de 5 min, o cooldown vai bloquear: espere a janela,
ou reduza `cooldownSeconds` no ensaio (e **volte para 300** na gravação).

## Checklist de ensaio (o grupo combinou **3 ensaios completos até 05/11**)

- [ ] `test-webhook.sh badtoken` → 401
- [ ] `test-webhook.sh firing` → pods reiniciam
- [ ] `test-webhook.sh twice` → 2º cai em cooldown
- [ ] `test-webhook.sh resolved` e `other` → ignorados
- [ ] Alerta **real** do Grafana (não o `curl`) dispara o restart ponta a ponta
- [ ] Collector sincronizado **antes** do Alloy; `{container="self-healing"}` retorna linhas no Explore → Loki
- [ ] Se o alerta usar traces (opção A): o 5xx do chaos aparece no APM e em `traces_span_metrics_calls_total` (exige o auth instrumentado)
- [ ] Argo CD continua `Synced` depois do restart (não reverteu a annotation)
- [ ] Chaos desligou sozinho após o restart (`scripts/chaos.sh status` → `enabled:false` nos pods novos)
- [ ] Tempo total do Firing até pods novos cronometrado (para decidir o `for` e cortes do vídeo)
- [ ] Grafana reenviando Firing durante o incidente não causa 2º restart (cooldown)

## Datas combinadas pelo grupo
01/11 integração pronta · 05/11 evidências salvas e incidente ensaiado 3 vezes · 07–08/11 reunião final e gravação · 17/11 prazo oficial.

## Plano B se o alerta de 5xx atrasar
Se o alerta por **traces** ainda não existir (auth sem instrumentação), use a opção B (logs/Loki) de `docs/integracao.md`: ela depende só do chaos e do tráfego.
`scripts/chaos.sh on unhealthy` derruba `/health`; o alerta `probe_success == 0` (recriado no Grafana) dispara e o
`config.json` já tem a regra `ToggleMasterHTTPHealthDown` com `service=auth-service`.

## Problemas prováveis
| Sintoma | Causa provável |
|---|---|
| 401 no log do receptor | Token do contact point ≠ Secret (ou Secret rotacionado sem restart do receptor) |
| `rollout_restart_failed ... 403` | Deployment não está em `resourceNames` do Role |
| Alerta Firing mas nada acontece | `alertname`/label diferente do `config.json` → veja `alert_ignored: not_in_allowlist` |
| Pods não reiniciam e logs ok | Imagem do receptor sem acesso ao ECR / `ImagePullBackOff` (tag `bootstrap` ainda não substituída pelo CI) |
| Chaos só em 1 pod | Use `scripts/chaos.sh` (todos os pods), não um `curl` isolado |
