package discovery

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"
)

// Kubernetes uses a caller-supplied TLS client and reloads the projected token
// per request. A failed/expired paginated list in an ACTIVE namespace aborts
// the entire snapshot; a pending namespace is all-or-nothing and only skipped.
type Kubernetes struct {
	BaseURL string
	Client  *http.Client
	Token   func() (string, error)
	// Reserve is the part of the cycle's deadline that pending namespaces may
	// not use, kept for the upload; zero means PendingReserve.
	Reserve time.Duration
}

// PendingReserve is how much of the cycle's time budget is kept for the
// upload: pending namespaces stop being scanned once only this much remains,
// and the rest are skipped with SkipDeadline.
const PendingReserve = 30 * time.Second

type kubeObject struct {
	Metadata struct {
		UID         string            `json:"uid"`
		Name        string            `json:"name"`
		Namespace   string            `json:"namespace"`
		Annotations map[string]string `json:"annotations"`
	} `json:"metadata"`
	Spec struct {
		Type  string `json:"type"`
		Ports []struct {
			Port     int    `json:"port"`
			Protocol string `json:"protocol"`
		} `json:"ports"`
		Rules []struct {
			Host string `json:"host"`
		} `json:"rules"`
		TLS []struct {
			Hosts []string `json:"hosts"`
		} `json:"tls"`
	} `json:"spec"`
}

// scanError is a namespace scan failure with the skip reason it maps to when
// the namespace is pending.
type scanError struct {
	reason string
	err    error
}

func (e *scanError) Error() string { return e.err.Error() }
func (e *scanError) Unwrap() error { return e.err }
func failScan(reason, format string, args ...any) error {
	return &scanError{reason: reason, err: fmt.Errorf(format, args...)}
}

// Collect scans the active namespaces first, exactly as configured: any
// failure there aborts the scan. Then each pending namespace is scanned
// all-or-nothing: on any failure its resources are dropped (truncated back to
// what preceded it) and it is reported in Skipped instead of Namespaces.
func (k Kubernetes) Collect(ctx context.Context, cfg Config) (Snapshot, error) {
	out := Snapshot{Revision: cfg.Revision, Namespaces: slices.Clone(cfg.Namespaces), Resources: []Resource{}}
	if out.Namespaces == nil {
		out.Namespaces = []string{}
	}
	for _, ns := range cfg.Namespaces {
		if !ValidNamespace(ns) {
			return out, fmt.Errorf("invalid namespace")
		}
		if err := k.scanNamespace(ctx, ns, &out.Resources); err != nil {
			return out, err
		}
	}
	// Pending namespaces run against a deadline that keeps Reserve of the
	// cycle for the upload, so a slow new namespace never costs the snapshot.
	pctx, cancel := ctx, context.CancelFunc(func() {})
	if deadline, ok := ctx.Deadline(); ok {
		reserve := k.Reserve
		if reserve <= 0 {
			reserve = PendingReserve
		}
		pctx, cancel = context.WithDeadline(ctx, deadline.Add(-reserve))
	}
	defer cancel()
	skip := func(ns, reason string) {
		if out.Skipped == nil {
			out.Skipped = map[string]string{}
		}
		out.Skipped[ns] = reason
	}
	for _, ns := range cfg.Pending {
		if slices.Contains(out.Namespaces, ns) || out.Skipped[ns] != "" {
			continue
		}
		if err := ctx.Err(); err != nil {
			return out, err // the whole cycle was cancelled
		}
		if pctx.Err() != nil {
			skip(ns, SkipDeadline)
			continue
		}
		if !ValidNamespace(ns) {
			skip(ns, SkipError)
			continue
		}
		mark := len(out.Resources)
		err := k.scanNamespace(pctx, ns, &out.Resources)
		if err == nil {
			out.Namespaces = append(out.Namespaces, ns)
			continue
		}
		out.Resources = out.Resources[:mark]
		if ctx.Err() != nil {
			return out, ctx.Err()
		}
		reason := SkipError
		var se *scanError
		if pctx.Err() != nil {
			reason = SkipDeadline
		} else if errors.As(err, &se) {
			reason = se.reason
		}
		skip(ns, reason)
	}
	return out, out.Validate(cfg.Namespaces, cfg.Pending)
}

// scanNamespace lists the Services and Ingresses of one namespace, appending
// them to resources. On error, what it appended is left in place; the caller
// aborts (active) or truncates (pending).
func (k Kubernetes) scanNamespace(ctx context.Context, ns string, resources *[]Resource) error {
	for _, kind := range []string{"Service", "Ingress"} {
		path := "/api/v1/namespaces/" + ns + "/services"
		if kind == "Ingress" {
			path = "/apis/networking.k8s.io/v1/namespaces/" + ns + "/ingresses"
		}
		continuation := ""
		seen := map[string]bool{}
		for {
			token, err := k.Token()
			if err != nil {
				return err
			}
			endpoint := k.BaseURL + path + "?limit=200"
			if continuation != "" {
				endpoint += "&continue=" + url.QueryEscape(continuation)
			}
			req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
			if err != nil {
				return err
			}
			req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(token))
			resp, err := k.Client.Do(req)
			if err != nil {
				return fmt.Errorf("Kubernetes request failed: %w", err)
			}
			var page struct {
				Metadata struct {
					Continue string `json:"continue"`
				} `json:"metadata"`
				Items []kubeObject `json:"items"`
			}
			if resp.StatusCode != http.StatusOK {
				resp.Body.Close()
				reason := SkipError
				if resp.StatusCode == http.StatusForbidden || resp.StatusCode == http.StatusUnauthorized {
					reason = SkipForbidden
				}
				return failScan(reason, "list %s in %s returned HTTP %d", kind, ns, resp.StatusCode)
			}
			data, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20+1))
			resp.Body.Close()
			if err != nil {
				return fmt.Errorf("Kubernetes page could not be read: %w", err)
			}
			if len(data) > 4<<20 {
				return failScan(SkipTooLarge, "Kubernetes page exceeds limit")
			}
			if err = json.Unmarshal(data, &page); err != nil || page.Items == nil {
				return fmt.Errorf("invalid Kubernetes list response")
			}
			for _, obj := range page.Items {
				if obj.Metadata.Namespace != ns {
					return fmt.Errorf("Kubernetes namespace mismatch")
				}
				if obj.Metadata.Annotations["probara.io/discover"] == "false" {
					continue
				}
				*resources = append(*resources, resourceFromObject(kind, obj))
				if len(*resources) > MaxResources {
					return failScan(SkipTooLarge, "inventory exceeds %d resources", MaxResources)
				}
			}
			continuation = page.Metadata.Continue
			if continuation == "" {
				break
			}
			if seen[continuation] {
				return fmt.Errorf("repeated pagination token")
			}
			seen[continuation] = true
		}
	}
	return nil
}
func resourceFromObject(kind string, o kubeObject) Resource {
	r := Resource{UID: o.Metadata.UID, Kind: kind, Name: o.Metadata.Name, Namespace: o.Metadata.Namespace, Service: o.Metadata.Annotations["probara.io/service"], Suggestions: []Suggestion{}}
	// An optional annotation must never poison the complete inventory scan.
	r.Service = strings.TrimSpace(r.Service)
	if len(r.Service) > 100 {
		r.Service = ""
	}
	add := func(s Suggestion) {
		s.Key = s.Identity()
		for _, p := range r.Suggestions {
			if p.Key == s.Key {
				return
			}
		}
		if len(r.Suggestions) < 32 {
			r.Suggestions = append(r.Suggestions, s)
		}
	}
	if kind == "Ingress" {
		var tlsHosts []string
		for _, t := range o.Spec.TLS {
			tlsHosts = append(tlsHosts, t.Hosts...)
		}
		for _, rule := range o.Spec.Rules {
			if !validHost(rule.Host) {
				continue
			} // Wildcards and catch-all rules need operator input.
			scheme := "http"
			if tlsCovers(tlsHosts, rule.Host) {
				scheme = "https"
			}
			add(Suggestion{Type: "http", Target: scheme + "://" + rule.Host + "/", Internal: InternalHost(rule.Host), Reason: "Ingress hostname; GET / with an operator-selected expected status"})
		}
	} else {
		// ExternalName resources need an explicit policy; do not infer internal reachability.
		if o.Spec.Type == "ExternalName" {
			return r
		}
		host := r.Name + "." + r.Namespace + ".svc"
		path := o.Metadata.Annotations["probara.io/health-path"]
		healthPortText := o.Metadata.Annotations["probara.io/health-port"]
		healthPort, healthPortErr := strconv.Atoi(healthPortText)
		validHealthPort := healthPortText == "" || (healthPortErr == nil && healthPort > 0 && healthPort <= 65535)
		scheme := o.Metadata.Annotations["probara.io/health-scheme"]
		if scheme == "" {
			scheme = "http"
		}
		parsedPath, pathErr := url.ParseRequestURI(path)
		validPath := pathErr == nil && parsedPath != nil && !parsedPath.IsAbs() && strings.HasPrefix(path, "/") && !strings.HasPrefix(path, "//") && !strings.ContainsAny(path, "?#\\")
		for _, p := range o.Spec.Ports {
			if p.Protocol != "" && p.Protocol != "TCP" {
				continue
			}
			if validPath && validHealthPort && len(scheme)+len(host)+len(path)+12 <= 2048 && (scheme == "http" || scheme == "https") && (healthPort == p.Port || (healthPort == 0 && len(o.Spec.Ports) == 1)) {
				add(Suggestion{Type: "http", Target: fmt.Sprintf("%s://%s:%d%s", scheme, host, p.Port, path), Internal: true, Reason: "Explicit probara.io/health-path annotation"})
			} else {
				add(Suggestion{Type: "tcp", Target: host, Port: p.Port, Internal: true, Reason: "Declared TCP service port; connectivity only, not authenticated application health"})
			}
		}
	}
	return r
}

// tlsCovers matches a rule host against spec.tls hosts, including a wildcard
// entry, which covers exactly one leftmost label (*.example.com covers
// api.example.com, not example.com or a.b.example.com).
func tlsCovers(tlsHosts []string, host string) bool {
	host = strings.ToLower(host)
	for _, h := range tlsHosts {
		h = strings.ToLower(h)
		if h == host {
			return true
		}
		if suffix, ok := strings.CutPrefix(h, "*"); ok && strings.HasPrefix(suffix, ".") {
			if label, rest, found := strings.Cut(host, "."); found && label != "" && "."+rest == suffix {
				return true
			}
		}
	}
	return false
}
