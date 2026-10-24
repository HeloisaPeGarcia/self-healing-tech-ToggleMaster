package main

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

type fakeRestarter struct {
	calls []string
	err   error
}

func (f *fakeRestarter) Restart(_ context.Context, ns, dep string) error {
	f.calls = append(f.calls, ns+"/"+dep)
	return f.err
}

func newTestServer(r Restarter) *Server {
	return &Server{
		cfg: Config{
			Namespace:       "togglemaster",
			CooldownSeconds: 300,
			Rules: []Rule{
				{AlertName: "AuthHigh5xxRate", Deployment: "auth-service"},
				{AlertName: "ToggleMasterHTTPHealthDown", Match: map[string]string{"service": "auth-service"}, Deployment: "auth-service"},
			},
		},
		token:     "s3cret",
		restarter: r,
		cooldown:  newCooldown(5 * time.Minute),
		log:       slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
}

func post(t *testing.T, s *Server, token, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/webhook", strings.NewReader(body))
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rec := httptest.NewRecorder()
	s.routes().ServeHTTP(rec, req)
	return rec
}

const firingAuth = `{"status":"firing","alerts":[{"status":"firing","labels":{"alertname":"AuthHigh5xxRate"},"annotations":{"summary":"5xx alto"},"fingerprint":"abc"}]}`

func TestRejectsMissingOrWrongToken(t *testing.T) {
	f := &fakeRestarter{}
	s := newTestServer(f)

	if rec := post(t, s, "", firingAuth); rec.Code != http.StatusUnauthorized {
		t.Fatalf("sem token: esperado 401, veio %d", rec.Code)
	}
	if rec := post(t, s, "errado", firingAuth); rec.Code != http.StatusUnauthorized {
		t.Fatalf("token errado: esperado 401, veio %d", rec.Code)
	}
	if len(f.calls) != 0 {
		t.Fatalf("nenhum restart esperado, veio %v", f.calls)
	}
}

func TestFiringAlertRestartsDeployment(t *testing.T) {
	f := &fakeRestarter{}
	s := newTestServer(f)

	rec := post(t, s, "s3cret", firingAuth)
	if rec.Code != http.StatusOK {
		t.Fatalf("esperado 200, veio %d", rec.Code)
	}
	if len(f.calls) != 1 || f.calls[0] != "togglemaster/auth-service" {
		t.Fatalf("restart esperado em togglemaster/auth-service, veio %v", f.calls)
	}
}

func TestResolvedAlertIsIgnored(t *testing.T) {
	f := &fakeRestarter{}
	s := newTestServer(f)

	body := `{"status":"resolved","alerts":[{"status":"resolved","labels":{"alertname":"AuthHigh5xxRate"}}]}`
	if rec := post(t, s, "s3cret", body); rec.Code != http.StatusOK {
		t.Fatalf("esperado 200, veio %d", rec.Code)
	}
	if len(f.calls) != 0 {
		t.Fatalf("resolved não deve reiniciar, veio %v", f.calls)
	}
}

func TestAlertOutsideAllowlistIsIgnored(t *testing.T) {
	f := &fakeRestarter{}
	s := newTestServer(f)

	body := `{"status":"firing","alerts":[{"status":"firing","labels":{"alertname":"OutroAlerta"}}]}`
	post(t, s, "s3cret", body)
	if len(f.calls) != 0 {
		t.Fatalf("alerta fora da allowlist não deve reiniciar, veio %v", f.calls)
	}
}

func TestRuleWithLabelMatch(t *testing.T) {
	f := &fakeRestarter{}
	s := newTestServer(f)

	other := `{"status":"firing","alerts":[{"status":"firing","labels":{"alertname":"ToggleMasterHTTPHealthDown","service":"flag-service"}}]}`
	post(t, s, "s3cret", other)
	if len(f.calls) != 0 {
		t.Fatalf("label service=flag-service não casa com a regra, veio %v", f.calls)
	}

	match := `{"status":"firing","alerts":[{"status":"firing","labels":{"alertname":"ToggleMasterHTTPHealthDown","service":"auth-service"}}]}`
	post(t, s, "s3cret", match)
	if len(f.calls) != 1 {
		t.Fatalf("service=auth-service deve reiniciar, veio %v", f.calls)
	}
}

func TestCooldownBlocksSecondRestart(t *testing.T) {
	f := &fakeRestarter{}
	s := newTestServer(f)

	post(t, s, "s3cret", firingAuth)
	rec := post(t, s, "s3cret", firingAuth)

	if rec.Code != http.StatusOK {
		t.Fatalf("cooldown responde 200, veio %d", rec.Code)
	}
	if len(f.calls) != 1 {
		t.Fatalf("segundo restart deveria ser bloqueado, chamadas: %v", f.calls)
	}
	if !strings.Contains(rec.Body.String(), "cooldown") {
		t.Fatalf("resposta deveria citar cooldown: %s", rec.Body.String())
	}
}

func TestCooldownExpires(t *testing.T) {
	f := &fakeRestarter{}
	s := newTestServer(f)

	now := time.Now()
	s.cooldown.now = func() time.Time { return now }

	post(t, s, "s3cret", firingAuth)
	now = now.Add(6 * time.Minute)
	post(t, s, "s3cret", firingAuth)

	if len(f.calls) != 2 {
		t.Fatalf("após a janela deveria reiniciar de novo, chamadas: %v", f.calls)
	}
}

func TestFailedRestartReleasesCooldownAndReturns500(t *testing.T) {
	f := &fakeRestarter{err: errors.New("forbidden")}
	s := newTestServer(f)

	rec := post(t, s, "s3cret", firingAuth)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("falha deve responder 500 (Grafana tenta de novo), veio %d", rec.Code)
	}

	f.err = nil
	rec = post(t, s, "s3cret", firingAuth)
	if rec.Code != http.StatusOK || len(f.calls) != 2 {
		t.Fatalf("nova tentativa após falha deve passar (cooldown liberado). code=%d calls=%v", rec.Code, f.calls)
	}
}

func TestInvalidJSON(t *testing.T) {
	s := newTestServer(&fakeRestarter{})
	if rec := post(t, s, "s3cret", "{nao é json"); rec.Code != http.StatusBadRequest {
		t.Fatalf("esperado 400, veio %d", rec.Code)
	}
}

func TestMethodNotAllowed(t *testing.T) {
	s := newTestServer(&fakeRestarter{})
	req := httptest.NewRequest(http.MethodGet, "/webhook", nil)
	rec := httptest.NewRecorder()
	s.routes().ServeHTTP(rec, req)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("esperado 405, veio %d", rec.Code)
	}
}
