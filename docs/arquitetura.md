# Arquitetura do self-healing

## Decisões e porquês

| Decisão | Por quê |
|---|---|
| Receptor **dentro do cluster**, namespace `togglemaster` | O Alloy só coleta logs desse namespace → o log da automação chega ao Loki (evidência). Também dispensa expor endpoint público. |
| Só biblioteca padrão, sem `client-go` | Imagem pequena, build rápido, nada para o DevSecOps (Trivy etc.) reclamar. Chama a API com a ServiceAccount do pod. |
| Ação = patch de `restartedAt` | É exatamente o que `kubectl rollout restart` faz; respeita `maxUnavailable: 0` e o PDB do auth (sem downtime). |
| **Role** (não ClusterRole) com `resourceNames` | Privilégio mínimo: só `get`/`patch` em `deployments/auth-service`, só em `togglemaster`. Um token vazado não derruba o resto. |
| Allowlist `alertname → deployment` em ConfigMap | Alerta desconhecido é ignorado. A regra fica versionada no Git/Argo. |
| Cooldown por deployment (300 s) | O Grafana reenvia alertas em Firing; sem cooldown viraria loop de restart. Reserva atômica; liberada se o restart falhar. |
| 1 réplica | O cooldown é em memória. Se o pod reiniciar, o cooldown zera (aceitável: o restart é idempotente e raro). |
| Resposta 500 só quando o restart **falha** | O Grafana tenta de novo. Cooldown/ignorado/resolved respondem 200 para não gerar retry inútil. |
| Chaos em memória + `CHAOS_ENABLED` + MASTER_KEY + TTL | A cura é demonstrável (restart limpa a falha), e o endpoint não existe fora do laboratório. |
| `/live` nunca é afetado pelo chaos | Se falhasse, o kubelet reiniciaria o container e o mérito seria do Kubernetes, não da automação. |

## Dois modos de falha (escolha conforme o alerta da Susana)

| Modo | O que quebra | Pod continua Ready? | Alerta que pega |
|---|---|---|---|
| `errors` (padrão) | `/validate` → 500 | **Sim** (recebe tráfego, gera 5xx real) | Taxa de 5xx (logs/Loki ou OTel) — o alerta do enunciado |
| `unhealthy` | `/validate` → 500 e `/health` → 503 | Não (sai do Service) | `probe_success == 0` do Blackbox |

Atenção: em `unhealthy` os pods saem do Service, então **não chega tráfego** e o 5xx
some dos logs. Por isso `errors` é o modo recomendado para o alerta de taxa de 5xx.

## Dois caminhos para o alerta de 5xx

| Origem | Como a taxa é calculada | Quando usar |
|---|---|---|
| Logs (Loki) | `status_code >= 500` / `status_code > 0` nos logs JSON do auth | Funciona desde já; depende só do chaos e do tráfego |
| Traces (spanmetrics do Collector) | `traces_span_metrics_calls_total{http_response_status_code=~"5.."}` | Depois que o `auth-service` (Go) for instrumentado; o `otelhttp` precisa ficar **por fora** do `chaosMiddleware` |

O receptor não depende de qual origem dispara: ele só olha `alertname` e labels do webhook.

## Estado por pod

O chaos é por pod. Com 2 réplicas, `scripts/chaos.sh on` liga nas duas (senão ~50% das
requisições falham e o alerta oscila). Depois do restart **os dois pods são novos**, então
a falha some em ambos.

## Limites conhecidos (para a Justificativa Técnica)

- Cooldown em memória (zera se o receptor reiniciar).
- A allowlist exige editar ConfigMap **e** Role juntos; é intencional (dupla trava).
- Restart não resolve falha de dependência (ex.: RDS fora do ar). O self-healing aqui cobre
  falhas de estado do processo.
