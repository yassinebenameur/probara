package ai

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/google/uuid"

	"github.com/yassinebenameur/probara/shared/models"
)

// DependencyAdvisor suggests monitor dependency edges from observed signals
// (co-firing alerts, name variants, and service/backing-resource structure).
// It is a separate capability from RootCauseAnalyzer but shares the same
// Provider/transport.
type DependencyAdvisor interface {
	SuggestDependencies(ctx context.Context, in DependencyInput) (DependencySuggestions, error)
}

// DependencyInput is the evidence handed to the model. Monitors are referenced
// by an opaque Ref (not name) so the model can't invent identifiers — the
// caller maps refs back to monitor IDs and discards anything unrecognized.
type DependencyInput struct {
	Monitors             []DependencyMonitorRef `json:"monitors"`
	ExistingDependencies []DependencyEdgeRef    `json:"existing_dependencies,omitempty"`
	CoFiringAlerts       []CoFiringPair         `json:"co_firing_alerts,omitempty"`
	// TagMonitors and PrefixMonitors are the tenant-wide populations
	// (MonitorPopulations over every candidate-eligible monitor of the
	// tenant, not only the capped Monitors). When nil, they are counted over
	// Monitors.
	TagMonitors    map[string]int `json:"-"`
	PrefixMonitors map[string]int `json:"-"`
}

// DependencyMonitorRef is a monitor the model may reference. Tags and Groups
// give the model naming/clustering signals beyond co-firing alerts.
type DependencyMonitorRef struct {
	Ref    string               `json:"ref"`
	Name   string               `json:"name"`
	Type   string               `json:"type"`
	Tags   []string             `json:"tags,omitempty"`
	Groups []DependencyGroupRef `json:"groups,omitempty"`
	// AlertWindows is the number of 10-minute buckets with an availability
	// alert of this monitor in the lookback (PairEvidence.AWindows/BWindows).
	AlertWindows int `json:"alert_windows"`
}

// DependencyGroupRef is a group a monitor belongs to: an opaque identity
// (never the group's name; two groups with one name are different groups)
// and its member count. Neither reaches the model except through
// PairEvidence's counters.
type DependencyGroupRef struct {
	Ref     string `json:"ref"`
	Members int    `json:"members"`
}

// DependencyEdgeRef is an existing edge: Monitor depends on DependsOn. Provided
// so the model doesn't re-suggest edges that already exist.
type DependencyEdgeRef struct {
	Monitor   string `json:"monitor"`
	DependsOn string `json:"depends_on"`
}

// CoFiringPair captures that Earlier's alerts tend to fire at or shortly before
// Later's, within a short window — a hint that Later may depend on Earlier.
type CoFiringPair struct {
	Earlier           string `json:"earlier_monitor"`
	Later             string `json:"later_monitor"`
	TimesCoFired      int    `json:"times_co_fired"`
	MedianLeadSeconds int    `json:"median_lead_seconds"`
	ReverseCoFires    int    `json:"reverse_co_fires"`
	// WindowMonitors is the median number of distinct monitors with an
	// availability alert in the pair's shared windows (the pair included).
	WindowMonitors int `json:"window_monitors"`
}

// Dependency suggestion bases: which rule of DependencyGuard let a pair reach
// the model. DependencyBasisNone pairs are never sent.
const (
	DependencyBasisTiming     = "timing"
	DependencyBasisVariant    = "variant"
	DependencyBasisStructural = "structural"
	DependencyBasisNone       = "none"
	// DependencyAbstain is DependencyGuard's label for DependencyBasisNone.
	DependencyAbstain = "abstain"
)

// DependencySuggestion proposes that Monitor depends on DependsOn.
type DependencySuggestion struct {
	Monitor    string   `json:"monitor"`
	DependsOn  string   `json:"depends_on"`
	Basis      string   `json:"basis"` // timing | variant | structural
	Reason     string   `json:"reason"`
	Confidence string   `json:"confidence"`
	Metadata   Metadata `json:"metadata"`
}

// DependencySuggestions is the model's output. Analyzed counts the pairs the
// advisor judged: every pair DependencyGuard rejected plus every pair sent to
// the model (guard-eligible pairs past the budget are neither).
// Analyzed == Abstained + len(Suggestions).
type DependencySuggestions struct {
	Suggestions []DependencySuggestion `json:"suggestions"`
	Model       string                 `json:"-"`
	Analyzed    int                    `json:"analyzed_pairs"`
	Abstained   int                    `json:"abstained_pairs"`
}

// PairEvidence contains measurements and boolean corroboration, never names.
// A and B are oriented a < b by ref. It stays a flat, comparable struct of
// ints and bools: snapshots and labels compare it with ==, and the label
// exporter decodes it strictly (decodeEvidence). Evidence stored under
// catalogue probara-evidence-v1 holds only the first seven fields; it still
// decodes, with the later fields zero.
type PairEvidence struct {
	CoFires        int  `json:"co_fires"`
	ALeadsSeconds  int  `json:"a_leads_seconds"` // >0: A fails first
	ReverseCoFires int  `json:"reverse_co_fires"`
	SharedService  bool `json:"shared_service"`
	SharedGroup    bool `json:"shared_group"`
	NamingSignal   bool `json:"naming_signal"`
	CommonUpstream bool `json:"common_upstream"`
	// Alert windows (10-minute buckets with an availability alert) of each
	// monitor in the lookback.
	AWindows int `json:"a_windows"`
	BWindows int `json:"b_windows"`
	// Median number of the tenant's monitors alerting in the pair's co-fire
	// windows; 0 when CoFires is 0.
	WindowMonitors int `json:"window_monitors"`
	// Population, among the advisor's candidate monitors, of the least common
	// shared non-generic tag; 0 when no such tag is shared.
	SharedTagMonitors int `json:"shared_tag_monitors"`
	// Member count of the smallest shared group (by identity); 0 when none.
	SharedGroupMonitors int `json:"shared_group_monitors"`
	// Leading name tokens the two names have in common (nameTokens), except
	// that a prefix of two or more tokens whose first two tokens are carried
	// by more than dependencyRareMax monitors of the tenant reports 1: an
	// environment-style prefix shared by many monitors is not a signal.
	SharedNamePrefix int `json:"shared_name_prefix"`
	// One name is the other's tokens followed by at least one more token, one
	// of which is not all digits ("db degraded" extends "db"; "node-2" does
	// not extend "node").
	AExtendsB bool `json:"a_extends_b"`
	BExtendsA bool `json:"b_extends_a"`
	SameType  bool `json:"same_type"`
	// The monitor's type is a backing resource (infraTypes).
	AInfra bool `json:"a_infra"`
	BInfra bool `json:"b_infra"`
	// Same-role peers: same type and either identical non-empty non-generic
	// tag sets with neither name extending the other, or names equal apart
	// from a trailing number ("web-01"/"web-02", "node"/"node-2").
	Peers bool `json:"peers"`
}

type candidate struct {
	a, b  string
	e     PairEvidence
	basis string // DependencyGuard(e)
	label string
}

// infraTypes are the monitor types that check a backing resource rather than
// a service. The ai-service guard keeps the same list.
var infraTypes = map[string]bool{
	string(models.MonitorTypeMongoDB):    true,
	string(models.MonitorTypeMySQL):      true,
	string(models.MonitorTypePostgres):   true,
	string(models.MonitorTypeRedis):      true,
	string(models.MonitorTypeRabbitMQ):   true,
	string(models.MonitorTypePrometheus): true,
	string(models.MonitorTypeAgent):      true,
}

// genericTag reports a tag too broad to corroborate anything.
func genericTag(tag string) bool {
	switch strings.ToLower(tag) {
	case "prod", "production", "dev", "staging", "test":
		return true
	}
	return false
}

// serviceTags is a monitor's set of non-empty, non-generic tags.
func serviceTags(tags []string) map[string]bool {
	out := map[string]bool{}
	for _, t := range tags {
		if t != "" && !genericTag(t) {
			out[t] = true
		}
	}
	return out
}

func intersection(a, b []string, ignoreGeneric bool) bool {
	for _, x := range a {
		for _, y := range b {
			if x == y && x != "" {
				if ignoreGeneric && genericTag(x) {
					continue
				}
				return true
			}
		}
	}
	return false
}

// nameTokens splits a monitor name into lowercase tokens on '-', '_', ' '
// and '.', dropping empty tokens.
func nameTokens(s string) []string {
	return strings.FieldsFunc(strings.ToLower(s), func(r rune) bool { return r == '-' || r == '_' || r == ' ' || r == '.' })
}

func naming(a, b string) bool {
	for _, x := range nameTokens(a) {
		if len(x) < 3 {
			continue
		}
		switch x {
		case "http", "tcp", "dns", "api", "db", "prod", "dev", "server", "monitor":
			continue
		}
		for _, y := range nameTokens(b) {
			if x == y {
				return true
			}
		}
	}
	return false
}

// commonPrefix is the number of leading tokens a and b share.
func commonPrefix(a, b []string) int {
	n := 0
	for n < len(a) && n < len(b) && a[n] == b[n] {
		n++
	}
	return n
}

// extends reports whether name x is name y's tokens followed by at least one
// more token, not all of them numbers ("X DEGRADED" extends "X"; "X 2" is a
// numbered sibling, not a variant).
func extends(x, y []string) bool {
	if len(y) == 0 || len(x) <= len(y) || commonPrefix(x, y) != len(y) {
		return false
	}
	for _, t := range x[len(y):] {
		if strings.TrimLeft(t, "0123456789") != "" {
			return true
		}
	}
	return false
}

// unnumbered drops a trailing number from a name's tokens: trailing ASCII
// digits are stripped from the last token, which is dropped if nothing is
// left ("web-01" -> [web], "node-2" -> [node], "web1" -> [web]).
func unnumbered(tokens []string) []string {
	if len(tokens) == 0 {
		return tokens
	}
	out := append([]string(nil), tokens...)
	last := strings.TrimRight(out[len(out)-1], "0123456789")
	if last == "" {
		return out[:len(out)-1]
	}
	out[len(out)-1] = last
	return out
}

func equalTokens(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// prefixKey is the population key of a name's two-token prefix ("" when the
// name has fewer than two tokens). Tokens never contain a space.
func prefixKey(tokens []string) string {
	if len(tokens) < 2 {
		return ""
	}
	return tokens[0] + " " + tokens[1]
}

// MonitorPopulations counts, over ms, how many monitors carry each
// non-generic tag and each two-token name prefix. The depsuggest service
// calls it over every candidate-eligible monitor of the tenant for
// DependencyInput.TagMonitors/PrefixMonitors; allCandidatePairs falls back to
// it over the input's monitors.
func MonitorPopulations(ms []DependencyMonitorRef) (tags, prefixes map[string]int) {
	tags, prefixes = map[string]int{}, map[string]int{}
	for _, m := range ms {
		for t := range serviceTags(m.Tags) {
			tags[t]++
		}
		if k := prefixKey(nameTokens(m.Name)); k != "" {
			prefixes[k]++
		}
	}
	return tags, prefixes
}

// dependencyRareMax bounds a "rare" tag or group population: one held by more
// monitors is not corroboration. testdata/dependency-guard-cases.json pins it.
const dependencyRareMax = 10

func rare(n int) bool { return n > 0 && n <= dependencyRareMax }

// DependencyGuard is the only rule deciding whether a pair's evidence can
// support a dependency, and in which direction. It returns the basis
// (timing, variant, structural, or none) and the one label the model must
// pick for a suggestion ("abstain" with basis none). The ai-service has the
// same guard; testdata/dependency-guard-cases.json and
// testdata/dependency-regression.json pin both.
//
// A known common upstream abstains. Otherwise the first match wins:
//  1. variant: exactly one name extends the other, >= 2 co-fires, none
//     reversed, a time direction, and the base fails first; the extension
//     depends on the base. If the extension fails first, fall through.
//  2. timing: >= 3 co-fires, none reversed, a time direction, a median of at
//     most 3 monitors alerting in those windows, not peers, and corroborated
//     by a rare tag, a rare group or a name prefix of >= 2 tokens; the later
//     monitor depends on the earlier.
//  3. structural: exactly one is a backing resource, not peers, and a rare
//     tag or a name prefix of >= 2 tokens (a small group alone is not
//     enough); the service depends on the resource. Timing is ignored.
func DependencyGuard(e PairEvidence) (basis, label string) {
	if e.CommonUpstream {
		return DependencyBasisNone, DependencyAbstain
	}
	directed := e.ReverseCoFires == 0 && e.ALeadsSeconds != 0
	// A backing resource never depends on the service that uses it. A timing
	// or variant direction that says so is detection latency (the service
	// check alerts sooner), so that basis does not apply and the pair falls
	// through to the structural rule or abstains.
	againstRoles := func(label string) bool {
		return e.AInfra != e.BInfra && ((label == "a_depends_b" && e.AInfra) || (label == "b_depends_a" && e.BInfra))
	}
	if e.AExtendsB != e.BExtendsA && e.CoFires >= 2 && directed {
		label := ""
		if e.AExtendsB && e.ALeadsSeconds < 0 {
			label = "a_depends_b"
		} else if e.BExtendsA && e.ALeadsSeconds > 0 {
			label = "b_depends_a"
		}
		if label != "" && !againstRoles(label) {
			return DependencyBasisVariant, label
		}
	}
	corroborated := rare(e.SharedTagMonitors) || rare(e.SharedGroupMonitors) || e.SharedNamePrefix >= 2
	if e.CoFires >= 3 && directed && e.WindowMonitors <= 3 && !e.Peers && corroborated {
		label := "a_depends_b"
		if e.ALeadsSeconds > 0 {
			label = "b_depends_a"
		}
		if !againstRoles(label) {
			return DependencyBasisTiming, label
		}
	}
	if e.AInfra != e.BInfra && !e.Peers && (rare(e.SharedTagMonitors) || e.SharedNamePrefix >= 2) {
		if e.BInfra {
			return DependencyBasisStructural, "a_depends_b"
		}
		return DependencyBasisStructural, "b_depends_a"
	}
	return DependencyBasisNone, DependencyAbstain
}

// maxCandidatePairs is the per-request decision budget of SuggestDependencies.
const maxCandidatePairs = 50

// basisOrder ranks the bases for the decision budget.
var basisOrder = map[string]int{DependencyBasisTiming: 0, DependencyBasisVariant: 1, DependencyBasisStructural: 2}

// CandidatePairs returns the pairs the advisor sends to the model: only those
// DependencyGuard gives a basis, timing first, then variant, then structural,
// strongest evidence first within a basis, capped at the decision budget.
// Pairs are oriented a < b by ref.
func CandidatePairs(in DependencyInput) []candidate {
	out, _ := splitCandidates(allCandidatePairs(in))
	return out
}

// splitCandidates returns the budgeted, ordered guard-eligible pairs and the
// number of pairs the guard rejected.
func splitCandidates(all []candidate) (eligible []candidate, rejected int) {
	for _, c := range all {
		if c.basis == DependencyBasisNone {
			rejected++
			continue
		}
		eligible = append(eligible, c)
	}
	sort.SliceStable(eligible, func(i, j int) bool {
		a, b := eligible[i], eligible[j]
		if a.basis != b.basis {
			return basisOrder[a.basis] < basisOrder[b.basis]
		}
		switch a.basis {
		case DependencyBasisTiming:
			if a.e.CoFires != b.e.CoFires {
				return a.e.CoFires > b.e.CoFires
			}
			if a.e.WindowMonitors != b.e.WindowMonitors {
				return a.e.WindowMonitors < b.e.WindowMonitors
			}
		case DependencyBasisVariant:
			if a.e.CoFires != b.e.CoFires {
				return a.e.CoFires > b.e.CoFires
			}
		case DependencyBasisStructural:
			// A rarer shared tag first (no rare tag ranks last), then a
			// longer shared name prefix.
			tag := func(e PairEvidence) int {
				if rare(e.SharedTagMonitors) {
					return e.SharedTagMonitors
				}
				return dependencyRareMax + 1
			}
			if tag(a.e) != tag(b.e) {
				return tag(a.e) < tag(b.e)
			}
			if a.e.SharedNamePrefix != b.e.SharedNamePrefix {
				return a.e.SharedNamePrefix > b.e.SharedNamePrefix
			}
		}
		if a.a != b.a {
			return a.a < b.a
		}
		return a.b < b.b
	})
	if len(eligible) > maxCandidatePairs {
		eligible = eligible[:maxCandidatePairs]
	}
	return eligible, rejected
}

// PairDecisionFor returns the evidence the advisor would compute for the pair
// {refX, refY}, oriented a < b by ref, whether or not the guard or the
// 50-pair budget would send it. ok=false when the pair is not a candidate.
func PairDecisionFor(in DependencyInput, refX, refY string) (aRef, bRef string, e PairEvidence, ok bool) {
	if refX == refY {
		return "", "", PairEvidence{}, false
	}
	if refX > refY {
		refX, refY = refY, refX
	}
	for _, c := range allCandidatePairs(in) {
		if c.a == refX && c.b == refY {
			return c.a, c.b, c.e, true
		}
	}
	return "", "", PairEvidence{}, false
}

// allCandidatePairs is every pair the advisor considers, before the guard and
// the budget. Operator labels (PairDecisionFor) resolve through it so a pair
// the advisor did not send gets the same evidence it would compute for it.
func allCandidatePairs(in DependencyInput) []candidate {
	refs := map[string]DependencyMonitorRef{}
	for _, m := range in.Monitors {
		refs[m.Ref] = m
	}
	pairs := map[[2]string]PairEvidence{}
	key := func(a, b string) [2]string {
		if a > b {
			return [2]string{b, a}
		}
		return [2]string{a, b}
	}
	for _, p := range in.CoFiringAlerts {
		if p.Earlier == p.Later || p.TimesCoFired < 1 {
			continue
		}
		if _, ok := refs[p.Earlier]; !ok {
			continue
		}
		if _, ok := refs[p.Later]; !ok {
			continue
		}
		k := key(p.Earlier, p.Later)
		e := pairs[k]
		if e.CoFires > 0 {
			e.ReverseCoFires += p.TimesCoFired
		} else {
			e.CoFires = p.TimesCoFired
			e.ReverseCoFires = p.ReverseCoFires
			e.WindowMonitors = p.WindowMonitors
			e.ALeadsSeconds = p.MedianLeadSeconds
			if p.Earlier != k[0] {
				e.ALeadsSeconds = -e.ALeadsSeconds
			}
		}
		pairs[k] = e
	}
	upstream := map[string]map[string]bool{}
	for _, edge := range in.ExistingDependencies {
		if upstream[edge.Monitor] == nil {
			upstream[edge.Monitor] = map[string]bool{}
		}
		upstream[edge.Monitor][edge.DependsOn] = true
	}
	// Per-monitor derivations, and the populations (tenant-wide when the
	// caller passed them).
	tokens := map[string][]string{}
	tags := map[string]map[string]bool{}
	for _, m := range in.Monitors {
		tokens[m.Ref] = nameTokens(m.Name)
		tags[m.Ref] = serviceTags(m.Tags)
	}
	tagPop, prefixPop := in.TagMonitors, in.PrefixMonitors
	if tagPop == nil || prefixPop == nil {
		t, p := MonitorPopulations(in.Monitors)
		if tagPop == nil {
			tagPop = t
		}
		if prefixPop == nil {
			prefixPop = p
		}
	}
	for i, x := range in.Monitors {
		for _, y := range in.Monitors[i+1:] {
			k := key(x.Ref, y.Ref)
			a, b := refs[k[0]], refs[k[1]]
			e := pairs[k]
			e.SharedService = intersection(a.Tags, b.Tags, true)
			e.SharedGroupMonitors, e.SharedGroup = sharedGroupMembers(a, b)
			e.NamingSignal = naming(a.Name, b.Name)
			for u := range upstream[a.Ref] {
				if upstream[b.Ref][u] {
					e.CommonUpstream = true
				}
			}
			e.AWindows, e.BWindows = a.AlertWindows, b.AlertWindows
			e.SharedTagMonitors = sharedTagPopulation(tags[a.Ref], tags[b.Ref], tagPop)
			ta, tb := tokens[a.Ref], tokens[b.Ref]
			e.SharedNamePrefix = commonPrefix(ta, tb)
			if e.SharedNamePrefix >= 2 && max(prefixPop[prefixKey(ta)], 2) > dependencyRareMax {
				e.SharedNamePrefix = 1
			}
			e.AExtendsB, e.BExtendsA = extends(ta, tb), extends(tb, ta)
			e.SameType = a.Type == b.Type
			e.AInfra, e.BInfra = infraTypes[a.Type], infraTypes[b.Type]
			sameTags := len(tags[a.Ref]) > 0 && sameSet(tags[a.Ref], tags[b.Ref]) && !e.AExtendsB && !e.BExtendsA
			numbered := a.Name != b.Name && equalTokens(unnumbered(ta), unnumbered(tb))
			e.Peers = e.SameType && (sameTags || numbered)
			// A two-token name prefix is a structural signal of its own, so
			// such a pair stays a candidate (and labelable) without a
			// naming token or tag.
			if e.CoFires > 0 || e.SharedService || e.SharedGroup || e.NamingSignal || e.SharedNamePrefix >= 2 {
				pairs[k] = e
			}
		}
	}
	out := []candidate{}
	for k, e := range pairs {
		basis, label := DependencyGuard(e)
		out = append(out, candidate{a: k[0], b: k[1], e: e, basis: basis, label: label})
	}
	sort.Slice(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if a.e.CoFires != b.e.CoFires {
			return a.e.CoFires > b.e.CoFires
		}
		rank := func(e PairEvidence) int {
			n := 0
			if e.SharedService {
				n += 4
			}
			if e.SharedGroup {
				n += 2
			}
			if e.NamingSignal {
				n++
			}
			return n
		}
		if rank(a.e) != rank(b.e) {
			return rank(a.e) > rank(b.e)
		}
		if a.a != b.a {
			return a.a < b.a
		}
		return a.b < b.b
	})
	return out
}

// sharedTagPopulation is the population of the least common tag in both sets,
// or 0 when they share none. Both monitors carry the tag, so a population
// below 2 (a stale or partial count) reads as 2.
func sharedTagPopulation(a, b map[string]bool, population map[string]int) int {
	least := 0
	for t := range a {
		if !b[t] {
			continue
		}
		if n := max(population[t], 2); least == 0 || n < least {
			least = n
		}
	}
	return least
}

// sharedGroupMembers reports whether the monitors share a group (matched by
// opaque ref, never by name) and the member count of the smallest one (0
// when none, or when no count is known).
func sharedGroupMembers(a, b DependencyMonitorRef) (members int, shared bool) {
	for _, x := range a.Groups {
		for _, y := range b.Groups {
			if x.Ref == "" || x.Ref != y.Ref {
				continue
			}
			shared = true
			if n := max(x.Members, y.Members); n > 0 && (members == 0 || n < members) {
				members = n
			}
		}
	}
	return members, shared
}

func sameSet(a, b map[string]bool) bool {
	if len(a) != len(b) {
		return false
	}
	for k := range a {
		if !b[k] {
			return false
		}
	}
	return true
}

func WouldCycle(edges []DependencyEdgeRef, monitor, upstream string) bool {
	graph := map[string][]string{}
	for _, e := range edges {
		graph[e.Monitor] = append(graph[e.Monitor], e.DependsOn)
	}
	visited := map[string]bool{}
	todo := []string{upstream}
	for len(todo) > 0 {
		n := todo[len(todo)-1]
		todo = todo[:len(todo)-1]
		if n == monitor {
			return true
		}
		if visited[n] {
			continue
		}
		visited[n] = true
		todo = append(todo, graph[n]...)
	}
	return false
}

// proposal is a pair the guard and the model agreed on, before the
// variant, existing-edge and cycle filters.
type proposal struct {
	c                 candidate
	monitor, upstream string
	score             float64
}

// dropVariantUpstreams removes a proposal "M depends on Y" when the same
// monitor also has a proposal "M depends on X" and Y's name extends X's
// ("X DEGRADED" is a variant of X): only the suggestion to the base stays.
func dropVariantUpstreams(ps []proposal, tokens map[string][]string) (kept []proposal, dropped int) {
	for _, p := range ps {
		variant := false
		for _, q := range ps {
			if q.monitor == p.monitor && q.upstream != p.upstream && extends(tokens[p.upstream], tokens[q.upstream]) {
				variant = true
				break
			}
		}
		if variant {
			dropped++
			continue
		}
		kept = append(kept, p)
	}
	return kept, dropped
}

// dependencyReason renders a suggestion's explanation from its evidence only
// (never names), per basis, ending with a caveat.
func dependencyReason(basis string, e PairEvidence) string {
	lead := e.ALeadsSeconds
	if lead < 0 {
		lead = -lead
	}
	switch basis {
	case DependencyBasisTiming:
		r := fmt.Sprintf("Alert timing: %d co-firing alert windows in 30 days, the proposed upstream failing first by a median %d seconds and never after it", e.CoFires, lead)
		var by []string
		if rare(e.SharedTagMonitors) {
			by = append(by, fmt.Sprintf("a tag held by only %d monitors", e.SharedTagMonitors))
		}
		if rare(e.SharedGroupMonitors) {
			by = append(by, fmt.Sprintf("a group of %d monitors", e.SharedGroupMonitors))
		}
		if e.SharedNamePrefix >= 2 {
			by = append(by, fmt.Sprintf("a %d-token shared name prefix", e.SharedNamePrefix))
		}
		if len(by) > 0 {
			r += ", corroborated by " + strings.Join(by, " and ")
		}
		r += fmt.Sprintf(", in windows where a median of %d monitors alerted. Timing correlation does not prove causation.", e.WindowMonitors)
		return r
	case DependencyBasisVariant:
		return fmt.Sprintf("Same target variant: this monitor's name extends the proposed upstream's, and it failed after its base in %d alert windows in 30 days (median %d seconds later), never before it. A variant usually checks the same target; confirm it is not an independent check.", e.CoFires, lead)
	case DependencyBasisStructural:
		var by []string
		if rare(e.SharedTagMonitors) {
			by = append(by, fmt.Sprintf("a tag held by only %d monitors", e.SharedTagMonitors))
		}
		if e.SharedNamePrefix >= 2 {
			by = append(by, fmt.Sprintf("a %d-token name prefix", e.SharedNamePrefix))
		}
		return "Structure, no alert correlation required: a service and a backing resource sharing " + strings.Join(by, " and ") + ". Shared naming or tagging does not prove the service uses this resource."
	}
	return ""
}

func (a *localAnalyzer) SuggestDependencies(ctx context.Context, in DependencyInput) (DependencySuggestions, error) {
	// Only guard-eligible pairs reach the model, so pairs the guard rejects
	// (simultaneous pile-ups with the most co-fires) never spend the budget.
	// They count as analyzed and abstained without a model call.
	pairs, rejected := splitCandidates(allCandidatePairs(in))
	out := DependencySuggestions{Suggestions: []DependencySuggestion{}, Analyzed: rejected + len(pairs), Abstained: rejected}
	if len(pairs) == 0 {
		return out, nil
	}
	ds := []Decision{}
	for _, p := range pairs {
		ds = append(ds, Decision{ID: uuid.NewString(), Task: "dependency", Evidence: p.e, Choices: choices("dependency")})
	}
	batch, err := a.decide(ctx, ds)
	if err != nil {
		return out, err
	}
	out.Model = batch.Model.Version
	// A suggestion needs the model's best choice to be the guard's label at
	// or above the calibrated threshold.
	var proposals []proposal
	for i, r := range batch.Results {
		h, score := best(r)
		p := pairs[i]
		if h != p.label || score < batch.Thresholds["dependency"] {
			out.Abstained++
			continue
		}
		monitor, upstream := p.a, p.b
		if h == "b_depends_a" {
			monitor, upstream = p.b, p.a
		}
		proposals = append(proposals, proposal{c: p, monitor: monitor, upstream: upstream, score: score})
	}
	// Existing-edge and cycle filters run before the variant de-dup, so a
	// base suggestion they reject cannot also cost its variant.
	edges := append([]DependencyEdgeRef{}, in.ExistingDependencies...)
	existing := map[[2]string]bool{}
	for _, e := range edges {
		existing[[2]string{e.Monitor, e.DependsOn}] = true
	}
	var acyclic []proposal
	for _, p := range proposals {
		if existing[[2]string{p.monitor, p.upstream}] || WouldCycle(edges, p.monitor, p.upstream) {
			out.Abstained++
			continue
		}
		acyclic = append(acyclic, p)
		edges = append(edges, DependencyEdgeRef{Monitor: p.monitor, DependsOn: p.upstream})
		existing[[2]string{p.monitor, p.upstream}] = true
	}
	tokens := map[string][]string{}
	for _, m := range in.Monitors {
		tokens[m.Ref] = nameTokens(m.Name)
	}
	acyclic, dropped := dropVariantUpstreams(acyclic, tokens)
	out.Abstained += dropped

	for _, p := range acyclic {
		reason := dependencyReason(p.c.basis, p.c.e)
		kind := "alerts"
		level := confidence(p.score)
		if p.c.basis == DependencyBasisStructural {
			// Structure is weaker evidence than measured failures: never
			// more than low confidence, whatever the calibrated score.
			kind, level = "structure", "low"
		}
		refs := []EvidenceReference{{Ref: fmt.Sprintf("pair/%s/%s/%s", p.c.a, p.c.b, kind), Fact: reason}}
		out.Suggestions = append(out.Suggestions, DependencySuggestion{Monitor: p.monitor, DependsOn: p.upstream, Basis: p.c.basis, Reason: reason, Confidence: level, Metadata: metadata(batch.Model, p.c.label, p.score, refs, nil)})
	}
	return out, nil
}

func NewDependencyAdvisor(cfg Config) (DependencyAdvisor, error) {
	a, err := newLocalAnalyzer(cfg)
	if err != nil {
		// Literal nil keeps the interface nil on error (see NewAnalyzer).
		return nil, err
	}
	return a, nil
}
