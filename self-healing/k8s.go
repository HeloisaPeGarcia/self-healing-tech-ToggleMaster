package main

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"time"
)

const (
	tokenFile = "/var/run/secrets/kubernetes.io/serviceaccount/token"
	caFile    = "/var/run/secrets/kubernetes.io/serviceaccount/ca.crt"

	restartAnnotation = "kubectl.kubernetes.io/restartedAt"
)

// Restarter reinicia um Deployment. É uma interface para permitir testes e dry-run.
type Restarter interface {
	Restart(ctx context.Context, namespace, deployment string) error
}

// kubeRestarter fala direto com a API do Kubernetes usando a ServiceAccount do
// pod. Faz o mesmo que `kubectl rollout restart`: atualiza a annotation
// restartedAt no template do pod, o que dispara um rolling update normal
// (respeitando maxUnavailable/PDB do Deployment).
type kubeRestarter struct {
	client  *http.Client
	baseURL string
}

func newInClusterRestarter() (*kubeRestarter, error) {
	host := os.Getenv("KUBERNETES_SERVICE_HOST")
	port := os.Getenv("KUBERNETES_SERVICE_PORT")
	if host == "" || port == "" {
		return nil, errors.New("fora do cluster: KUBERNETES_SERVICE_HOST/PORT ausentes")
	}

	ca, err := os.ReadFile(caFile)
	if err != nil {
		return nil, fmt.Errorf("ler CA do cluster: %w", err)
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(ca) {
		return nil, errors.New("CA do cluster inválida")
	}

	return &kubeRestarter{
		client: &http.Client{
			Timeout:   10 * time.Second,
			Transport: &http.Transport{TLSClientConfig: &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12}},
		},
		baseURL: "https://" + host + ":" + port,
	}, nil
}

func (k *kubeRestarter) Restart(ctx context.Context, namespace, deployment string) error {
	// O token é relido a cada chamada: o kubelet o rotaciona.
	token, err := os.ReadFile(tokenFile)
	if err != nil {
		return fmt.Errorf("ler token da ServiceAccount: %w", err)
	}

	patch, err := json.Marshal(map[string]any{
		"spec": map[string]any{
			"template": map[string]any{
				"metadata": map[string]any{
					"annotations": map[string]string{
						restartAnnotation: time.Now().UTC().Format(time.RFC3339),
					},
				},
			},
		},
	})
	if err != nil {
		return err
	}

	url := fmt.Sprintf("%s/apis/apps/v1/namespaces/%s/deployments/%s", k.baseURL, namespace, deployment)
	req, err := http.NewRequestWithContext(ctx, http.MethodPatch, url, bytes.NewReader(patch))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(string(token)))
	req.Header.Set("Content-Type", "application/strategic-merge-patch+json")
	req.Header.Set("Accept", "application/json")

	resp, err := k.client.Do(req)
	if err != nil {
		return fmt.Errorf("chamar API do Kubernetes: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("API do Kubernetes respondeu %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	return nil
}

// dryRunRestarter só registra o que faria. Útil para ensaiar a integração com o
// Grafana sem derrubar nada (DRY_RUN=true).
type dryRunRestarter struct{ log *slog.Logger }

func (d dryRunRestarter) Restart(_ context.Context, namespace, deployment string) error {
	d.log.Info("dry_run_restart_skipped", "namespace", namespace, "deployment", deployment)
	return nil
}
