package models

import (
	"time"

	"github.com/google/uuid"

	"github.com/yassinebenameur/probara/shared/ai"
)

// AI label subject types (ai_labels.subject_type).
const (
	AILabelSubjectIncidentAnalysis = "incident_analysis"
	AILabelSubjectDependencyPair   = "dependency_pair"
)

// AILabel is an operator's verdict on one AI subject, as returned by the label
// routes and embedded in AILabelContext. Answers are keyed by the opaque
// decision id of the stored snapshot.
type AILabel struct {
	SubjectType      string                    `json:"subject_type"`
	SubjectID        *uuid.UUID                `json:"subject_id,omitempty"`
	Verdict          string                    `json:"verdict"`
	IncidentLabel    string                    `json:"incident_label,omitempty"`
	Answers          map[string]ai.LabelAnswer `json:"answers"`
	CatalogueVersion string                    `json:"catalogue_version"`
	ModelVersion     string                    `json:"model_version,omitempty"`
	LabeledBy        *uuid.UUID                `json:"labeled_by"` // null for API keys
	LabeledAt        time.Time                 `json:"labeled_at"`
}

// AILabelChoice is one selectable catalogue answer.
type AILabelChoice struct {
	Key   string `json:"key"`
	Title string `json:"title"`
}

// AILabelDecision is one per-monitor decision of a labelable analysis.
// MonitorName is null when the monitor has been purged.
type AILabelDecision struct {
	ID          string     `json:"id"`
	MonitorID   *uuid.UUID `json:"monitor_id"`
	MonitorName *string    `json:"monitor_name"`
	Predicted   string     `json:"predicted"`
}

// AILabelContext is what the incident panel needs to render the label
// control. Present only on ready analyses with a decision snapshot.
type AILabelContext struct {
	CatalogueVersion string            `json:"catalogue_version"`
	Predicted        string            `json:"predicted"`
	Choices          []AILabelChoice   `json:"choices"`
	Decisions        []AILabelDecision `json:"decisions"`
	Label            *AILabel          `json:"label"`
}

// IncidentAILabelRequest is the body of POST /incidents/{id}/ai-analysis/label.
// AnalysisID defaults to the incident's newest ready analysis.
type IncidentAILabelRequest struct {
	AnalysisID    string            `json:"analysis_id,omitempty"`
	Verdict       string            `json:"verdict"`
	IncidentLabel string            `json:"incident_label,omitempty"`
	Answers       map[string]string `json:"answers,omitempty"`
}

// DependencyLabelRequest is the body of POST
// /monitors/dependency-suggestions/label: MonitorID depends on DependsOnID
// as suggested.
type DependencyLabelRequest struct {
	MonitorID   string `json:"monitor_id"`
	DependsOnID string `json:"depends_on_id"`
	Verdict     string `json:"verdict"`
	Corrected   string `json:"corrected,omitempty"`
}

// AILabelExportFilter is the optional body of POST /ai-labels/export. The
// window is half-open [Since, Until) on the label's last update.
type AILabelExportFilter struct {
	Since        *time.Time `json:"since,omitempty"`
	Until        *time.Time `json:"until,omitempty"`
	SubjectTypes []string   `json:"subject_types,omitempty"`
}
