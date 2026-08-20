package worker

import (
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"

	"github.com/yassinebenameur/probara/shared/models"
)

// decodeServerStatus round-trips a fixture through real BSON so the decode
// path — including the space-containing WiredTiger cache keys — is exercised
// the same way RunCommand().Decode exercises it.
func decodeServerStatus(t *testing.T, fixture bson.M) mongoServerStatusDoc {
	t.Helper()
	raw, err := bson.Marshal(fixture)
	if err != nil {
		t.Fatalf("marshal fixture: %v", err)
	}
	var doc mongoServerStatusDoc
	if err := bson.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("unmarshal fixture: %v", err)
	}
	return doc
}

func fullServerStatusFixture() bson.M {
	return bson.M{
		"uptime": 12345.6,
		"connections": bson.M{
			"current":   int32(42),
			"available": int64(958),
		},
		"mem": bson.M{
			"resident": int32(512),
			"virtual":  int32(2048),
		},
		"wiredTiger": bson.M{
			"cache": bson.M{
				"bytes currently in the cache":     int64(100_000_000),
				"maximum bytes configured":         int64(1_000_000_000),
				"tracked dirty bytes in the cache": int64(5_000_000),
			},
		},
		"network": bson.M{
			"bytesIn":     int64(1111),
			"bytesOut":    int64(2222),
			"numRequests": int64(333),
		},
		"opcounters": bson.M{
			"insert":  int64(1),
			"query":   int64(2),
			"update":  int64(3),
			"delete":  int64(4),
			"getmore": int64(5),
			"command": int64(6),
		},
		"extra_info": bson.M{
			"note":           "fields vary by platform",
			"user_time_us":   int64(16_000_000),
			"system_time_us": int64(4_000_000),
		},
	}
}

func TestApplyMongoServerStatusGroups(t *testing.T) {
	doc := decodeServerStatus(t, fullServerStatusFixture())

	t.Run("all groups", func(t *testing.T) {
		m := &mongoMetrics{}
		applyMongoServerStatus(m, doc, models.MongoDBMonitorConfig{
			CollectConnections: true, CollectCache: true, CollectMemory: true, CollectNetwork: true, CollectCPU: true,
		})
		if m.UptimeSeconds == nil || *m.UptimeSeconds != 12345 {
			t.Fatalf("uptime = %v, want 12345", m.UptimeSeconds)
		}
		if m.ConnectedClients == nil || *m.ConnectedClients != 42 {
			t.Fatalf("connected_clients = %v, want 42", m.ConnectedClients)
		}
		if m.ConnectionsAvailable == nil || *m.ConnectionsAvailable != 958 {
			t.Fatalf("connections_available = %v, want 958", m.ConnectionsAvailable)
		}
		if m.UsedMemoryBytes == nil || *m.UsedMemoryBytes != 512*1024*1024 {
			t.Fatalf("used_memory_bytes = %v, want 512MiB", m.UsedMemoryBytes)
		}
		if m.MemVirtualBytes == nil || *m.MemVirtualBytes != 2048*1024*1024 {
			t.Fatalf("mem_virtual_bytes = %v, want 2048MiB", m.MemVirtualBytes)
		}
		if m.CacheUsedBytes == nil || *m.CacheUsedBytes != 100_000_000 {
			t.Fatalf("cache_used_bytes = %v, want 100000000 (space-keyed BSON tag)", m.CacheUsedBytes)
		}
		if m.CacheMaxBytes == nil || *m.CacheMaxBytes != 1_000_000_000 {
			t.Fatalf("cache_max_bytes = %v", m.CacheMaxBytes)
		}
		if m.CacheDirtyBytes == nil || *m.CacheDirtyBytes != 5_000_000 {
			t.Fatalf("cache_dirty_bytes = %v", m.CacheDirtyBytes)
		}
		if m.NetworkBytesIn == nil || *m.NetworkBytesIn != 1111 || m.NetworkRequests == nil || *m.NetworkRequests != 333 {
			t.Fatalf("network = %v/%v", m.NetworkBytesIn, m.NetworkRequests)
		}
		if len(m.Opcounters) != 6 || m.Opcounters["command"] != 6 {
			t.Fatalf("opcounters = %v", m.Opcounters)
		}
		if m.CPUUserMicros == nil || *m.CPUUserMicros != 16_000_000 || m.CPUSystemMicros == nil || *m.CPUSystemMicros != 4_000_000 {
			t.Fatalf("cpu = %v/%v", m.CPUUserMicros, m.CPUSystemMicros)
		}
	})

	t.Run("only enabled groups populate", func(t *testing.T) {
		m := &mongoMetrics{}
		applyMongoServerStatus(m, doc, models.MongoDBMonitorConfig{CollectCache: true})
		if m.CacheUsedBytes == nil {
			t.Fatal("cache group enabled but not populated")
		}
		if m.ConnectedClients != nil || m.UsedMemoryBytes != nil || m.NetworkBytesIn != nil || m.Opcounters != nil || m.CPUUserMicros != nil {
			t.Fatal("disabled groups must stay empty")
		}
		if m.UptimeSeconds == nil {
			t.Fatal("uptime is always set once serverStatus ran")
		}
	})

	t.Run("absent sections emit nothing", func(t *testing.T) {
		bare := decodeServerStatus(t, bson.M{"uptime": 7.0})
		m := &mongoMetrics{}
		applyMongoServerStatus(m, bare, models.MongoDBMonitorConfig{
			CollectConnections: true, CollectCache: true, CollectMemory: true, CollectNetwork: true, CollectCPU: true,
		})
		if m.ConnectedClients != nil || m.CacheUsedBytes != nil || m.UsedMemoryBytes != nil || m.NetworkBytesIn != nil || m.CPUUserMicros != nil {
			t.Fatal("nil sections must not populate fields")
		}
	})

	t.Run("platform without cpu times emits nothing", func(t *testing.T) {
		fixture := fullServerStatusFixture()
		fixture["extra_info"] = bson.M{"note": "fields vary by platform", "page_faults": int64(12)}
		m := &mongoMetrics{}
		applyMongoServerStatus(m, decodeServerStatus(t, fixture), models.MongoDBMonitorConfig{CollectCPU: true})
		if m.CPUUserMicros != nil || m.CPUSystemMicros != nil {
			t.Fatal("zero cpu times mean the platform omits them; must stay nil")
		}
	})

	t.Run("zero resident memory skipped", func(t *testing.T) {
		fixture := fullServerStatusFixture()
		fixture["mem"] = bson.M{"resident": int32(0), "virtual": int32(100)}
		m := &mongoMetrics{}
		applyMongoServerStatus(m, decodeServerStatus(t, fixture), models.MongoDBMonitorConfig{CollectMemory: true})
		if m.UsedMemoryBytes != nil {
			t.Fatal("resident=0 means not reported; must stay nil")
		}
		if m.MemVirtualBytes == nil {
			t.Fatal("virtual memory should still be reported")
		}
	})
}

func replMember(name string, state int32, stateStr string, health float64, optime time.Time) bson.M {
	return bson.M{"name": name, "state": state, "stateStr": stateStr, "health": health, "optimeDate": optime}
}

func decodeReplStatus(t *testing.T, fixture bson.M) mongoReplSetStatusDoc {
	t.Helper()
	raw, err := bson.Marshal(fixture)
	if err != nil {
		t.Fatalf("marshal fixture: %v", err)
	}
	var doc mongoReplSetStatusDoc
	if err := bson.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("unmarshal fixture: %v", err)
	}
	return doc
}

func TestBuildMongoReplicationMetrics(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)

	t.Run("healthy set with lag", func(t *testing.T) {
		repl := buildMongoReplicationMetrics(decodeReplStatus(t, bson.M{
			"set": "rs0",
			"members": bson.A{
				replMember("a:27017", 1, "PRIMARY", 1, now),
				replMember("b:27017", 2, "SECONDARY", 1, now.Add(-3*time.Second)),
				replMember("c:27017", 2, "SECONDARY", 1, now.Add(-9*time.Second)),
			},
		}))
		if repl.Set != "rs0" || repl.Primary != "a:27017" {
			t.Fatalf("set/primary = %q/%q", repl.Set, repl.Primary)
		}
		if repl.MembersTotal != 3 || repl.MembersHealthy != 3 {
			t.Fatalf("members = %d/%d", repl.MembersHealthy, repl.MembersTotal)
		}
		if repl.MaxLagSeconds == nil || *repl.MaxLagSeconds != 9 {
			t.Fatalf("max lag = %v, want 9", repl.MaxLagSeconds)
		}
		if repl.Members[1].LagSeconds == nil || *repl.Members[1].LagSeconds != 3 {
			t.Fatalf("member b lag = %v, want 3", repl.Members[1].LagSeconds)
		}
		if repl.Members[0].LagSeconds != nil {
			t.Fatal("primary must have no lag")
		}
	})

	t.Run("no primary means undefined lag", func(t *testing.T) {
		repl := buildMongoReplicationMetrics(decodeReplStatus(t, bson.M{
			"set": "rs0",
			"members": bson.A{
				replMember("a:27017", 2, "SECONDARY", 1, now),
				replMember("b:27017", 2, "SECONDARY", 1, now),
			},
		}))
		if repl.Primary != "" || repl.MaxLagSeconds != nil {
			t.Fatalf("primary=%q lag=%v; want empty/nil", repl.Primary, repl.MaxLagSeconds)
		}
	})

	t.Run("primary only means undefined lag", func(t *testing.T) {
		repl := buildMongoReplicationMetrics(decodeReplStatus(t, bson.M{
			"set":     "rs0",
			"members": bson.A{replMember("a:27017", 1, "PRIMARY", 1, now)},
		}))
		if repl.MaxLagSeconds != nil {
			t.Fatalf("lag = %v, want nil with no secondaries", repl.MaxLagSeconds)
		}
	})

	t.Run("negative skew clamps to zero", func(t *testing.T) {
		repl := buildMongoReplicationMetrics(decodeReplStatus(t, bson.M{
			"set": "rs0",
			"members": bson.A{
				replMember("a:27017", 1, "PRIMARY", 1, now),
				replMember("b:27017", 2, "SECONDARY", 1, now.Add(2*time.Second)),
			},
		}))
		if repl.MaxLagSeconds == nil || *repl.MaxLagSeconds != 0 {
			t.Fatalf("lag = %v, want clamped 0", repl.MaxLagSeconds)
		}
	})

	t.Run("arbiter counted but not lagged, unhealthy counted", func(t *testing.T) {
		repl := buildMongoReplicationMetrics(decodeReplStatus(t, bson.M{
			"set": "rs0",
			"members": bson.A{
				replMember("a:27017", 1, "PRIMARY", 1, now),
				replMember("b:27017", 7, "ARBITER", 1, time.Time{}),
				replMember("c:27017", 8, "(not reachable/healthy)", 0, time.Time{}),
			},
		}))
		if repl.MembersTotal != 3 || repl.MembersHealthy != 2 {
			t.Fatalf("members = %d/%d, want 2/3", repl.MembersHealthy, repl.MembersTotal)
		}
		if repl.Members[1].LagSeconds != nil {
			t.Fatal("arbiter must have no lag")
		}
		if repl.MaxLagSeconds != nil {
			t.Fatalf("no secondaries: lag = %v, want nil", repl.MaxLagSeconds)
		}
	})
}

func TestMongoCmdUnavailable(t *testing.T) {
	cases := []struct {
		name       string
		err        error
		wantReason string
	}{
		{"unauthorized code", mongo.CommandError{Code: 13, Message: "not authorized on admin"}, "unauthorized"},
		{"standalone code", mongo.CommandError{Code: 76, Message: "not running with --replSet"}, "not_replica_set"},
		{"unauthorized text fallback", errors.New("(Unauthorized) not authorized on admin to execute command"), "unauthorized"},
		{"standalone text fallback", errors.New("NoReplicationEnabled: not running with --replSet"), "not_replica_set"},
		{"generic error", errors.New("connection pool cleared"), "error"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := mongoCmdUnavailable("repl_set_status", tc.err)
			if got.Reason != tc.wantReason {
				t.Fatalf("reason = %q, want %q", got.Reason, tc.wantReason)
			}
			if got.Check != "repl_set_status" {
				t.Fatalf("check = %q", got.Check)
			}
			if tc.wantReason == "error" && got.Message == "" {
				t.Fatal("generic errors must carry a message")
			}
			if tc.wantReason != "error" && got.Message != "" {
				t.Fatalf("classified reasons carry no message, got %q", got.Message)
			}
		})
	}
}

// TestMongoMetricsJSONShape guards the embedding contract: the shared
// dbMetrics fields and the cluster fields marshal as siblings under the
// "mongodb" key, exactly the envelope the existing UI readers consume.
func TestMongoMetricsJSONShape(t *testing.T) {
	used := int64(7)
	lag := int64(2)
	m := &mongoMetrics{
		dbMetrics:      dbMetrics{ServerVersion: "7.0.5", Role: "primary", ReplicaSet: "rs0"},
		CacheUsedBytes: &used,
		Replication:    &mongoReplicationMetrics{Set: "rs0", Primary: "a:27017", MembersTotal: 3, MembersHealthy: 3, MaxLagSeconds: &lag},
	}
	result := dbLatencyResultEnvelope("mongodb", 12, nil, nil, &m.dbMetrics, m)
	if result.Status != "success" {
		t.Fatalf("status = %q", result.Status)
	}
	var envelope map[string]map[string]json.RawMessage
	if err := json.Unmarshal(result.MetricsData, &envelope); err != nil {
		t.Fatalf("unmarshal envelope: %v", err)
	}
	inner, ok := envelope["mongodb"]
	if !ok {
		t.Fatalf("envelope keys = %v, want mongodb", envelope)
	}
	for _, key := range []string{"server_version", "role", "replica_set", "cache_used_bytes", "replication"} {
		if _, ok := inner[key]; !ok {
			t.Fatalf("missing flat key %q in %v", key, inner)
		}
	}
	if _, ok := inner["dbMetrics"]; ok {
		t.Fatal("embedded dbMetrics must marshal inline, not nested")
	}
}

func TestMongoFinishResult(t *testing.T) {
	i64 := func(v int64) *int64 { return &v }
	replWithLag := func(lag *int64, primary string) *mongoReplicationMetrics {
		return &mongoReplicationMetrics{Set: "rs0", Primary: primary, MembersTotal: 2, MembersHealthy: 2, MaxLagSeconds: lag}
	}

	t.Run("no thresholds: unavailable stays success", func(t *testing.T) {
		m := &mongoMetrics{Unavailable: []mongoUnavailableCheck{{Check: "repl_set_status", Reason: "unauthorized"}}}
		result := mongoFinishResult(models.MongoDBMonitorConfig{CollectReplication: true}, m, 10)
		if result.Status != "success" {
			t.Fatalf("status = %q, want success (no hard assertion)", result.Status)
		}
	})

	t.Run("lag over max fails", func(t *testing.T) {
		m := &mongoMetrics{Replication: replWithLag(i64(30), "a:27017")}
		result := mongoFinishResult(models.MongoDBMonitorConfig{CollectReplication: true, MaxReplicationLagSeconds: i64(10)}, m, 10)
		if result.Status != "failure" || result.ErrorMessage == nil || *result.ErrorMessage != "replication_lag: 30s > 10s" {
			t.Fatalf("got %q / %v", result.Status, result.ErrorMessage)
		}
		if result.MetricsData == nil {
			t.Fatal("failure must keep the metrics envelope")
		}
	})

	t.Run("lag under max succeeds", func(t *testing.T) {
		m := &mongoMetrics{Replication: replWithLag(i64(5), "a:27017")}
		result := mongoFinishResult(models.MongoDBMonitorConfig{CollectReplication: true, MaxReplicationLagSeconds: i64(10)}, m, 10)
		if result.Status != "success" {
			t.Fatalf("status = %q", result.Status)
		}
	})

	t.Run("warn only annotates", func(t *testing.T) {
		m := &mongoMetrics{Replication: replWithLag(i64(5), "a:27017")}
		result := mongoFinishResult(models.MongoDBMonitorConfig{CollectReplication: true, WarnReplicationLagSeconds: i64(3)}, m, 10)
		if result.Status != "success" {
			t.Fatalf("status = %q, want success (warn never fails)", result.Status)
		}
		if m.ReplicationLagWarnSeconds == nil || *m.ReplicationLagWarnSeconds != 3 {
			t.Fatalf("warn annotation = %v, want 3", m.ReplicationLagWarnSeconds)
		}
		var envelope map[string]map[string]json.RawMessage
		if err := json.Unmarshal(result.MetricsData, &envelope); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		if _, ok := envelope["mongodb"]["replication_lag_warn_seconds"]; !ok {
			t.Fatal("warn annotation missing from marshaled envelope")
		}
	})

	failClosedCases := []struct {
		name       string
		m          *mongoMetrics
		wantReason string
	}{
		{"unauthorized", &mongoMetrics{Unavailable: []mongoUnavailableCheck{{Check: "repl_set_status", Reason: "unauthorized"}}}, "replication_lag: cannot evaluate (unauthorized)"},
		{"standalone", &mongoMetrics{Unavailable: []mongoUnavailableCheck{{Check: "repl_set_status", Reason: "not_replica_set"}}}, "replication_lag: cannot evaluate (not_replica_set)"},
		{"command error", &mongoMetrics{Unavailable: []mongoUnavailableCheck{{Check: "repl_set_status", Reason: "error", Message: "boom"}}}, "replication_lag: cannot evaluate (error: boom)"},
		{"no primary", &mongoMetrics{Replication: replWithLag(nil, "")}, "replication_lag: cannot evaluate (no primary)"},
		{"no secondaries", &mongoMetrics{Replication: replWithLag(nil, "a:27017")}, "replication_lag: cannot evaluate (no secondaries)"},
	}
	for _, tc := range failClosedCases {
		t.Run("fail closed: "+tc.name, func(t *testing.T) {
			cfg := models.MongoDBMonitorConfig{CollectReplication: true, MaxReplicationLagSeconds: i64(10)}
			result := mongoFinishResult(cfg, tc.m, 10)
			if result.Status != "failure" {
				t.Fatalf("status = %q, want failure (configured max lag must fail closed)", result.Status)
			}
			if result.ErrorMessage == nil || *result.ErrorMessage != tc.wantReason {
				t.Fatalf("error = %v, want %q", result.ErrorMessage, tc.wantReason)
			}
		})
	}

	t.Run("latency failure takes precedence", func(t *testing.T) {
		m := &mongoMetrics{Replication: replWithLag(i64(30), "a:27017")}
		cfg := models.MongoDBMonitorConfig{CollectReplication: true, MaxReplicationLagSeconds: i64(10), MaxLatencyMs: i64(5)}
		result := mongoFinishResult(cfg, m, 50)
		if result.Status != "failure" || result.ErrorMessage == nil || *result.ErrorMessage != fmt.Sprintf("latency: %dms > %dms", 50, 5) {
			t.Fatalf("got %q / %v, want latency failure first", result.Status, result.ErrorMessage)
		}
	})
}
