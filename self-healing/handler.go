package main

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"
)

// Server recebe webhooks de alerta (formato Grafana/Alertmanager) e aplica
// a remediação permitida pela allowlist, respeitando autenticação e cooldown.
type Server struct {
	cfg       Config
	token     string
	restarter Restarter
	cooldown  *Cooldown
	log       *slog.Logger
	dryRun    bool
}

// Subconjunto do payload de webhook do Grafana (compatível com Alertmanager).
type webhookPayload struct {
	Status string  `json:"status"`
	Alerts []alert `json:"alerts"`
}

type alert struct {
	Status      string            `json:"status"`
	Labels      map[string]string `json:"labels"`
	Annotations map[string]string `json:"annotations"`
	Fingerprint string            `json:"fingerprint"`
}

type result struct {
	AlertName  string `json:"alertname"`
	Deployment string `json:"deployment,omitempty"`
	Action     string `json:"action"` // restarted | skipped | ignored | failed
	Reason     string `json:"reason,omitempty"`
}

func (s *Server) routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	})
	mux.HandleFunc("/webhook", s.webhook)
	return mux
}

func (s *Server) authorized(r *http.Request) bool {
	header := r.Header.Get("Authorization")
	got := strings.TrimSpace(strings.TrimPrefix(header, "Bearer "))
	if got == "" || got == header { // sem prefixo "Bearer " ou vazio
		return false
	}
	// Compara hashes para tempo constante e sem vazar o tamanho do token.
	a := sha256.Sum256([]byte(got))
	b := sha256.Sum256([]byte(s.token))
	return subtle.ConstantTimeCompare(a[:], b[:]) == 1
}

func (s *Server) webhook(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "método não permitido", http.StatusMethodNotAllowed)
		return
	}

	if !s.authorized(r) {
		s.log.Warn("webhook_unauthorized", "remote_addr", r.RemoteAddr)
		http.Error(w, "não autorizado", http.StatusUnauthorized)
		return
	}

	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 1<<20))
	if err != nil {
		http.Error(w, "corpo inválido", http.StatusBadRequest)
		return
	}
	var payload webhookPayload
	if err := json.Unmarshal(body, &payload); err != nil {
		s.log.Warn("webhook_invalid_payload", "error", err.Error())
		http.Error(w, "JSON inválido", http.StatusBadRequest)
		return
	}

	s.log.Info("alert_received", "status", payload.Status, "alerts", len(payload.Alerts))

	results := make([]result, 0, len(payload.Alerts))
	failed := false

	for _, a := range payload.Alerts {
		res := s.handleAlert(r.Context(), payload.Status, a)
		if res.Action == "failed" {
			failed = true
		}
		results = append(results, res)
	}

	w.Header().Set("Content-Type", "application/json")
	if failed {
		// 5xx faz o Grafana tentar de novo; o cooldown foi liberado na falha.
		w.WriteHeader(http.StatusInternalServerError)
	}
	_ = json.NewEncoder(w).Encode(map[string]any{"results": results})
}

func (s *Server) handleAlert(ctx context.Context, payloadStatus string, a alert) result {
	name := a.Labels["alertname"]
	res := result{AlertName: name}

	status := a.Status
	if status == "" {
		status = payloadStatus
	}
	if status != "firing" {
		res.Action, res.Reason = "ignored", "status_"+status
		s.log.Info("alert_ignored", "alertname", name, "reason", res.Reason)
		return res
	}

	rule, ok := s.cfg.findRule(name, a.Labels)
	if !ok {
		res.Action, res.Reason = "ignored", "not_in_allowlist"
		s.log.Info("alert_ignored", "alertname", name, "reason", res.Reason)
		return res
	}
	res.Deployment = rule.Deployment

	if ok, remaining := s.cooldown.TryAcquire(rule.Deployment); !ok {
		res.Action, res.Reason = "skipped", "cooldown"
		s.log.Info("action_skipped",
			"alertname", name,
			"deployment", rule.Deployment,
			"reason", "cooldown",
			"cooldown_remaining_seconds", int(remaining.Seconds()))
		return res
	}

	callCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	started := time.Now()
	if err := s.restarter.Restart(callCtx, s.cfg.Namespace, rule.Deployment); err != nil {
		s.cooldown.Release(rule.Deployment)
		res.Action, res.Reason = "failed", "restart_error"
		s.log.Error("rollout_restart_failed",
			"alertname", name,
			"deployment", rule.Deployment,
			"namespace", s.cfg.Namespace,
			"error", err.Error())
		return res
	}

	res.Action = "restarted"
	s.log.Info("rollout_restart_triggered",
		"alertname", name,
		"deployment", rule.Deployment,
		"namespace", s.cfg.Namespace,
		"fingerprint", a.Fingerprint,
		"summary", a.Annotations["summary"],
		"dry_run", s.dryRun,
		"duration_ms", time.Since(started).Milliseconds())
	return res
}
