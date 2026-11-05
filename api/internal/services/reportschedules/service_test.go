package reportschedules

import (
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/yassinebenameur/probara/api/internal/models"
)

func TestNormalizeRecipients(t *testing.T) {
	got, err := normalizeRecipients([]string{" Ops <OPS@Example.com>", "ops@example.com", "", "cto@example.org"})
	require.NoError(t, err)
	require.Equal(t, []string{"ops@example.com", "cto@example.org"}, got, "bare, lower-cased, de-duplicated")

	_, err = normalizeRecipients([]string{"  "})
	require.ErrorAs(t, err, new(Invalid))
	_, err = normalizeRecipients([]string{"not an address"})
	require.ErrorAs(t, err, new(Invalid))
	_, err = normalizeRecipients([]string{"a@example.com\r\nBcc: x@example.com"})
	require.ErrorAs(t, err, new(Invalid), "header injection is refused")
	many := make([]string, MaxRecipients+1)
	for i := range many {
		many[i] = strings.Repeat("a", i+1) + "@example.com"
	}
	_, err = normalizeRecipients(many)
	require.ErrorAs(t, err, new(Invalid))
}

func TestNormalizeFormats(t *testing.T) {
	got, err := normalizeFormats(nil)
	require.NoError(t, err)
	require.Equal(t, []string{"pdf"}, got)
	got, err = normalizeFormats([]string{"CSV", "pdf", "csv"})
	require.NoError(t, err)
	require.Equal(t, []string{"pdf", "csv"}, got)
	_, err = normalizeFormats([]string{"json"})
	require.ErrorAs(t, err, new(Invalid))
}

func TestUnsubscribeTokenAndMask(t *testing.T) {
	id := uuid.New()
	key := []byte("0123456789abcdef0123456789abcdef")
	tok := UnsubscribeToken(id, key, "Ops@Example.com")
	require.Equal(t, tok, UnsubscribeToken(id, key, "ops@example.com"), "case-insensitive address")
	require.NotEqual(t, tok, UnsubscribeToken(id, key, "cto@example.com"))
	require.NotEqual(t, tok, UnsubscribeToken(id, []byte("another key, another schedule...."), "ops@example.com"))
	require.NotContains(t, tok, "/")
	require.NotContains(t, tok, "ops")
	require.Len(t, tok, 43)

	require.Equal(t, "o***@example.com", maskEmail("ops@example.com"))
	require.Equal(t, "***", maskEmail("nope"))
}

func TestPeriodsFollowTheSLA(t *testing.T) {
	sla := &models.SLA{Period: "monthly", Timezone: "Europe/Paris"}
	now := time.Date(2026, 10, 15, 12, 0, 0, 0, time.UTC)
	next, err := nextRun(sla, now)
	require.NoError(t, err)
	// November starts at 00:00 Paris (CET, UTC+1) = 23:00 UTC on Oct 31.
	require.Equal(t, time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC), next, "close plus IssueGrace")
	closed, err := lastClosedPeriod(sla, now)
	require.NoError(t, err)
	require.Equal(t, "2026-09", closed.Key)
}

func TestMessageCarriesUnsubscribeHeaders(t *testing.T) {
	svc := &Service{appBaseURL: "https://ops.example.com"}
	sc := &Schedule{ID: uuid.New(), unsubscribeKey: []byte("k")}
	pct := 99.95
	met := true
	r := &models.SLAReport{
		Definition: models.SLAReportDefinition{Name: "Checkout", TargetPct: 99.9, Timezone: "UTC"},
		Period: models.SLAReportPeriod{Key: "2026-09", Start: time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC),
			End: time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)},
		Summary: models.SLAReportSummary{AvailabilityPct: &pct, Met: &met},
	}
	sla := &models.SLA{ID: uuid.New()}
	msg := svc.message(sc, sla, r, "ops@example.com", nil)
	require.NoError(t, msg.Validate())
	tok := UnsubscribeToken(sc.ID, sc.unsubscribeKey, "ops@example.com")
	require.Equal(t, []string{"ops@example.com"}, msg.To, "one recipient per message")
	require.Equal(t, "<https://ops.example.com/api/v1/report-unsubscribe/"+tok+">", msg.Headers["List-Unsubscribe"])
	require.Equal(t, "List-Unsubscribe=One-Click", msg.Headers["List-Unsubscribe-Post"])
	require.Contains(t, msg.Text, "https://ops.example.com/report-unsubscribe/"+tok)
	require.Contains(t, msg.Text, "computed live and was not issued")
	require.Equal(t, "SLA report: Checkout, 2026-09: Met (99.950%)", msg.Subject)
}
