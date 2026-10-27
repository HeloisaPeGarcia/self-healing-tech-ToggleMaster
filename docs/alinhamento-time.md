# Alinhamento do time (mensagens prontas)

Estado em 27/10: o João publicou o OTel Collector e a instrumentação Python em branches
(`feat/fase4-otel-collector-v2` e `feat/fase4-instrumentacao-python-v2` do fork dele). Os patches deste repo foram
verificados com `git apply --check` contra a `main` do Gabriel **e** contra as duas branches do João.

## João
> João, li as suas branches. Duas coisas do meu lado: (1) o endpoint de chaos é `POST /admin/chaos`
> (protegido por MASTER_KEY e `CHAOS_ENABLED=true`), não `/chaos/enable`; e o meu script liga nos **dois** pods,
> então o erro fica perto de 100%, não 50%. (2) Quando você sincronizar o Collector (antes do Alloy), consegue conferir
> no Explore → Loki se `{container="self-healing"}` retorna as linhas JSON intactas? É a minha evidência do relatório.
> Também adicionei no bootstrap a criação do Secret do meu webhook e a Application; o patch aplica limpo na sua branch.

## Gustavo
> Gustavo, no `auth-service/main.go` preciso de 2 mudanças do chaos: registrar `/admin/chaos` e envolver o mux com
> `chaosMiddleware`. Para o alerta por traces funcionar, o `otelhttp` precisa ficar **por fora**:
> `otelhttp.NewHandler(chaosMiddleware(mux), "auth-service")`. O arquivo `chaos.go` entra em `auth-service/`.
> Quem fecha o merge do `main.go`, você ou eu? Quando o auth estiver instrumentado, me avisa para eu testar o 5xx no trace.

## Susana
> Susana, preciso de 3 coisas para o self-healing: (1) o `alertname` exato do alerta de 5xx do auth (hoje o config
> espera `AuthHigh5xxRate`, com label `service=auth-service`); (2) um contact point **Webhook** em
> `http://self-healing.togglemaster.svc.cluster.local:8090/webhook` com *Authorization Header* Bearer (te passo o token
> em privado); (3) o alerta no Grafana, já que o Alertmanager do Prometheus está desligado. Sugiro começar pela consulta de
> logs (Loki), que funciona agora, e migrar para a de traces quando o auth for instrumentado. Detalhes em `docs/integracao.md`.

## Gabriel
> Gabriel, preciso do merge de três coisas no repo principal: o `terraform-ecr.patch` (repositório ECR do self-healing),
> o `workflow-ci.patch` e o `bootstrap.patch`. Também o `terraform apply` antes do primeiro build. Não rode `destroy`
> antes da gravação.
