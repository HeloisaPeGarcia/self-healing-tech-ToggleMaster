package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
)

// Rule liga um alerta a um Deployment. Só alertas que casam com alguma regra
// são remediados (allowlist). Qualquer outro alerta é ignorado.
type Rule struct {
	AlertName  string            `json:"alertname"`
	Match      map[string]string `json:"match,omitempty"` // labels adicionais exigidas no alerta
	Deployment string            `json:"deployment"`
}

// Config é carregada de um arquivo JSON (ConfigMap), não de variáveis soltas,
// para que a allowlist fique versionada no GitOps.
type Config struct {
	Namespace       string `json:"namespace"`
	CooldownSeconds int    `json:"cooldownSeconds"`
	Rules           []Rule `json:"rules"`
}

const (
	defaultCooldownSeconds = 300
	namespaceFile          = "/var/run/secrets/kubernetes.io/serviceaccount/namespace"
)

func loadConfig(path string) (Config, error) {
	var cfg Config

	raw, err := os.ReadFile(path)
	if err != nil {
		return cfg, fmt.Errorf("ler config %q: %w", path, err)
	}
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return cfg, fmt.Errorf("config inválida: %w", err)
	}

	if cfg.Namespace == "" {
		cfg.Namespace = detectNamespace()
	}
	if cfg.CooldownSeconds <= 0 {
		cfg.CooldownSeconds = defaultCooldownSeconds
	}
	if len(cfg.Rules) == 0 {
		return cfg, errors.New("config sem regras: nenhuma remediação seria permitida")
	}
	for i, r := range cfg.Rules {
		if strings.TrimSpace(r.AlertName) == "" || strings.TrimSpace(r.Deployment) == "" {
			return cfg, fmt.Errorf("regra %d: alertname e deployment são obrigatórios", i)
		}
	}
	return cfg, nil
}

func detectNamespace() string {
	if ns := strings.TrimSpace(os.Getenv("POD_NAMESPACE")); ns != "" {
		return ns
	}
	if raw, err := os.ReadFile(namespaceFile); err == nil {
		if ns := strings.TrimSpace(string(raw)); ns != "" {
			return ns
		}
	}
	return "togglemaster"
}

// findRule devolve a primeira regra cujo alertname e labels casam com o alerta.
func (c Config) findRule(alertName string, labels map[string]string) (Rule, bool) {
	for _, r := range c.Rules {
		if r.AlertName != alertName {
			continue
		}
		matched := true
		for k, v := range r.Match {
			if labels[k] != v {
				matched = false
				break
			}
		}
		if matched {
			return r, true
		}
	}
	return Rule{}, false
}
