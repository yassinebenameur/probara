package worker

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"go.mongodb.org/mongo-driver/v2/mongo"

	"github.com/yassinebenameur/probara/shared/models"
)

// mongoMetrics is the MongoDB metrics envelope: the shared dbMetrics fields
// inline plus the optional cluster-check groups, marshaled under the
// "mongodb" key ({"mongodb": {"server_version": ..., "cache_used_bytes": ...}}).
// Every cluster field is omitempty — a monitor with no cluster checks enabled
// produces exactly the same JSON as before.
type mongoMetrics struct {
	dbMetrics

	// serverStatus-backed groups (clusterMonitor role).
	UptimeSeconds        *int64           `json:"uptime_seconds,omitempty"`
	ConnectionsAvailable *int64           `json:"connections_available,omitempty"`
	MemVirtualBytes      *int64           `json:"mem_virtual_bytes,omitempty"`
	CacheUsedBytes       *int64           `json:"cache_used_bytes,omitempty"`
	CacheMaxBytes        *int64           `json:"cache_max_bytes,omitempty"`
	CacheDirtyBytes      *int64           `json:"cache_dirty_bytes,omitempty"`
	NetworkBytesIn       *int64           `json:"network_bytes_in,omitempty"`  // cumulative since restart
	NetworkBytesOut      *int64           `json:"network_bytes_out,omitempty"` // cumulative since restart
	NetworkRequests      *int64           `json:"network_requests,omitempty"`
	Opcounters           map[string]int64 `json:"opcounters,omitempty"`
	// Process CPU time consumed by mongod (getrusage, Linux; cumulative µs).
	// Host CPU is not observable through clusterMonitor — that's agent territory.
	CPUUserMicros   *int64 `json:"cpu_user_us,omitempty"`
	CPUSystemMicros *int64 `json:"cpu_system_us,omitempty"`

	// replSetGetStatus-backed group (clusterMonitor role).
	Replication *mongoReplicationMetrics `json:"replication,omitempty"`
	// ReplicationLagWarnSeconds mirrors latency_warn_ms: set when max lag
	// exceeded warn_replication_lag_seconds but the check still succeeded.
	ReplicationLagWarnSeconds *int64 `json:"replication_lag_warn_seconds,omitempty"`

	// Unavailable lists enabled cluster checks that could not run. With no
	// hard assertion depending on them the check stays a success — the UI
	// surfaces the reason (e.g. "grant clusterMonitor") instead.
	Unavailable []mongoUnavailableCheck `json:"unavailable,omitempty"`
}

type mongoUnavailableCheck struct {
	Check   string `json:"check"`             // "server_status" | "repl_set_status"
	Reason  string `json:"reason"`            // "unauthorized" | "not_replica_set" | "error"
	Message string `json:"message,omitempty"` // trimmed server error for reason "error"
}

type mongoReplicationMetrics struct {
	Set            string                   `json:"set,omitempty"`
	Primary        string                   `json:"primary,omitempty"` // host:port; "" when the set has no primary
	MembersTotal   int                      `json:"members_total"`
	MembersHealthy int                      `json:"members_healthy"`
	// MaxLagSeconds is nil when lag is undefined: no primary, or no
	// secondaries to lag behind it.
	MaxLagSeconds *int64                   `json:"max_lag_seconds,omitempty"`
	Members       []mongoReplicationMember `json:"members,omitempty"`
}

type mongoReplicationMember struct {
	Name       string `json:"name"`
	State      string `json:"state"` // stateStr: PRIMARY/SECONDARY/ARBITER/...
	Health     bool   `json:"health"`
	LagSeconds *int64 `json:"lag_seconds,omitempty"` // secondaries only, when a primary exists
}

// mongoServerStatusDoc decodes the serverStatus sections the cluster checks
// read. Pointer sections tolerate servers that omit them (e.g. no wiredTiger
// on the in-memory engine); the space-containing keys are real BSON field
// names in the WiredTiger cache document.
type mongoServerStatusDoc struct {
	Uptime      float64 `bson:"uptime"`
	Connections *struct {
		Current   int64 `bson:"current"`
		Available int64 `bson:"available"`
	} `bson:"connections"`
	Mem *struct {
		Resident int64 `bson:"resident"` // MB
		Virtual  int64 `bson:"virtual"`  // MB
	} `bson:"mem"`
	WiredTiger *struct {
		Cache struct {
			Used  int64 `bson:"bytes currently in the cache"`
			Max   int64 `bson:"maximum bytes configured"`
			Dirty int64 `bson:"tracked dirty bytes in the cache"`
		} `bson:"cache"`
	} `bson:"wiredTiger"`
	Network *struct {
		BytesIn     int64 `bson:"bytesIn"`
		BytesOut    int64 `bson:"bytesOut"`
		NumRequests int64 `bson:"numRequests"`
	} `bson:"network"`
	Opcounters *struct {
		Insert  int64 `bson:"insert"`
		Query   int64 `bson:"query"`
		Update  int64 `bson:"update"`
		Delete  int64 `bson:"delete"`
		Getmore int64 `bson:"getmore"`
		Command int64 `bson:"command"`
	} `bson:"opcounters"`
	ExtraInfo *struct {
		UserTimeUs   int64 `bson:"user_time_us"`   // "fields vary by platform" —
		SystemTimeUs int64 `bson:"system_time_us"` // zero when the host OS omits them
	} `bson:"extra_info"`
}

type mongoReplSetStatusDoc struct {
	Set     string `bson:"set"`
	Members []struct {
		Name       string    `bson:"name"`
		State      int32     `bson:"state"`
		StateStr   string    `bson:"stateStr"`
		Health     float64   `bson:"health"`
		OptimeDate time.Time `bson:"optimeDate"`
	} `bson:"members"`
}

const (
	mongoStatePrimary   = 1
	mongoStateSecondary = 2
)

// applyMongoServerStatus copies the enabled serverStatus groups into the
// metrics envelope. Absent sections (nil pointers) emit nothing; uptime is
// always set — it is free once the command has run.
func applyMongoServerStatus(m *mongoMetrics, doc mongoServerStatusDoc, config models.MongoDBMonitorConfig) {
	uptime := int64(doc.Uptime)
	m.UptimeSeconds = &uptime

	if config.CollectConnections && doc.Connections != nil {
		current, available := doc.Connections.Current, doc.Connections.Available
		m.ConnectedClients = &current
		m.ConnectionsAvailable = &available
	}
	if config.CollectMemory && doc.Mem != nil {
		// mem.resident/virtual are MB; 0 means "not reported on this platform".
		if doc.Mem.Resident > 0 {
			resident := doc.Mem.Resident * 1024 * 1024
			m.UsedMemoryBytes = &resident
		}
		if doc.Mem.Virtual > 0 {
			virtual := doc.Mem.Virtual * 1024 * 1024
			m.MemVirtualBytes = &virtual
		}
	}
	if config.CollectCache && doc.WiredTiger != nil {
		used, max, dirty := doc.WiredTiger.Cache.Used, doc.WiredTiger.Cache.Max, doc.WiredTiger.Cache.Dirty
		m.CacheUsedBytes = &used
		m.CacheMaxBytes = &max
		m.CacheDirtyBytes = &dirty
	}
	if config.CollectCPU && doc.ExtraInfo != nil && (doc.ExtraInfo.UserTimeUs > 0 || doc.ExtraInfo.SystemTimeUs > 0) {
		user, system := doc.ExtraInfo.UserTimeUs, doc.ExtraInfo.SystemTimeUs
		m.CPUUserMicros = &user
		m.CPUSystemMicros = &system
	}
	if config.CollectNetwork {
		if doc.Network != nil {
			in, out, reqs := doc.Network.BytesIn, doc.Network.BytesOut, doc.Network.NumRequests
			m.NetworkBytesIn = &in
			m.NetworkBytesOut = &out
			m.NetworkRequests = &reqs
		}
		if doc.Opcounters != nil {
			m.Opcounters = map[string]int64{
				"insert":  doc.Opcounters.Insert,
				"query":   doc.Opcounters.Query,
				"update":  doc.Opcounters.Update,
				"delete":  doc.Opcounters.Delete,
				"getmore": doc.Opcounters.Getmore,
				"command": doc.Opcounters.Command,
			}
		}
	}
}

// buildMongoReplicationMetrics derives member health and replication lag from
// replSetGetStatus. replSetGetStatus reports heartbeat-derived optimes for
// every member regardless of which node served the command, so lag is
// primary optime minus secondary optime, clamped at zero (heartbeat skew can
// produce small negatives).
func buildMongoReplicationMetrics(doc mongoReplSetStatusDoc) *mongoReplicationMetrics {
	repl := &mongoReplicationMetrics{
		Set:          doc.Set,
		MembersTotal: len(doc.Members),
	}

	var primaryOptime time.Time
	var hasPrimary bool
	for _, member := range doc.Members {
		if member.State == mongoStatePrimary {
			repl.Primary = member.Name
			primaryOptime = member.OptimeDate
			hasPrimary = true
			break
		}
	}

	for _, member := range doc.Members {
		out := mongoReplicationMember{
			Name:   member.Name,
			State:  member.StateStr,
			Health: member.Health > 0,
		}
		if out.Health {
			repl.MembersHealthy++
		}
		if hasPrimary && member.State == mongoStateSecondary {
			lag := int64(primaryOptime.Sub(member.OptimeDate) / time.Second)
			if lag < 0 {
				lag = 0
			}
			out.LagSeconds = &lag
			if repl.MaxLagSeconds == nil || lag > *repl.MaxLagSeconds {
				v := lag
				repl.MaxLagSeconds = &v
			}
		}
		repl.Members = append(repl.Members, out)
	}
	return repl
}

// mongoCmdUnavailable classifies why an enabled cluster command could not run.
func mongoCmdUnavailable(check string, err error) mongoUnavailableCheck {
	var cmdErr mongo.CommandError
	if errors.As(err, &cmdErr) {
		switch cmdErr.Code {
		case 13: // Unauthorized
			return mongoUnavailableCheck{Check: check, Reason: "unauthorized"}
		case 76: // NoReplicationEnabled — standalone mongod
			return mongoUnavailableCheck{Check: check, Reason: "not_replica_set"}
		}
	}
	// Some proxies/hosted tiers wrap the code away; fall back on the message.
	lower := strings.ToLower(err.Error())
	if strings.Contains(lower, "not authorized") || strings.Contains(lower, "unauthorized") {
		return mongoUnavailableCheck{Check: check, Reason: "unauthorized"}
	}
	if strings.Contains(lower, "not running with --replset") || strings.Contains(lower, "noreplicationenabled") {
		return mongoUnavailableCheck{Check: check, Reason: "not_replica_set"}
	}
	return mongoUnavailableCheck{Check: check, Reason: "error", Message: truncateMongoError(err.Error())}
}

func truncateMongoError(msg string) string {
	const maxLen = 200
	if len(msg) > maxLen {
		return msg[:maxLen] + "…"
	}
	return msg
}

// mongoFinishResult turns a successful ping plus collected metrics into the
// final check result: latency thresholds first (shared semantics), then the
// replication-lag assertion. A configured max_replication_lag_seconds fails
// CLOSED — if lag cannot be evaluated (unauthorized, standalone, command
// error, no primary, no secondaries) the check fails rather than silently
// passing, otherwise a lagging cluster would look healthy and an open alert
// could resolve just because the monitoring role was revoked. Without a max
// threshold every cluster-check problem stays a non-fatal annotation.
func mongoFinishResult(config models.MongoDBMonitorConfig, m *mongoMetrics, latencyMs int64) CheckResult {
	lag := (*int64)(nil)
	if m.Replication != nil {
		lag = m.Replication.MaxLagSeconds
	}

	// Warn annotation goes on the envelope before it is marshaled.
	warn := config.WarnReplicationLagSeconds
	if lag != nil && warn != nil && *warn > 0 && *lag > *warn {
		m.ReplicationLagWarnSeconds = warn
	}

	result := dbLatencyResultEnvelope("mongodb", latencyMs, config.MaxLatencyMs, config.WarnLatencyMs, &m.dbMetrics, m)
	if result.Status != "success" {
		return result
	}

	max := config.MaxReplicationLagSeconds
	if max == nil || *max <= 0 {
		return result
	}
	if lag == nil {
		reason := "replication status not collected"
		for _, u := range m.Unavailable {
			if u.Check == "repl_set_status" {
				reason = u.Reason
				if u.Message != "" {
					reason = fmt.Sprintf("%s: %s", u.Reason, u.Message)
				}
				break
			}
		}
		if m.Replication != nil {
			if m.Replication.Primary == "" {
				reason = "no primary"
			} else {
				reason = "no secondaries"
			}
		}
		return dbFailureResultEnvelope("mongodb", latencyMs, m,
			fmt.Sprintf("replication_lag: cannot evaluate (%s)", reason))
	}
	if *lag > *max {
		return dbFailureResultEnvelope("mongodb", latencyMs, m,
			fmt.Sprintf("replication_lag: %ds > %ds", *lag, *max))
	}
	return result
}
