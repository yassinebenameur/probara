// Package discovery defines the bounded inventory contract. Kubernetes credentials
// never leave the collector; snapshots contain only selected metadata and targets.
package discovery

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net"
	"net/url"
	"regexp"
	"slices"
	"strings"
)

const MaxResources = 2000

var DNSLabel = regexp.MustCompile(`^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`)

func ValidNamespace(s string) bool { return len(s) > 0 && len(s) <= 63 && DNSLabel.MatchString(s) }

type Config struct {
	Namespaces []string `json:"namespaces"`
	Revision   int64    `json:"revision"`
}
type Snapshot struct {
	Revision   int64      `json:"revision"`
	Namespaces []string   `json:"namespaces"`
	Resources  []Resource `json:"resources"`
}
type Resource struct {
	UID         string       `json:"uid"`
	Kind        string       `json:"kind"`
	Name        string       `json:"name"`
	Namespace   string       `json:"namespace"`
	Service     string       `json:"service,omitempty"`
	Suggestions []Suggestion `json:"suggestions"`
}
type Suggestion struct {
	Key      string `json:"key"`
	Type     string `json:"type"`
	Target   string `json:"target"`
	Port     int    `json:"port,omitempty"`
	Internal bool   `json:"internal"`
	Reason   string `json:"reason"`
}

func (s Suggestion) Identity() string {
	sum := sha256.Sum256([]byte(fmt.Sprintf("%s|%s|%d|%t", s.Type, s.Target, s.Port, s.Internal)))
	return hex.EncodeToString(sum[:])
}
func (r Resource) ServiceKey() string {
	if r.Service != "" {
		return r.Namespace + "/service/" + r.Service
	}
	return r.Namespace + "/" + r.Kind + "/" + r.Name
}
func (s *Snapshot) Validate(namespaces []string) error {
	expected, actual := slices.Clone(namespaces), slices.Clone(s.Namespaces)
	slices.Sort(expected)
	slices.Sort(actual)
	if !slices.Equal(expected, actual) {
		return fmt.Errorf("snapshot must cover exactly the configured namespaces")
	}
	if s.Resources == nil || len(s.Resources) > MaxResources {
		return fmt.Errorf("resources must be an array of at most %d entries", MaxResources)
	}
	seen := map[string]bool{}
	for i := range s.Resources {
		r := &s.Resources[i]
		if r.UID == "" || len(r.UID) > 128 || seen[r.UID] {
			return fmt.Errorf("invalid or duplicate resource UID")
		}
		seen[r.UID] = true
		if !slices.Contains(namespaces, r.Namespace) || r.Name == "" || len(r.Name) > 253 || len(r.Service) > 100 {
			return fmt.Errorf("invalid resource metadata")
		}
		if r.Kind != "Service" && r.Kind != "Ingress" {
			return fmt.Errorf("unsupported resource kind")
		}
		if r.Suggestions == nil {
			r.Suggestions = []Suggestion{}
		}
		if len(r.Suggestions) > 32 {
			return fmt.Errorf("too many suggestions")
		}
		targets := map[string]bool{}
		for j := range r.Suggestions {
			p := &r.Suggestions[j]
			if len(p.Target) > 2048 || len(p.Reason) > 300 {
				return fmt.Errorf("suggestion too long")
			}
			host := ""
			switch p.Type {
			case "http":
				u, err := url.Parse(p.Target)
				if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
					return fmt.Errorf("invalid HTTP target")
				}
				if r.Kind == "Ingress" && (u.Port() != "" || u.Path != "/") {
					return fmt.Errorf("invalid Ingress target")
				}
				host = u.Hostname()
			case "tcp":
				if r.Kind != "Service" || !validHost(p.Target) || p.Port < 1 || p.Port > 65535 {
					return fmt.Errorf("invalid TCP target")
				}
				host = p.Target
			default:
				return fmt.Errorf("unsupported suggestion type")
			}
			// Snapshot data is untrusted: reachability is derived here, never
			// taken from the collector, because Internal decides whether an
			// approval may run from the shared worker fleet. A Service target
			// must be that Service's own cluster DNS name.
			if r.Kind == "Service" && host != r.Name+"."+r.Namespace+".svc" {
				return fmt.Errorf("service suggestion must target its own cluster address")
			}
			p.Internal = r.Kind == "Service" || InternalHost(host)
			p.Key = p.Identity()
			if targets[p.Key] {
				return fmt.Errorf("duplicate suggestion")
			}
			targets[p.Key] = true
		}
	}
	return nil
}

// InternalHost reports whether a hostname only resolves inside a cluster or
// private network, so a check of it needs a private location.
func InternalHost(h string) bool {
	h = strings.TrimSuffix(strings.ToLower(h), ".")
	if net.ParseIP(h) != nil || !strings.Contains(h, ".") {
		return true
	}
	for _, suffix := range []string{".svc", ".cluster.local", ".local", ".internal"} {
		if strings.HasSuffix(h, suffix) {
			return true
		}
	}
	return false
}
func validHost(h string) bool {
	if len(h) == 0 || len(h) > 253 {
		return false
	}
	for _, s := range strings.Split(h, ".") {
		if !ValidNamespace(s) {
			return false
		}
	}
	return true
}
