# Evidências e trechos do relatório (parte Self-Healing)

## Prints obrigatórios (PDF)
1. **Log da automação** — `kubectl logs deploy/self-healing` (ou Grafana/Loki) com `alert_received` → `rollout_restart_triggered`.
2. **Execução da cura** — `kubectl get pods -w` mostrando pods antigos terminando e novos subindo; saída de `auth-restartedAt.txt`.
3. **Cooldown** — linha `action_skipped ... reason: cooldown`.
4. **Alerta Firing → Resolved** no Grafana (junto com Susana).
5. **RBAC** — `kubectl -n togglemaster get role,rolebinding self-healing-restarter -o yaml` e
   `kubectl auth can-i delete deployments/auth-service --as=system:serviceaccount:togglemaster:self-healing -n togglemaster` → `no`.

`scripts/collect-evidence.sh` gera os arquivos textuais em `evidence/<timestamp>/`.

## Texto-base para a Justificativa Técnica (ajuste ao que foi de fato feito)

> **Self-healing.** Optamos por um receptor de webhook próprio, implantado no cluster via GitOps, em vez de
> Lambda ou GitHub Action, porque a remediação precisa acontecer perto do plano de controle, sem expor
> credenciais do cluster a serviços externos e com logs coletados pela mesma stack (Alloy → Loki). O
> Grafana Alerting chama o webhook (autenticado por token) quando o alerta de taxa de 5xx do auth-service
> entra em Firing. O receptor aplica uma allowlist alerta→deployment, respeita um cooldown de 5 minutos para
> evitar loops de restart e executa o equivalente a `kubectl rollout restart` por meio da API do Kubernetes,
> com uma ServiceAccount cujo Role permite apenas `get` e `patch` no Deployment `auth-service`.
> A falha usada na demonstração é injetada por um endpoint administrativo protegido (MASTER_KEY, habilitado
> por variável de ambiente e com estado apenas em memória), de modo que o restart comprovadamente remove a
> falha. Limitações: o cooldown é em memória e o restart não corrige falhas de dependências externas.

## Linha do tempo para anotar durante o ensaio final
| Evento | Horário |
|---|---|
| `chaos_enabled` | |
| Alerta Firing | |
| Incidente aberto / Slack | |
| `rollout_restart_triggered` | |
| Pods novos Ready | |
| Alerta Resolved | |
