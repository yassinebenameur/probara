package discovery

import (
	"testing"

	"github.com/stretchr/testify/require"
	contract "github.com/yassinebenameur/probara/shared/discovery"
)

// gapsFor is the production gap logic; presence itself is computed in SQL by
// Overview and covered by the integration tests.
func TestGapsSeparateHealthFromCoverage(t *testing.T) {
	monitors := []Monitor{{Type: "http", Enabled: true, State: "down", Fresh: true, Routed: true}}
	require.Empty(t, gapsFor(true, false, monitors), "a detected outage is not missing coverage")
	monitors[0].State = "up"
	monitors[0].Routed = false
	require.Equal(t, []string{"unrouted"}, gapsFor(true, false, monitors))
	monitors[0].Fresh = false
	require.Equal(t, []string{"stale_checks", "unrouted"}, gapsFor(true, false, monitors))
	monitors[0].Enabled = false
	require.Equal(t, []string{"no_enabled_monitor"}, gapsFor(true, false, monitors), "disabled monitors are not judged")
	monitors[0].Enabled = true
	monitors[0].Type = "group"
	monitors[0].Routed = true
	require.Equal(t, []string{"freshness_unavailable"}, gapsFor(true, false, monitors))
	require.Equal(t, []string{"no_enabled_monitor"}, gapsFor(true, false, nil))
}
func TestGapsMissingGrace(t *testing.T) {
	monitors := []Monitor{{Type: "http", Enabled: true, Fresh: true, Routed: true}}
	require.Equal(t, []string{"resource_missing_grace"}, gapsFor(false, true, monitors))
	require.Equal(t, []string{"resource_missing"}, gapsFor(false, false, monitors))
	require.Empty(t, gapsFor(true, true, monitors), "one current resource keeps a grouped service present")
	require.NotContains(t, gapsFor(false, false, monitors), "inventory_stale", "staleness is integration-level, added by Overview")
}

func TestTargetFormsCoverEquivalentSpellings(t *testing.T) {
	forms := targetForms(contract.Suggestion{Type: "http", Target: "https://Pay.Example.com/"})
	require.ElementsMatch(t, []string{"https://pay.example.com/", "https://pay.example.com", "https://pay.example.com:443/", "https://pay.example.com:443"}, forms)
	forms = targetForms(contract.Suggestion{Type: "http", Target: "http://web.prod.svc:8080/readyz"})
	require.ElementsMatch(t, []string{"http://web.prod.svc:8080/readyz", "http://web.prod.svc.cluster.local:8080/readyz"}, forms)
	require.ElementsMatch(t, []string{"redis.prod.svc", "redis.prod.svc.cluster.local"}, targetForms(contract.Suggestion{Type: "tcp", Target: "redis.prod.svc", Port: 6379}))
}
