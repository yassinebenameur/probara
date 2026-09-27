// Package metricstore is the single implementation of the generic OTel
// metric store (migration 000084): series identity/canonicalization, the
// ingest write path, and the freshness-bounded read paths shared by the API
// query endpoints, the alerter, and status pages. Anything that names a
// series — ingest dedup, alert identity, chart labels — must go through the
// canonical forms defined here, never a parallel encoding.
package metricstore

import (
	"crypto/sha256"
	"encoding/json"
	"sort"
	"strings"
)

// CanonicalAttributes renders attributes as the canonical JSON object stored
// in metric_series.attributes: sorted keys (encoding/json sorts map keys),
// string values. This exact byte sequence feeds AttrHash, so it must never
// change shape once data exists.
func CanonicalAttributes(attrs map[string]string) []byte {
	if len(attrs) == 0 {
		return []byte("{}")
	}
	// map[string]string marshals with sorted keys and cannot fail.
	b, _ := json.Marshal(attrs)
	return b
}

// AttrHash is the series-identity hash stored in metric_series.attr_hash:
// sha256 over metric_name, a NUL separator, and the canonical attributes.
func AttrHash(metricName string, attrs map[string]string) []byte {
	h := sha256.New()
	h.Write([]byte(metricName))
	h.Write([]byte{0})
	h.Write(CanonicalAttributes(attrs))
	return h.Sum(nil)
}

// SeriesKeyString is the human-readable canonical series key used as alert
// identity (alerts.metric_name) and in UI labels:
//
//	system.filesystem.utilization{mountpoint=/data,state=used}
//
// Keys are sorted; a series with no attributes is just the metric name.
// Attribute values containing ',' or '}' would make the string ambiguous —
// rule validation forbids them in filters, and semconv host metrics never
// produce them; the string form is identity, not a parseable wire format,
// so the residual ambiguity is accepted.
func SeriesKeyString(metricName string, attrs map[string]string) string {
	if len(attrs) == 0 {
		return metricName
	}
	keys := make([]string, 0, len(attrs))
	for k := range attrs {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	b.WriteString(metricName)
	b.WriteByte('{')
	for i, k := range keys {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString(k)
		b.WriteByte('=')
		b.WriteString(attrs[k])
	}
	b.WriteByte('}')
	return b.String()
}

// ParseSeriesKeyString splits a canonical series key back into metric name
// and attributes. Inputs that are plain names (legacy alert keys like "cpu")
// return an empty attribute map.
func ParseSeriesKeyString(key string) (string, map[string]string) {
	open := strings.IndexByte(key, '{')
	if open < 0 || !strings.HasSuffix(key, "}") {
		return key, map[string]string{}
	}
	name := key[:open]
	attrs := map[string]string{}
	body := key[open+1 : len(key)-1]
	if body == "" {
		return name, attrs
	}
	for _, pair := range strings.Split(body, ",") {
		if eq := strings.IndexByte(pair, '='); eq >= 0 {
			attrs[pair[:eq]] = pair[eq+1:]
		}
	}
	return name, attrs
}
