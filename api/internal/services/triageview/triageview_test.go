package triageview

import (
	"database/sql"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/yassinebenameur/probara/api/internal/models"
	"github.com/yassinebenameur/probara/shared/ai"
)

func ns(s string) sql.NullString { return sql.NullString{String: s, Valid: true} }

func TestEffectiveCauseLabelOverHypothesis(t *testing.T) {
	cases := []struct {
		name string
		in   CauseInput
		want Cause
	}{
		{"ready named hypothesis", CauseInput{Status: ns("ready"), Confidence: ns("high"), Hypothesis: ns("dns_failure")},
			Cause{Kind: models.SLACauseSuspected, Key: "dns_failure"}},
		{"ready insufficient evidence is inconclusive", CauseInput{Status: ns("ready"), Confidence: ns("high"), Hypothesis: ns(ai.RCAInsufficientEvidence)},
			Cause{Kind: models.SLACauseInconclusive, Key: models.SLACauseInconclusive}},
		{"ready key the catalogue cannot title is inconclusive", CauseInput{Status: ns("ready"), Confidence: ns("high"), Hypothesis: ns("retired_cause")},
			Cause{Kind: models.SLACauseInconclusive, Key: models.SLACauseInconclusive}},
		{"confirmed label", CauseInput{Status: ns("ready"), Confidence: ns("high"), Hypothesis: ns("dns_failure"),
			LabelVerdict: ns(ai.LabelVerdictConfirmed), LabelKey: ns("dns_failure")},
			Cause{Kind: models.SLACauseSuspected, Key: "dns_failure", Verdict: ai.LabelVerdictConfirmed}},
		{"corrected label replaces the hypothesis", CauseInput{Status: ns("ready"), Confidence: ns("high"), Hypothesis: ns("dns_failure"),
			LabelVerdict: ns(ai.LabelVerdictCorrected), LabelKey: ns("tls_problem")},
			Cause{Kind: models.SLACauseSuspected, Key: "tls_problem", Verdict: ai.LabelVerdictCorrected}},
		{"corrected to insufficient evidence", CauseInput{Status: ns("ready"), Confidence: ns("high"), Hypothesis: ns("dns_failure"),
			LabelVerdict: ns(ai.LabelVerdictCorrected), LabelKey: ns(ai.RCAInsufficientEvidence)},
			Cause{Kind: models.SLACauseInconclusive, Key: models.SLACauseInconclusive, Verdict: ai.LabelVerdictCorrected}},
		{"confirmed label with an empty key falls through to the hypothesis", CauseInput{Status: ns("ready"), Confidence: ns("high"), Hypothesis: ns("dns_failure"),
			LabelVerdict: ns(ai.LabelVerdictConfirmed), LabelKey: ns("")},
			Cause{Kind: models.SLACauseSuspected, Key: "dns_failure"}},
		{"corrected label with a NULL key falls through to the hypothesis", CauseInput{Status: ns("ready"), Confidence: ns("high"), Hypothesis: ns("dns_failure"),
			LabelVerdict: ns(ai.LabelVerdictCorrected)},
			Cause{Kind: models.SLACauseSuspected, Key: "dns_failure"}},
		{"confirmed label the catalogue cannot title is inconclusive", CauseInput{Status: ns("ready"), Confidence: ns("high"), Hypothesis: ns("dns_failure"),
			LabelVerdict: ns(ai.LabelVerdictConfirmed), LabelKey: ns("retired_cause")},
			Cause{Kind: models.SLACauseInconclusive, Key: models.SLACauseInconclusive, Verdict: ai.LabelVerdictConfirmed}},
		{"confirmed insufficient evidence is inconclusive", CauseInput{Status: ns("ready"), Confidence: ns("high"), Hypothesis: ns(ai.RCAInsufficientEvidence),
			LabelVerdict: ns(ai.LabelVerdictConfirmed), LabelKey: ns(ai.RCAInsufficientEvidence)},
			Cause{Kind: models.SLACauseInconclusive, Key: models.SLACauseInconclusive, Verdict: ai.LabelVerdictConfirmed}},
		{"pending triage wins over claimable", CauseInput{Status: ns("pending"), Claimable: true},
			Cause{Kind: models.SLACausePending, Key: models.SLACausePending}},
		{"failed triage is untriaged even if the alert looks claimable", CauseInput{Status: ns("failed"), Claimable: true},
			Cause{Kind: models.SLACauseUntriaged, Key: models.SLACauseUntriaged}},
		{"rejected label is ignored", CauseInput{Status: ns("ready"), Confidence: ns("high"), Hypothesis: ns("dns_failure"),
			LabelVerdict: ns(ai.LabelVerdictRejected), LabelKey: ns("tls_problem")},
			Cause{Kind: models.SLACauseSuspected, Key: "dns_failure"}},
		{"ready named medium confidence", CauseInput{Status: ns("ready"), Confidence: ns("medium"), Hypothesis: ns("dns_failure")},
			Cause{Kind: models.SLACauseSuspected, Key: "dns_failure"}},
		{"ready named low confidence is inconclusive", CauseInput{Status: ns("ready"), Confidence: ns("low"), Hypothesis: ns("tls_problem")},
			Cause{Kind: models.SLACauseInconclusive, Key: models.SLACauseInconclusive}},
		{"ready named without confidence is inconclusive", CauseInput{Status: ns("ready"), Hypothesis: ns("tls_problem")},
			Cause{Kind: models.SLACauseInconclusive, Key: models.SLACauseInconclusive}},
		{"confirmed label wins over low confidence", CauseInput{Status: ns("ready"), Confidence: ns("low"), Hypothesis: ns("tls_problem"),
			LabelVerdict: ns(ai.LabelVerdictConfirmed), LabelKey: ns("tls_problem")},
			Cause{Kind: models.SLACauseSuspected, Key: "tls_problem", Verdict: ai.LabelVerdictConfirmed}},
		{"corrected label wins over low confidence", CauseInput{Status: ns("ready"), Confidence: ns("low"), Hypothesis: ns("tls_problem"),
			LabelVerdict: ns(ai.LabelVerdictCorrected), LabelKey: ns("dns_failure")},
			Cause{Kind: models.SLACauseSuspected, Key: "dns_failure", Verdict: ai.LabelVerdictCorrected}},
		{"rejected label on low confidence stays inconclusive", CauseInput{Status: ns("ready"), Confidence: ns("low"), Hypothesis: ns("tls_problem"),
			LabelVerdict: ns(ai.LabelVerdictRejected), LabelKey: ns("dns_failure")},
			Cause{Kind: models.SLACauseInconclusive, Key: models.SLACauseInconclusive}},
		{"pending triage", CauseInput{Status: ns("pending")},
			Cause{Kind: models.SLACausePending, Key: models.SLACausePending}},
		{"no row, claimable", CauseInput{Claimable: true},
			Cause{Kind: models.SLACausePending, Key: models.SLACausePending}},
		{"no row, not claimable", CauseInput{},
			Cause{Kind: models.SLACauseUntriaged, Key: models.SLACauseUntriaged}},
		{"failed", CauseInput{Status: ns("failed")},
			Cause{Kind: models.SLACauseUntriaged, Key: models.SLACauseUntriaged}},
		{"skipped", CauseInput{Status: ns("skipped")},
			Cause{Kind: models.SLACauseUntriaged, Key: models.SLACauseUntriaged}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, EffectiveCause(tc.in))
		})
	}
}
