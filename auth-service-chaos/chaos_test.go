package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func okHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
}

func do(h http.Handler, method, path, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func resetChaos(t *testing.T) {
	t.Helper()
	chaos.disable()
	chaos.now = time.Now
	t.Cleanup(func() { chaos.disable(); chaos.now = time.Now })
}

func TestChaosDisabledByEnvIs404(t *testing.T) {
	resetChaos(t)
	t.Setenv("CHAOS_ENABLED", "false")

	if rec := do(http.HandlerFunc(chaosAdminHandler), http.MethodPost, "/admin/chaos", `{"enabled":true}`); rec.Code != http.StatusNotFound {
		t.Fatalf("esperado 404, veio %d", rec.Code)
	}
	if on, _, _ := chaos.current(); on {
		t.Fatal("chaos não deveria ligar com CHAOS_ENABLED!=true")
	}
}

func TestChaosErrorsModeBreaksValidateOnly(t *testing.T) {
	resetChaos(t)
	t.Setenv("CHAOS_ENABLED", "true")
	admin := http.HandlerFunc(chaosAdminHandler)
	h := chaosMiddleware(okHandler())

	if rec := do(admin, http.MethodPost, "/admin/chaos", `{"enabled":true}`); rec.Code != http.StatusOK {
		t.Fatalf("liga: esperado 200, veio %d", rec.Code)
	}

	if rec := do(h, http.MethodPost, "/validate", ""); rec.Code != http.StatusInternalServerError {
		t.Fatalf("/validate: esperado 500, veio %d", rec.Code)
	}
	if rec := do(h, http.MethodGet, "/health", ""); rec.Code != http.StatusOK {
		t.Fatalf("/health em modo errors deve continuar 200, veio %d", rec.Code)
	}
	if rec := do(h, http.MethodGet, "/live", ""); rec.Code != http.StatusOK {
		t.Fatalf("/live nunca é afetado, veio %d", rec.Code)
	}
}

func TestChaosUnhealthyModeBreaksHealthButNotLive(t *testing.T) {
	resetChaos(t)
	t.Setenv("CHAOS_ENABLED", "true")
	admin := http.HandlerFunc(chaosAdminHandler)
	h := chaosMiddleware(okHandler())

	do(admin, http.MethodPost, "/admin/chaos", `{"enabled":true,"mode":"unhealthy"}`)

	if rec := do(h, http.MethodGet, "/health", ""); rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("/health: esperado 503, veio %d", rec.Code)
	}
	if rec := do(h, http.MethodGet, "/live", ""); rec.Code != http.StatusOK {
		t.Fatalf("/live nunca é afetado, veio %d", rec.Code)
	}
}

func TestChaosTTLExpires(t *testing.T) {
	resetChaos(t)
	t.Setenv("CHAOS_ENABLED", "true")

	now := time.Now()
	chaos.now = func() time.Time { return now }

	do(http.HandlerFunc(chaosAdminHandler), http.MethodPost, "/admin/chaos", `{"enabled":true,"ttl_seconds":60}`)
	h := chaosMiddleware(okHandler())

	if rec := do(h, http.MethodPost, "/validate", ""); rec.Code != http.StatusInternalServerError {
		t.Fatalf("dentro do TTL: esperado 500, veio %d", rec.Code)
	}

	now = now.Add(61 * time.Second)
	if rec := do(h, http.MethodPost, "/validate", ""); rec.Code != http.StatusOK {
		t.Fatalf("após o TTL a falha deve sumir, veio %d", rec.Code)
	}
}

func TestChaosDisableAndValidation(t *testing.T) {
	resetChaos(t)
	t.Setenv("CHAOS_ENABLED", "true")
	admin := http.HandlerFunc(chaosAdminHandler)

	do(admin, http.MethodPost, "/admin/chaos", `{"enabled":true}`)
	do(admin, http.MethodDelete, "/admin/chaos", "")
	if on, _, _ := chaos.current(); on {
		t.Fatal("DELETE deveria desligar")
	}

	if rec := do(admin, http.MethodPost, "/admin/chaos", `{"enabled":true,"mode":"xyz"}`); rec.Code != http.StatusBadRequest {
		t.Fatalf("mode inválido: esperado 400, veio %d", rec.Code)
	}
	if rec := do(admin, http.MethodPost, "/admin/chaos", `{"enabled":true,"ttl_seconds":99999}`); rec.Code != http.StatusBadRequest {
		t.Fatalf("ttl inválido: esperado 400, veio %d", rec.Code)
	}
}
