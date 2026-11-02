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

// Skip reasons: why a pending namespace was left out of a snapshot. The
// collector reports the first four (Snapshot.Skipped); the API alone assigns
// SkipInventoryLimit, when storing the namespace would exceed the stored
// resource cap.
const (
	SkipForbidden      = "forbidden"       // HTTP 401/403: no Role yet, or no such namespace
	SkipTooLarge       = "too_large"       // a page over 4 MiB, the MaxResources budget, or the 4 MiB upload
	SkipDeadline       = "deadline"        // not scanned within the cycle's time budget
	SkipError          = "error"           // any other failure
	SkipInventoryLimit = "inventory_limit" // assigned by the API, never the collector
)

// Config is what the collector scans. Namespaces are the active ones: every
// snapshot must cover them, and any failure in one aborts the scan, so a lost
// Role can never make their inventory look deleted. Pending namespaces were
// added but have not been scanned successfully yet; each is all-or-nothing
// and a failure only skips it (Snapshot.Skipped).
type Config struct {
	Namespaces []string `json:"namespaces"`
	Pending    []string `json:"pending"`
	Revision   int64    `json:"revision"`
}

// Snapshot is one complete scan. Namespaces lists what was scanned in full:
// every active namespace plus the pending ones that succeeded. Skipped gives
// the reason for each pending namespace that was not.
type Snapshot struct {
	Revision   int64             `json:"revision"`
	Namespaces []string          `json:"namespaces"`
	Skipped    map[string]string `json:"skipped,omitempty"`
	Resources  []Resource        `json:"resources"`
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

// Validate checks a snapshot against the configured active and pending
// namespaces: active ⊆ Namespaces ⊆ active ∪ pending, Skipped ⊆ pending and
// disjoint from Namespaces, and every resource inside Namespaces. A skip
// reason the API does not know (a newer collector) is stored as SkipError.
func (s *Snapshot) Validate(active, pending []string) error {
	scanned := map[string]bool{}
	for _, ns := range s.Namespaces {
		if scanned[ns] || (!slices.Contains(active, ns) && !slices.Contains(pending, ns)) {
			return fmt.Errorf("snapshot must cover only configured namespaces, each once")
		}
		scanned[ns] = true
	}
	for _, ns := range active {
		if !scanned[ns] {
			return fmt.Errorf("snapshot must cover every active namespace")
		}
	}
	for ns, reason := range s.Skipped {
		if !slices.Contains(pending, ns) || scanned[ns] {
			return fmt.Errorf("only pending namespaces that were not scanned can be skipped")
		}
		switch reason {
		case SkipForbidden, SkipTooLarge, SkipDeadline, SkipError:
		default:
			s.Skipped[ns] = SkipError
		}
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
		if !scanned[r.Namespace] || r.Name == "" || len(r.Name) > 253 || len(r.Service) > 100 {
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
