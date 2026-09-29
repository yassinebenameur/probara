package discovery

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	contract "github.com/yassinebenameur/probara/shared/discovery"
)

func TestCoverageSeparatesHealthFromCoverage(t *testing.T) {
	c := Coverage{Resources: []Resource{{Name: "api"}}, Monitors: []Monitor{{Type: "http", Enabled: true, State: "down", Fresh: true, Routed: true}}}
	require.Empty(t, coverageGaps(c, false), "a detected outage is not missing coverage")
	c.Monitors[0].State = "up"
	c.Monitors[0].Routed = false
	require.Equal(t, []string{"unrouted"}, coverageGaps(c, false))
	c.Monitors[0].Enabled = false
	require.Equal(t, []string{"no_enabled_monitor"}, coverageGaps(c, false))
	c.Monitors[0].Enabled = true
	c.Monitors[0].Type = "group"
	c.Monitors[0].Routed = true
	require.Equal(t, []string{"freshness_unavailable"}, coverageGaps(c, false))
}
func TestCoverageMissingGraceAndStaleness(t *testing.T) {
	recent := time.Now().Add(-time.Minute)
	old := time.Now().Add(-time.Hour)
	c := Coverage{Resources: []Resource{{MissingSince: &recent}}, Monitors: []Monitor{{Type: "http", Enabled: true, Fresh: true, Routed: true}}}
	require.Equal(t, []string{"resource_missing_grace"}, coverageGaps(c, false))
	c.Resources[0].MissingSince = &old
	require.Equal(t, []string{"inventory_stale", "resource_missing"}, coverageGaps(c, true))
	c.Resources = append(c.Resources, Resource{})
	require.Empty(t, coverageGaps(c, false), "one current resource keeps a grouped service present")
}

func TestTargetFormsCoverEquivalentSpellings(t *testing.T) {
	forms := targetForms(contract.Suggestion{Type: "http", Target: "https://Pay.Example.com/"})
	require.ElementsMatch(t, []string{"https://pay.example.com/", "https://pay.example.com", "https://pay.example.com:443/", "https://pay.example.com:443"}, forms)
	forms = targetForms(contract.Suggestion{Type: "http", Target: "http://web.prod.svc:8080/readyz"})
	require.ElementsMatch(t, []string{"http://web.prod.svc:8080/readyz", "http://web.prod.svc.cluster.local:8080/readyz"}, forms)
	require.ElementsMatch(t, []string{"redis.prod.svc", "redis.prod.svc.cluster.local"}, targetForms(contract.Suggestion{Type: "tcp", Target: "redis.prod.svc", Port: 6379}))
}
