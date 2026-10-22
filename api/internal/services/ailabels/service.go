// Package ailabels stores operator verdicts on local AI decisions and exports
// them as training rows.
//
// A label never accepts client evidence: the request half is copied
// server-side from the decision snapshot the worker persisted
// (incident_ai_analyses.decision_snapshot, alert_triage.decision_snapshot) or
// recomputed for a dependency pair through depsuggest.PairDecision. The export row format lives in
// shared/ai (WriteLabelExport). ai_labels only cascades on tenant delete, so
// monitor purges and incident deletion keep labels exportable.
package ailabels

import (
	"bytes"
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/lib/pq"

	"github.com/yassinebenameur/probara/api/internal/models"
	"github.com/yassinebenameur/probara/api/internal/services/depsuggest"
	"github.com/yassinebenameur/probara/shared/ai"
	shareddb "github.com/yassinebenameur/probara/shared/db"
)

// Export bounds; a larger window must be narrowed. The row cap is what bounds
// memory: one incident label expands to a row per snapshot decision (up to
// ~100, the worker's affected-monitor limit) and the export is buffered for
// its count headers, so a label cap alone would allow ~500k buffered rows.
const (
	DefaultExportCap    = 5000  // labels
	DefaultExportRowCap = 20000 // decisions, i.e. training rows before skips
)

// Errors the handlers map onto HTTP statuses.
var (
	ErrNotFound             = errors.New("not found")
	ErrAnalysisNotReady     = errors.New("analysis not ready")
	ErrAnalysisNotLabelable = errors.New("analysis not labelable")
	ErrTriageNotReady       = errors.New("triage not ready")
	ErrTriageNotLabelable   = errors.New("triage not labelable")
	ErrPairUnavailable      = errors.New("pair evidence unavailable")
	ErrExportTooLarge       = errors.New("export too large")
)

// ValidationError is a malformed request (400 validation_error). Labels that
// contradict the snapshot come back as *ai.LabelValidationError instead.
type ValidationError struct{ Msg string }

func (e *ValidationError) Error() string { return e.Msg }

// pairDecider recomputes dependency pair evidence (*depsuggest.Service).
type pairDecider interface {
	PairDecision(ctx context.Context, tenantID, monitorID, dependsOnID uuid.UUID) (ai.SnapshotDecision, error)
}

// Querier is the read access LoadLabelContext needs.
type Querier interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// Service stores and exports AI labels.
type Service struct {
	db           *shareddb.Client
	pairs        pairDecider
	exportCap    int
	exportRowCap int
}

// NewService wires the database and the dependency pair recomputation.
func NewService(db *shareddb.Client, pairs pairDecider) *Service {
	return &Service{db: db, pairs: pairs, exportCap: DefaultExportCap, exportRowCap: DefaultExportRowCap}
}

// LabelIncidentAnalysis records the operator verdict on an incident analysis.
// The default target is the incident's newest ready analysis.
func (s *Service) LabelIncidentAnalysis(ctx context.Context, tenantID, incidentID uuid.UUID, req models.IncidentAILabelRequest, labeledBy *uuid.UUID) (*models.AILabel, error) {
	if req.Verdict != ai.LabelVerdictConfirmed && req.Verdict != ai.LabelVerdictCorrected {
		return nil, &ValidationError{Msg: `verdict must be "confirmed" or "corrected"`}
	}
	var analysisID *uuid.UUID
	if req.AnalysisID != "" {
		id, err := uuid.Parse(req.AnalysisID)
		if err != nil {
			return nil, &ValidationError{Msg: "analysis_id must be a UUID"}
		}
		analysisID = &id
	}

	var exists bool
	if err := s.db.QueryRowContext(ctx,
		`SELECT EXISTS(SELECT 1 FROM incidents WHERE id=$1 AND tenant_id=$2)`, incidentID, tenantID).Scan(&exists); err != nil {
		return nil, fmt.Errorf("check incident exists: %w", err)
	}
	if !exists {
		return nil, fmt.Errorf("incident %w", ErrNotFound)
	}

	var (
		id       uuid.UUID
		status   string
		snapshot []byte
		err      error
	)
	if analysisID != nil {
		err = s.db.QueryRowContext(ctx, `
			SELECT id, status, decision_snapshot FROM incident_ai_analyses
			WHERE incident_id=$1 AND tenant_id=$2 AND id=$3`, incidentID, tenantID, *analysisID).Scan(&id, &status, &snapshot)
		if errors.Is(err, sql.ErrNoRows) {
			return nil, fmt.Errorf("analysis %w", ErrNotFound)
		}
	} else {
		err = s.db.QueryRowContext(ctx, `
			SELECT id, status, decision_snapshot FROM incident_ai_analyses
			WHERE incident_id=$1 AND tenant_id=$2 AND status='ready'
			ORDER BY created_at DESC LIMIT 1`, incidentID, tenantID).Scan(&id, &status, &snapshot)
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrAnalysisNotReady
		}
	}
	if err != nil {
		return nil, fmt.Errorf("load analysis: %w", err)
	}
	if status != string(models.IncidentAIAnalysisStatusReady) {
		return nil, ErrAnalysisNotReady
	}
	snap, ok := decodeSnapshot(snapshot)
	if !ok {
		return nil, ErrAnalysisNotLabelable
	}
	answers, incident, err := ai.ResolveIncidentAnswers(snap, req.Verdict, req.IncidentLabel, req.Answers)
	if errors.Is(err, ai.ErrSnapshotNotLabelable) {
		return nil, ErrAnalysisNotLabelable
	}
	if err != nil {
		return nil, err
	}

	label := &models.AILabel{
		SubjectType:      models.AILabelSubjectIncidentAnalysis,
		SubjectID:        &id,
		Verdict:          req.Verdict,
		IncidentLabel:    incident,
		Answers:          answers,
		CatalogueVersion: snap.Catalogue,
		ModelVersion:     snap.Model.Version,
		LabeledBy:        labeledBy,
	}
	predicted := ai.SnapshotPredictions(snap)
	if err := s.upsert(ctx, tenantID, id.String(), &id, &incidentID, label, snap.WithoutPredictions(), &predicted, snap.Model); err != nil {
		return nil, err
	}
	return label, nil
}

// LabelAlertTriage records the operator verdict on an alert's automatic
// triage. The request half is the triage's stored decision snapshot; req.Label
// is the alert-level key and lands in AILabel.IncidentLabel. The label joins
// the export group of the earliest incident linking the alert, so triage and
// incident labels of one outage share a split group.
func (s *Service) LabelAlertTriage(ctx context.Context, tenantID, alertID uuid.UUID, req models.AlertTriageLabelRequest, labeledBy *uuid.UUID) (*models.AILabel, error) {
	if req.Verdict != ai.LabelVerdictConfirmed && req.Verdict != ai.LabelVerdictCorrected {
		return nil, &ValidationError{Msg: `verdict must be "confirmed" or "corrected"`}
	}
	var (
		triageID uuid.NullUUID
		status   sql.NullString
		snapshot []byte
	)
	err := s.db.QueryRowContext(ctx, `
		SELECT tr.id, tr.status, tr.decision_snapshot FROM alerts a
		LEFT JOIN alert_triage tr ON tr.alert_id = a.id AND tr.tenant_id = a.tenant_id
		WHERE a.id = $1 AND a.tenant_id = $2`, alertID, tenantID).Scan(&triageID, &status, &snapshot)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("alert %w", ErrNotFound)
	}
	if err != nil {
		return nil, fmt.Errorf("load alert triage: %w", err)
	}
	if !triageID.Valid || status.String != models.AlertTriageStatusReady {
		return nil, ErrTriageNotReady
	}
	snap, ok := decodeSnapshot(snapshot)
	if !ok {
		return nil, ErrTriageNotLabelable
	}
	answers, alertLabel, err := ai.ResolveIncidentAnswers(snap, req.Verdict, req.Label, req.Answers)
	if errors.Is(err, ai.ErrSnapshotNotLabelable) {
		return nil, ErrTriageNotLabelable
	}
	if err != nil {
		return nil, triageLabelError(err)
	}

	var incidentID uuid.NullUUID
	err = s.db.QueryRowContext(ctx, `
		SELECT ia.incident_id FROM incident_alerts ia
		JOIN incidents i ON i.id = ia.incident_id AND i.tenant_id = $2
		WHERE ia.alert_id = $1
		ORDER BY ia.created_at, ia.incident_id LIMIT 1`, alertID, tenantID).Scan(&incidentID)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("load alert incident: %w", err)
	}
	var group *uuid.UUID
	if incidentID.Valid {
		group = &incidentID.UUID
	}

	id := triageID.UUID
	label := &models.AILabel{
		SubjectType:      models.AILabelSubjectAlertTriage,
		SubjectID:        &id,
		Verdict:          req.Verdict,
		IncidentLabel:    alertLabel,
		Answers:          answers,
		CatalogueVersion: snap.Catalogue,
		ModelVersion:     snap.Model.Version,
		LabeledBy:        labeledBy,
	}
	predicted := ai.SnapshotPredictions(snap)
	if err := s.upsert(ctx, tenantID, id.String(), &id, group, label, snap.WithoutPredictions(), &predicted, snap.Model); err != nil {
		return nil, err
	}
	return label, nil
}

// triageLabelError rewrites ResolveIncidentAnswers' validation messages for
// the triage endpoint: they name the incident endpoint's incident_label field
// and "analysis", but the triage body field is label. The wording is pinned by
// TestTriageLabelErrorNamesTriageField, so a reworded shared/ai message fails
// there instead of leaking incident wording to triage clients.
func triageLabelError(err error) error {
	var ve *ai.LabelValidationError
	if !errors.As(err, &ve) {
		return err
	}
	msg := strings.NewReplacer(
		"incident_label", "label",
		"an analysis", "a triage",
		"this analysis", "this triage",
	).Replace(ve.Msg)
	return &ai.LabelValidationError{Msg: msg}
}

// LabelDependencySuggestion records the operator verdict on a dependency
// suggestion. Evidence is recomputed now through depsuggest, never taken from
// the request.
func (s *Service) LabelDependencySuggestion(ctx context.Context, tenantID uuid.UUID, req models.DependencyLabelRequest, labeledBy *uuid.UUID) (*models.AILabel, error) {
	monitorID, err1 := uuid.Parse(req.MonitorID)
	dependsOnID, err2 := uuid.Parse(req.DependsOnID)
	if err1 != nil || err2 != nil {
		return nil, &ValidationError{Msg: "monitor_id and depends_on_id must be UUIDs"}
	}
	if monitorID == dependsOnID {
		return nil, &ValidationError{Msg: "monitor_id and depends_on_id must differ"}
	}
	if err := ai.ValidateDependencyVerdict(req.Verdict, req.Corrected); err != nil {
		return nil, err
	}
	d, err := s.pairs.PairDecision(ctx, tenantID, monitorID, dependsOnID)
	switch {
	case errors.Is(err, depsuggest.ErrMonitorNotFound):
		return nil, fmt.Errorf("monitor %w", ErrNotFound)
	case errors.Is(err, depsuggest.ErrPairUnavailable):
		return nil, ErrPairUnavailable
	case err != nil:
		return nil, err
	}
	answer, err := ai.DependencyAnswer(d, monitorID.String(), req.Verdict, req.Corrected)
	if err != nil {
		return nil, err
	}
	lo, hi := monitorID.String(), dependsOnID.String()
	if lo > hi {
		lo, hi = hi, lo
	}
	request := ai.DecisionSnapshot{V: ai.DecisionSnapshotVersion, Catalogue: ai.CatalogueVersion, Decisions: []ai.SnapshotDecision{d}}
	label := &models.AILabel{
		SubjectType:      models.AILabelSubjectDependencyPair,
		Verdict:          req.Verdict,
		Answers:          map[string]ai.LabelAnswer{d.ID: answer},
		CatalogueVersion: request.Catalogue,
		LabeledBy:        labeledBy,
	}
	if err := s.upsert(ctx, tenantID, lo+":"+hi, nil, nil, label, request, nil, ai.ModelIdentity{}); err != nil {
		return nil, err
	}
	return label, nil
}

// upsert writes the label, latest wins: the row id and created_at survive a
// re-label, everything else is replaced. It fills label.LabeledAt.
func (s *Service) upsert(ctx context.Context, tenantID uuid.UUID, subjectKey string, subjectID, incidentID *uuid.UUID, label *models.AILabel, request ai.DecisionSnapshot, predicted *ai.LabelPredictions, model ai.ModelIdentity) error {
	answers, err := json.Marshal(label.Answers)
	if err != nil {
		return err
	}
	req, err := json.Marshal(request)
	if err != nil {
		return err
	}
	var pred []byte
	if predicted != nil {
		if pred, err = json.Marshal(predicted); err != nil {
			return err
		}
	}
	return s.db.QueryRowContext(ctx, `
		INSERT INTO ai_labels (tenant_id, subject_type, subject_key, subject_id, incident_id, verdict,
		                       incident_label, answers, request, predicted, catalogue_version,
		                       model_version, model_sha256, labeled_by)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14)
		ON CONFLICT (tenant_id, subject_type, subject_key) DO UPDATE SET
		    verdict = EXCLUDED.verdict,
		    incident_label = EXCLUDED.incident_label,
		    answers = EXCLUDED.answers,
		    request = EXCLUDED.request,
		    predicted = EXCLUDED.predicted,
		    catalogue_version = EXCLUDED.catalogue_version,
		    model_version = EXCLUDED.model_version,
		    model_sha256 = EXCLUDED.model_sha256,
		    labeled_by = EXCLUDED.labeled_by,
		    updated_at = NOW()
		RETURNING updated_at`,
		tenantID, label.SubjectType, subjectKey, subjectID, incidentID, label.Verdict,
		nullString(label.IncidentLabel), answers, req, nullBytes(pred), label.CatalogueVersion,
		nullString(model.Version), nullString(model.SHA256), label.LabeledBy,
	).Scan(&label.LabeledAt)
}

// LoadIncidentContext builds the label control context for a ready incident
// analysis (LoadLabelContext for incident_analysis).
func LoadIncidentContext(ctx context.Context, q Querier, tenantID, analysisID uuid.UUID, snapshot json.RawMessage) (*models.AILabelContext, error) {
	return LoadLabelContext(ctx, q, tenantID, models.AILabelSubjectIncidentAnalysis, analysisID, snapshot)
}

// LoadLabelContext builds the label control context for a snapshot-backed
// subject (an incident analysis or an alert triage). It returns nil (no
// control) when the snapshot carries no labelable decision. Monitor names are
// tenant-scoped and include soft-deleted monitors; a purged monitor has a
// null name.
func LoadLabelContext(ctx context.Context, q Querier, tenantID uuid.UUID, subjectType string, subjectID uuid.UUID, snapshot json.RawMessage) (*models.AILabelContext, error) {
	snap, ok := decodeSnapshot(snapshot)
	if !ok {
		return nil, nil
	}
	ids := []uuid.UUID{}
	for _, d := range snap.Decisions {
		if id, err := uuid.Parse(d.MonitorRef); err == nil {
			ids = append(ids, id)
		}
	}
	names := map[uuid.UUID]string{}
	if len(ids) > 0 {
		rows, err := q.QueryContext(ctx, `SELECT id, name FROM monitors WHERE tenant_id=$1 AND id=ANY($2)`, tenantID, pq.Array(ids))
		if err != nil {
			return nil, fmt.Errorf("label context monitors: %w", err)
		}
		defer rows.Close()
		for rows.Next() {
			var id uuid.UUID
			var name string
			if err := rows.Scan(&id, &name); err != nil {
				return nil, err
			}
			names[id] = name
		}
		if err := rows.Err(); err != nil {
			return nil, err
		}
	}

	out := &models.AILabelContext{CatalogueVersion: snap.Catalogue, Predicted: snap.Hypothesis, Choices: []models.AILabelChoice{}, Decisions: []models.AILabelDecision{}}
	// The snapshot's own choice keys, in the running catalogue's display order;
	// keys the running catalogue no longer has go last, titled by key.
	available := map[string]bool{}
	for k := range snap.Decisions[0].Choices {
		available[k] = true
	}
	for _, o := range ai.ChoiceOptions("rca") {
		if available[o.Key] {
			out.Choices = append(out.Choices, models.AILabelChoice{Key: o.Key, Title: o.Title})
			delete(available, o.Key)
		}
	}
	for _, k := range sortedKeys(available) {
		out.Choices = append(out.Choices, models.AILabelChoice{Key: k, Title: ai.ChoiceTitle("rca", k)})
	}
	for _, d := range snap.Decisions {
		dec := models.AILabelDecision{ID: d.ID, Predicted: d.Final}
		if id, err := uuid.Parse(d.MonitorRef); err == nil {
			dec.MonitorID = &id
			if name, ok := names[id]; ok {
				dec.MonitorName = &name
			}
		}
		out.Decisions = append(out.Decisions, dec)
	}
	label, err := loadLabel(ctx, q, tenantID, subjectType, subjectID.String())
	if err != nil {
		return nil, err
	}
	out.Label = label
	return out, nil
}

func loadLabel(ctx context.Context, q Querier, tenantID uuid.UUID, subjectType, subjectKey string) (*models.AILabel, error) {
	var (
		l             models.AILabel
		subjectID     uuid.NullUUID
		incidentLabel sql.NullString
		answers       []byte
		modelVersion  sql.NullString
		labeledBy     uuid.NullUUID
	)
	err := q.QueryRowContext(ctx, `
		SELECT subject_type, subject_id, verdict, incident_label, answers, catalogue_version,
		       model_version, labeled_by, updated_at
		FROM ai_labels WHERE tenant_id=$1 AND subject_type=$2 AND subject_key=$3`,
		tenantID, subjectType, subjectKey).Scan(&l.SubjectType, &subjectID, &l.Verdict, &incidentLabel, &answers,
		&l.CatalogueVersion, &modelVersion, &labeledBy, &l.LabeledAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("load label: %w", err)
	}
	if err := json.Unmarshal(answers, &l.Answers); err != nil {
		return nil, fmt.Errorf("decode label answers: %w", err)
	}
	if subjectID.Valid {
		id := subjectID.UUID
		l.SubjectID = &id
	}
	if labeledBy.Valid {
		id := labeledBy.UUID
		l.LabeledBy = &id
	}
	l.IncidentLabel = incidentLabel.String
	l.ModelVersion = modelVersion.String
	return &l, nil
}

var exportableSubjects = map[string]bool{
	models.AILabelSubjectIncidentAnalysis: true,
	models.AILabelSubjectAlertTriage:      true,
	models.AILabelSubjectDependencyPair:   true,
}

// ExportResult is a buffered JSONL export.
type ExportResult struct {
	Body    []byte
	Rows    int
	Skipped int
}

// Export renders the tenant's labels in the window as training rows (one per
// decision), ordered by last update. It creates the workspace salt on first
// use; the salt never leaves the database except as an HMAC key.
func (s *Service) Export(ctx context.Context, tenantID uuid.UUID, f models.AILabelExportFilter) (*ExportResult, error) {
	if f.Since != nil && f.Until != nil && !f.Since.Before(*f.Until) {
		return nil, &ValidationError{Msg: "since must be before until"}
	}
	subjects := f.SubjectTypes
	if len(subjects) == 0 {
		subjects = []string{models.AILabelSubjectIncidentAnalysis, models.AILabelSubjectAlertTriage, models.AILabelSubjectDependencyPair}
	}
	for _, t := range subjects {
		if !exportableSubjects[t] {
			return nil, &ValidationError{Msg: fmt.Sprintf("unknown subject type %q", t)}
		}
	}

	// Size the window before loading any request blob.
	const window = `
		FROM ai_labels
		WHERE tenant_id=$1
		  AND ($2::timestamptz IS NULL OR updated_at >= $2)
		  AND ($3::timestamptz IS NULL OR updated_at < $3)
		  AND subject_type = ANY($4)`
	var labelCount, decisionCount int64
	if err := s.db.QueryRowContext(ctx, `
		SELECT COUNT(*), COALESCE(SUM(CASE WHEN jsonb_typeof(request->'decisions') = 'array'
		                                   THEN jsonb_array_length(request->'decisions') ELSE 0 END), 0)`+window,
		tenantID, f.Since, f.Until, pq.Array(subjects)).Scan(&labelCount, &decisionCount); err != nil {
		return nil, fmt.Errorf("export labels size: %w", err)
	}
	if labelCount > int64(s.exportCap) || decisionCount > int64(s.exportRowCap) {
		return nil, ErrExportTooLarge
	}

	rows, err := s.db.QueryContext(ctx, `
		SELECT subject_type, subject_key, incident_id, verdict, answers, request, predicted, updated_at`+window+`
		ORDER BY updated_at, id
		LIMIT $5`,
		tenantID, f.Since, f.Until, pq.Array(subjects), s.exportCap+1)
	if err != nil {
		return nil, fmt.Errorf("export labels query: %w", err)
	}
	defer rows.Close()
	var records []ai.LabelRecord
	skipped, decisions := 0, 0
	for rows.Next() {
		var (
			rec        ai.LabelRecord
			incidentID uuid.NullUUID
			answers    []byte
			request    []byte
			predicted  []byte
		)
		if err := rows.Scan(&rec.SubjectType, &rec.SubjectKey, &incidentID, &rec.Verdict, &answers, &request, &predicted, &rec.LabeledAt); err != nil {
			return nil, err
		}
		if len(records)+skipped >= s.exportCap {
			return nil, ErrExportTooLarge
		}
		if json.Unmarshal(answers, &rec.Answers) != nil || json.Unmarshal(request, &rec.Request) != nil {
			skipped++
			continue
		}
		// Labels written after the size check still count against the row cap.
		if decisions += len(rec.Request.Decisions); decisions > s.exportRowCap {
			return nil, ErrExportTooLarge
		}
		if len(predicted) > 0 {
			var p ai.LabelPredictions
			if json.Unmarshal(predicted, &p) == nil {
				rec.Predicted = p.AsSnapshot()
			}
		}
		switch rec.SubjectType {
		case models.AILabelSubjectIncidentAnalysis:
			if !incidentID.Valid {
				skipped++
				continue
			}
			rec.GroupKey = "incident:" + incidentID.UUID.String()
		case models.AILabelSubjectAlertTriage:
			// A triage linked to an incident shares that incident's split
			// group; an unlinked one is its own group.
			if incidentID.Valid {
				rec.GroupKey = "incident:" + incidentID.UUID.String()
			} else {
				rec.GroupKey = "triage:" + rec.SubjectKey
			}
		default:
			rec.GroupKey = "pair:" + rec.SubjectKey
		}
		records = append(records, rec)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	salt, err := s.salt(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	n, skippedRows, err := ai.WriteLabelExport(&buf, salt, records)
	if err != nil {
		return nil, err
	}
	return &ExportResult{Body: buf.Bytes(), Rows: n, Skipped: skipped + skippedRows}, nil
}

// salt returns the workspace's export HMAC key, creating it on first use.
func (s *Service) salt(ctx context.Context, tenantID uuid.UUID) ([]byte, error) {
	fresh := make([]byte, 32)
	if _, err := rand.Read(fresh); err != nil {
		return nil, err
	}
	if _, err := s.db.ExecContext(ctx,
		`INSERT INTO ai_label_salts (tenant_id, salt) VALUES ($1, $2) ON CONFLICT (tenant_id) DO NOTHING`, tenantID, fresh); err != nil {
		return nil, fmt.Errorf("create label salt: %w", err)
	}
	var salt []byte
	if err := s.db.QueryRowContext(ctx, `SELECT salt FROM ai_label_salts WHERE tenant_id=$1`, tenantID).Scan(&salt); err != nil {
		return nil, fmt.Errorf("load label salt: %w", err)
	}
	return salt, nil
}

// decodeSnapshot parses a stored snapshot; ok=false when it is absent or has
// no rca decision to label.
func decodeSnapshot(b []byte) (ai.DecisionSnapshot, bool) {
	var snap ai.DecisionSnapshot
	if len(b) == 0 || json.Unmarshal(b, &snap) != nil || len(snap.Decisions) == 0 {
		return snap, false
	}
	for _, d := range snap.Decisions {
		if d.Task != "rca" || d.Final == "" {
			return snap, false
		}
	}
	return snap, true
}

// ExportFilename is the attachment name for an export made at t.
func ExportFilename(t time.Time) string {
	return "probara-ai-labels-" + t.UTC().Format("20060102") + ".jsonl"
}

func nullString(s string) any {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	return s
}

func nullBytes(b []byte) any {
	if len(b) == 0 {
		return nil
	}
	return b
}

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
