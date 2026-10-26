#!/usr/bin/env python3
"""Gera a allowlist do self-healing a partir de parâmetros (usado pelo workflow
self-healing-config.yml, mas roda local também).

Atualiza DOIS arquivos de forma consistente (a dupla trava do projeto):
  - gitops/services/self-healing/config.json  (quais alertas disparam qual restart)
  - gitops/services/self-healing/rbac.yaml    (resourceNames: quais Deployments o Role permite)

Formato de --rules: uma regra por linha:
    alertname|deployment|label=valor,label2=valor2     (labels opcionais)
Exemplo:
    AuthHigh5xxRate|auth-service|service=auth-service
    ToggleMasterHTTPHealthDown|auth-service|service=auth-service

Só usa a biblioteca padrão. Entradas são validadas por regex ANTES de qualquer escrita.
"""
import argparse
import json
import re
import sys
from pathlib import Path

ALERT_RE = re.compile(r"^[A-Za-z0-9_:.\- ]{1,100}$")
DEPLOY_RE = re.compile(r"^[a-z0-9]([-a-z0-9]*[a-z0-9])?$")
LABEL_KEY_RE = re.compile(r"^[A-Za-z_][A-Za-z0-9_]{0,62}$")
LABEL_VAL_RE = re.compile(r"^[A-Za-z0-9_.:/\-]{1,100}$")
MAX_RULES = 10
COOLDOWN_MIN, COOLDOWN_MAX = 30, 3600

RESOURCE_NAMES_RE = re.compile(r"^(\s*)resourceNames:.*$", re.MULTILINE)


class InputError(ValueError):
    pass


def parse_rules(text: str):
    rules = []
    for n, raw in enumerate(text.splitlines(), start=1):
        line = raw.strip()
        if not line or line.startswith("#"):
            continue
        parts = [p.strip() for p in line.split("|")]
        if len(parts) not in (2, 3):
            raise InputError(f"linha {n}: use 'alertname|deployment|label=valor,...' (recebido: {line!r})")

        alertname, deployment = parts[0], parts[1]
        if not ALERT_RE.match(alertname):
            raise InputError(f"linha {n}: alertname inválido: {alertname!r}")
        if len(deployment) > 63 or not DEPLOY_RE.match(deployment):
            raise InputError(f"linha {n}: nome de deployment inválido: {deployment!r}")

        match = {}
        if len(parts) == 3 and parts[2]:
            for pair in parts[2].split(","):
                if "=" not in pair:
                    raise InputError(f"linha {n}: label sem '=': {pair!r}")
                k, v = (s.strip() for s in pair.split("=", 1))
                if not LABEL_KEY_RE.match(k):
                    raise InputError(f"linha {n}: nome de label inválido: {k!r}")
                if not LABEL_VAL_RE.match(v):
                    raise InputError(f"linha {n}: valor de label inválido: {v!r}")
                match[k] = v

        rule = {"alertname": alertname}
        if match:
            rule["match"] = match
        rule["deployment"] = deployment
        rules.append(rule)

    if not rules:
        raise InputError("nenhuma regra informada")
    if len(rules) > MAX_RULES:
        raise InputError(f"máximo de {MAX_RULES} regras")
    keys = [(r["alertname"], json.dumps(r.get("match", {}), sort_keys=True)) for r in rules]
    if len(set(keys)) != len(keys):
        raise InputError("regras duplicadas (mesmo alertname e labels)")
    return rules


def parse_cooldown(value: str) -> int:
    try:
        n = int(value)
    except (TypeError, ValueError):
        raise InputError(f"cooldown deve ser inteiro (recebido: {value!r})")
    if not COOLDOWN_MIN <= n <= COOLDOWN_MAX:
        raise InputError(f"cooldown deve estar entre {COOLDOWN_MIN} e {COOLDOWN_MAX} segundos")
    return n


def render_rbac(text: str, deployments):
    names = ", ".join(json.dumps(d) for d in deployments)
    new, count = RESOURCE_NAMES_RE.subn(lambda m: f"{m.group(1)}resourceNames: [{names}]", text)
    if count != 1:
        raise InputError(f"rbac.yaml deveria ter exatamente 1 'resourceNames' (achei {count})")
    return new


def main(argv=None):
    ap = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    ap.add_argument("--rules", required=True, help="regras, uma por linha (alertname|deployment|label=valor)")
    ap.add_argument("--cooldown", default="300", help="segundos (30-3600)")
    ap.add_argument("--dir", default="gitops/services/self-healing", help="pasta dos manifests")
    ap.add_argument("--dry-run", action="store_true", help="só mostra o resultado")
    args = ap.parse_args(argv)

    try:
        rules = parse_rules(args.rules)
        cooldown = parse_cooldown(args.cooldown)
        base = Path(args.dir)
        rbac_path, cfg_path = base / "rbac.yaml", base / "config.json"
        deployments = sorted({r["deployment"] for r in rules})
        new_rbac = render_rbac(rbac_path.read_text(encoding="utf-8"), deployments)
    except (InputError, OSError) as exc:
        print(f"ERRO: {exc}", file=sys.stderr)
        return 2

    cfg = {"namespace": "togglemaster", "cooldownSeconds": cooldown, "rules": rules}
    new_cfg = json.dumps(cfg, indent=2, ensure_ascii=False) + "\n"

    if args.dry_run:
        print(f"--- {cfg_path}\n{new_cfg}--- {rbac_path} (resourceNames)\n  {deployments}")
        return 0

    cfg_path.write_text(new_cfg, encoding="utf-8")
    rbac_path.write_text(new_rbac, encoding="utf-8")
    print(f"OK: {len(rules)} regra(s), deployments permitidos no RBAC: {deployments}, cooldown {cooldown}s")
    return 0


if __name__ == "__main__":
    sys.exit(main())
