// discovery-collector runs inside Kubernetes with namespaced read-only RBAC.
package main

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/google/uuid"
	"github.com/yassinebenameur/probara/shared/discovery"
)

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}
func run() error {
	base := strings.TrimRight(os.Getenv("PROBARA_URL"), "/")
	u, err := url.Parse(base)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") {
		return fmt.Errorf("PROBARA_URL must be an HTTPS origin")
	}
	id := os.Getenv("PROBARA_DISCOVERY_ID")
	if _, err := uuid.Parse(id); err != nil {
		return fmt.Errorf("invalid PROBARA_DISCOVERY_ID")
	}
	tokenPath := os.Getenv("PROBARA_DISCOVERY_TOKEN_FILE")
	if tokenPath == "" {
		tokenPath = "/var/run/probara/token"
	}
	const saDir = "/var/run/secrets/kubernetes.io/serviceaccount/"
	ca, err := os.ReadFile(saDir + "ca.crt")
	if err != nil {
		return err
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(ca) {
		return fmt.Errorf("invalid Kubernetes CA")
	}
	noRedirect := func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	kubeClient := &http.Client{Timeout: 30 * time.Second, Transport: &http.Transport{TLSClientConfig: &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12}}, CheckRedirect: noRedirect}
	platformClient := &http.Client{Timeout: 30 * time.Second, CheckRedirect: noRedirect}
	kube := discovery.Kubernetes{BaseURL: "https://" + net.JoinHostPort(os.Getenv("KUBERNETES_SERVICE_HOST"), os.Getenv("KUBERNETES_SERVICE_PORT")), Client: kubeClient, Token: func() (string, error) { b, e := os.ReadFile(saDir + "token"); return string(b), e }}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	endpoint := base + "/api/v1/discovery-collectors/" + id
	for {
		cycleCtx, cancel := context.WithTimeout(ctx, 2*time.Minute)
		err := cycle(cycleCtx, platformClient, kube, endpoint, tokenPath)
		cancel()
		if err != nil {
			log.Printf("Discovery cycle failed: %v", err)
		} else {
			log.Print("Inventory synchronized")
		}
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(5 * time.Minute):
		}
	}
}
func cycle(ctx context.Context, c *http.Client, k discovery.Kubernetes, endpoint, tokenPath string) error {
	token, err := os.ReadFile(tokenPath)
	if err != nil {
		return err
	}
	request := func(method, path string, body []byte) ([]byte, error) {
		req, err := http.NewRequestWithContext(ctx, method, endpoint+path, bytes.NewReader(body))
		if err != nil {
			return nil, err
		}
		req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(string(token)))
		req.Header.Set("Content-Type", "application/json")
		resp, err := c.Do(req)
		if err != nil {
			return nil, err
		}
		defer resp.Body.Close()
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			return nil, fmt.Errorf("platform returned HTTP %d", resp.StatusCode)
		}
		b, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		return b, err
	}
	b, err := request(http.MethodGet, "/config", nil)
	if err != nil {
		return err
	}
	var cfg discovery.Config
	if err = json.Unmarshal(b, &cfg); err != nil {
		return err
	}
	snapshot, err := k.Collect(ctx, cfg)
	if err != nil {
		return err
	}
	b, err = json.Marshal(snapshot)
	if err != nil {
		return err
	}
	if len(b) > 4<<20 {
		return fmt.Errorf("inventory payload exceeds 4 MiB")
	}
	_, err = request(http.MethodPost, "/snapshot", b)
	return err
}
