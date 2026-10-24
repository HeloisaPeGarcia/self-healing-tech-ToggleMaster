package main

import (
	"encoding/json"
	"net/http"
	"os"
	"sync"
	"time"
)

// Falha controlada para a "prova real" do self-healing.
//
// Regras de segurança do desenho:
//   - Só existe se CHAOS_ENABLED=true (fora disso o endpoint responde 404).
//   - Exige o MASTER_KEY (montado em main.go com masterKeyAuthMiddleware).
//   - O estado fica SÓ EM MEMÓRIA: reiniciar o pod (rollout restart) remove a
//     falha. É isso que torna a cura demonstrável.
//   - Tem TTL opcional (auto-desliga) para não esquecer a falha ligada.
//   - Nunca mexe em /live: quem reinicia deve ser o self-healing, não o kubelet.
//
// Modos:
//   - "errors"    (padrão): /validate responde 500. O pod continua Ready e recebe
//     tráfego, então o 5xx aparece em logs/traces/métricas (alerta de taxa de 5xx).
//   - "unhealthy": além de /validate, /health responde 503. O pod sai do Service
//     (readiness) e o probe do Blackbox falha (alerta ToggleMasterHTTPHealthDown).

const (
	chaosModeErrors    = "errors"
	chaosModeUnhealthy = "unhealthy"
	chaosMaxTTL        = time.Hour
)

type chaosController struct {
	mu    sync.Mutex
	on    bool
	mode  string
	until time.Time // zero = sem expiração
	now   func() time.Time
}

var chaos = &chaosController{now: time.Now}

func chaosAllowed() bool { return os.Getenv("CHAOS_ENABLED") == "true" }

func (c *chaosController) enable(mode string, ttl time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.on, c.mode = true, mode
	if ttl > 0 {
		c.until = c.now().Add(ttl)
	} else {
		c.until = time.Time{}
	}
}

func (c *chaosController) disable() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.on, c.mode, c.until = false, "", time.Time{}
}

// current devolve o estado vigente, desligando sozinho quando o TTL expira.
func (c *chaosController) current() (on bool, mode string, until time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.on && !c.until.IsZero() && !c.now().Before(c.until) {
		c.on, c.mode, c.until = false, "", time.Time{}
	}
	return c.on, c.mode, c.until
}

type chaosRequest struct {
	Enabled    bool   `json:"enabled"`
	Mode       string `json:"mode"`
	TTLSeconds int    `json:"ttl_seconds"`
}

type chaosStatus struct {
	Enabled bool   `json:"enabled"`
	Mode    string `json:"mode,omitempty"`
	Until   string `json:"until,omitempty"`
}

func writeChaosStatus(w http.ResponseWriter, code int) {
	on, mode, until := chaos.current()
	st := chaosStatus{Enabled: on, Mode: mode}
	if on && !until.IsZero() {
		st.Until = until.UTC().Format(time.RFC3339)
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(st)
}

// chaosAdminHandler: GET = status, POST = liga/desliga, DELETE = desliga.
// Deve ser registrado atrás do masterKeyAuthMiddleware.
func chaosAdminHandler(w http.ResponseWriter, r *http.Request) {
	if !chaosAllowed() {
		http.NotFound(w, r)
		return
	}

	switch r.Method {
	case http.MethodGet:
		writeChaosStatus(w, http.StatusOK)

	case http.MethodDelete:
		chaos.disable()
		writeLog("warn", "chaos_disabled", LogEvent{Component: "chaos", Message: "fault injection disabled"})
		writeChaosStatus(w, http.StatusOK)

	case http.MethodPost:
		var req chaosRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "Corpo inválido", http.StatusBadRequest)
			return
		}

		if !req.Enabled {
			chaos.disable()
			writeLog("warn", "chaos_disabled", LogEvent{Component: "chaos", Message: "fault injection disabled"})
			writeChaosStatus(w, http.StatusOK)
			return
		}

		if req.Mode == "" {
			req.Mode = chaosModeErrors
		}
		if req.Mode != chaosModeErrors && req.Mode != chaosModeUnhealthy {
			http.Error(w, "mode deve ser 'errors' ou 'unhealthy'", http.StatusBadRequest)
			return
		}

		ttl := time.Duration(req.TTLSeconds) * time.Second
		if ttl < 0 || ttl > chaosMaxTTL {
			http.Error(w, "ttl_seconds deve estar entre 0 e 3600", http.StatusBadRequest)
			return
		}

		chaos.enable(req.Mode, ttl)
		writeLog("warn", "chaos_enabled", LogEvent{
			Component: "chaos",
			Message:   "fault injection enabled, mode=" + req.Mode,
		})
		writeChaosStatus(w, http.StatusOK)

	default:
		http.Error(w, "Método não permitido", http.StatusMethodNotAllowed)
	}
}

// chaosMiddleware envolve o mux inteiro. Sem falha ativa, é um repasse direto.
func chaosMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !chaosAllowed() {
			next.ServeHTTP(w, r)
			return
		}

		on, mode, _ := chaos.current()
		if !on {
			next.ServeHTTP(w, r)
			return
		}

		switch {
		case r.URL.Path == "/validate":
			writeLog("error", "chaos_fault_injected", LogEvent{
				Component:  "chaos",
				StatusCode: http.StatusInternalServerError,
				Message:    "injected failure on /validate",
			})
			http.Error(w, "falha injetada (chaos)", http.StatusInternalServerError)
			return

		case r.URL.Path == "/health" && mode == chaosModeUnhealthy:
			writeLog("error", "chaos_fault_injected", LogEvent{
				Component:  "chaos",
				StatusCode: http.StatusServiceUnavailable,
				Message:    "injected failure on /health",
			})
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusServiceUnavailable)
			_ = json.NewEncoder(w).Encode(map[string]string{"status": "degraded", "chaos": "active"})
			return
		}

		next.ServeHTTP(w, r)
	})
}
