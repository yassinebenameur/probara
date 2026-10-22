package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strings"
	"time"
)

// apiClient talks to the Probara API as the superadmin, always scoped to the
// Northwind tenant (?tenant_id= on every call).
type apiClient struct {
	base       string
	user, pass string
	hc         *http.Client
	bearer     string
}

func newAPIClient(base, user, pass string) *apiClient {
	jar, _ := cookiejar.New(nil)
	return &apiClient{base: strings.TrimRight(base, "/"), user: user, pass: pass,
		hc: &http.Client{Jar: jar, Timeout: 90 * time.Second}}
}

func (c *apiClient) login(ctx context.Context) error {
	body, _ := json.Marshal(map[string]string{"username": c.user, "password": c.pass})
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, c.base+"/api/v1/auth/login", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.hc.Do(req)
	if err != nil {
		return fmt.Errorf("login: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("login: %s: %s", resp.Status, b)
	}
	return nil
}

// do sends a tenant-scoped request; out may be nil. A 401 triggers one
// re-login (access tokens live 15 minutes).
func (c *apiClient) do(ctx context.Context, method, path string, in, out any) error {
	return c.doRetry(ctx, method, path, in, out, true)
}

func (c *apiClient) doRetry(ctx context.Context, method, path string, in, out any, retry bool) error {
	u := c.base + path
	sep := "?"
	if strings.Contains(path, "?") {
		sep = "&"
	}
	u += sep + "tenant_id=" + url.QueryEscape(TenantID)
	var rdr io.Reader
	if in != nil {
		b, err := json.Marshal(in)
		if err != nil {
			return err
		}
		rdr = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, u, rdr)
	if err != nil {
		return err
	}
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (Macintosh; Intel Mac OS X 14_6) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/18.0 Safari/605.1.15")
	if c.bearer != "" {
		req.Header.Set("Authorization", "Bearer "+c.bearer)
	}
	resp, err := c.hc.Do(req)
	if err != nil {
		return fmt.Errorf("%s %s: %w", method, path, err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	if resp.StatusCode == http.StatusUnauthorized && retry && c.bearer == "" {
		if err := c.login(ctx); err != nil {
			return err
		}
		return c.doRetry(ctx, method, path, in, out, false)
	}
	if resp.StatusCode >= 300 {
		return fmt.Errorf("%s %s: %s: %s", method, path, resp.Status, strings.TrimSpace(string(b)))
	}
	if out != nil && len(b) > 0 {
		if err := json.Unmarshal(b, out); err != nil {
			return fmt.Errorf("%s %s: decode: %w (%s)", method, path, err, truncate(string(b), 200))
		}
	}
	return nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

type idResp struct {
	ID        string  `json:"id"`
	PushToken *string `json:"push_token"`
	AgentID   *string `json:"agent_id"`
	Key       string  `json:"key"`
}
