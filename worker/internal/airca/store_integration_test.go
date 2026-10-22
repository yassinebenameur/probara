//go:build !unit

package airca

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/yassinebenameur/probara/shared/ai"
	"github.com/yassinebenameur/probara/shared/config"
	"github.com/yassinebenameur/probara/shared/logger"
	"github.com/yassinebenameur/probara/shared/queue"
)

// snapshotAnalyzer returns a fixed result, as the local analyzer does after a
// successful model exchange.
type snapshotAnalyzer struct{ result ai.AnalysisResult }

func (a snapshotAnalyzer) Analyze(context.Context, ai.AnalysisInput) (ai.AnalysisResult, error) {
	return a.result, nil
}

func (f *analysisFixture) snapshot(id any) []byte {
	f.t.Helper()
	var b []byte
	if err := f.dbc.QueryRowContext(f.ctx, `SELECT decision_snapshot FROM incident_ai_analyses WHERE id=$1`, id).Scan(&b); err != nil {
		f.t.Fatal(err)
	}
	return b
}

func TestMarkReadyPersistsDecisionSnapshot(t *testing.T) {
	f := newAnalysisFixture(t)
	job := f.pending()
	snap := &ai.DecisionSnapshot{
		V: ai.DecisionSnapshotVersion, Catalogue: ai.CatalogueVersion,
		Model:      ai.ModelIdentity{Engine: "laya-cpu", Version: "probara-laya-v1", SHA256: strings.Repeat("a", 64), Catalogue: ai.CatalogueVersion},
		Thresholds: map[string]float64{"rca": .6}, Hypothesis: "dns_failure", Score: .9,
		Decisions: []ai.SnapshotDecision{{
			ID: "d1", Task: "rca", MonitorRef: "1f2e3d4c-5b6a-4978-8695-a4b3c2d1e0f9",
			Evidence: json.RawMessage(`{"checks":10,"failures":4,"dns_failures":4}`), Choices: ai.Choices("rca"),
			Probabilities: map[string]float64{"dns_failure": .9, "insufficient_evidence": .1}, Predicted: "dns_failure", Final: "dns_failure",
		}},
	}
	result := ai.AnalysisResult{Summary: "ok", Confidence: "high", Metadata: ai.Metadata{Engine: "laya-cpu", Outcome: "supported"}, Snapshot: snap}
	c := New(&config.WorkerConfig{LocalAI: ai.Config{Timeout: 10 * time.Second}}, logger.New("test", "error"), f.dbc, nil, snapshotAnalyzer{result})
	if err := c.handle(f.ctx, &queue.Message{Data: payload(t, job), NumDelivered: 1}); err != nil {
		t.Fatal(err)
	}
	if status, _ := f.status(job.AnalysisID); status != "ready" {
		t.Fatalf("status = %s", status)
	}
	var got ai.DecisionSnapshot
	if err := json.Unmarshal(f.snapshot(job.AnalysisID), &got); err != nil {
		t.Fatal(err)
	}
	// JSONB normalizes whitespace inside RawMessage; compare evidence semantically.
	var gotEv, wantEv map[string]any
	_ = json.Unmarshal(got.Decisions[0].Evidence, &gotEv)
	_ = json.Unmarshal(snap.Decisions[0].Evidence, &wantEv)
	if !reflect.DeepEqual(gotEv, wantEv) {
		t.Fatalf("evidence %s", got.Decisions[0].Evidence)
	}
	got.Decisions[0].Evidence, snap.Decisions[0].Evidence = nil, nil
	if !reflect.DeepEqual(&got, snap) {
		t.Fatalf("snapshot round trip:\n got %+v\nwant %+v", got, *snap)
	}
}

func TestMarkReadyNilSnapshotWritesNull(t *testing.T) {
	f := newAnalysisFixture(t)
	job := f.pending()
	c := New(&config.WorkerConfig{LocalAI: ai.Config{Timeout: 10 * time.Second}}, logger.New("test", "error"), f.dbc, nil, &countingAnalyzer{})
	updated, err := c.markReady(f.ctx, job, ai.AnalysisResult{Summary: "no decisions"})
	if err != nil || !updated {
		t.Fatalf("updated=%t err=%v", updated, err)
	}
	if b := f.snapshot(job.AnalysisID); b != nil {
		t.Fatalf("snapshot = %s, want NULL", b)
	}
}

func TestMarkFailedLeavesSnapshotNull(t *testing.T) {
	f := newAnalysisFixture(t)
	job := f.pending()
	c := New(&config.WorkerConfig{LocalAI: ai.Config{Timeout: 10 * time.Second}}, logger.New("test", "error"), f.dbc, nil, &countingAnalyzer{})
	updated, err := c.markFailed(f.ctx, job, "boom")
	if err != nil || !updated {
		t.Fatalf("updated=%t err=%v", updated, err)
	}
	if b := f.snapshot(job.AnalysisID); b != nil {
		t.Fatalf("snapshot = %s, want NULL", b)
	}
	// A late ready write never lands on the failed row.
	if updated, err := c.markReady(f.ctx, job, ai.AnalysisResult{Snapshot: &ai.DecisionSnapshot{V: 1}}); err != nil || updated {
		t.Fatalf("updated=%t err=%v", updated, err)
	}
	if b := f.snapshot(job.AnalysisID); b != nil {
		t.Fatalf("snapshot = %s, want NULL", b)
	}
}
