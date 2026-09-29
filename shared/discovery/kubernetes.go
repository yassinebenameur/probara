package discovery

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

// Kubernetes uses a caller-supplied TLS client and reloads the projected token
// per request. A failed/expired paginated list aborts the ENTIRE snapshot.
type Kubernetes struct {
	BaseURL string
	Client  *http.Client
	Token   func() (string, error)
}
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

func (k Kubernetes) Collect(ctx context.Context, cfg Config) (Snapshot, error) {
	out := Snapshot{Revision: cfg.Revision, Namespaces: cfg.Namespaces, Resources: []Resource{}}
	for _, ns := range cfg.Namespaces {
		if !ValidNamespace(ns) {
			return out, fmt.Errorf("invalid namespace")
		}
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
					return out, err
				}
				endpoint := k.BaseURL + path + "?limit=200"
				if continuation != "" {
					endpoint += "&continue=" + url.QueryEscape(continuation)
				}
				req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
				if err != nil {
					return out, err
				}
				req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(token))
				resp, err := k.Client.Do(req)
				if err != nil {
					return out, fmt.Errorf("Kubernetes request failed: %w", err)
				}
				var page struct {
					Metadata struct {
						Continue string `json:"continue"`
					} `json:"metadata"`
					Items []kubeObject `json:"items"`
				}
				if resp.StatusCode != http.StatusOK {
					resp.Body.Close()
					return out, fmt.Errorf("list %s in %s returned HTTP %d", kind, ns, resp.StatusCode)
				}
				data, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20+1))
				resp.Body.Close()
				if err != nil || len(data) > 4<<20 {
					return out, fmt.Errorf("Kubernetes page exceeds limit or could not be read")
				}
				if err = json.Unmarshal(data, &page); err != nil || page.Items == nil {
					return out, fmt.Errorf("invalid Kubernetes list response")
				}
				for _, obj := range page.Items {
					if obj.Metadata.Namespace != ns {
						return out, fmt.Errorf("Kubernetes namespace mismatch")
					}
					if obj.Metadata.Annotations["probara.io/discover"] == "false" {
						continue
					}
					out.Resources = append(out.Resources, resourceFromObject(kind, obj))
					if len(out.Resources) > MaxResources {
						return out, fmt.Errorf("inventory exceeds %d resources", MaxResources)
					}
				}
				continuation = page.Metadata.Continue
				if continuation == "" {
					break
				}
				if seen[continuation] {
					return out, fmt.Errorf("repeated pagination token")
				}
				seen[continuation] = true
			}
		}
	}
	return out, out.Validate(cfg.Namespaces)
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
