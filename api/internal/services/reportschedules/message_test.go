package reportschedules

import (
	"bufio"
	"context"
	"encoding/base64"
	"io"
	"mime"
	"mime/multipart"
	"net"
	netmail "net/mail"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/yassinebenameur/probara/api/internal/models"
	"github.com/yassinebenameur/probara/shared/actionlink"
	"github.com/yassinebenameur/probara/shared/notifications/plugin/builtin/email"
)

// smtpSink is a minimal SMTP server that accepts everything and keeps each
// message's DATA.
type smtpSink struct {
	host string
	port int
	mu   sync.Mutex
	data []string
}

func startSMTPSink(t *testing.T) *smtpSink {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { ln.Close() })
	s := &smtpSink{host: "127.0.0.1", port: ln.Addr().(*net.TCPAddr).Port}
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				defer conn.Close()
				r := bufio.NewReader(conn)
				write := func(line string) { _, _ = conn.Write([]byte(line + "\r\n")) }
				write("220 sink ESMTP")
				var body strings.Builder
				inData := false
				for {
					line, err := r.ReadString('\n')
					if err != nil {
						return
					}
					if inData {
						if strings.TrimRight(line, "\r\n") == "." {
							inData = false
							s.mu.Lock()
							s.data = append(s.data, body.String())
							s.mu.Unlock()
							body.Reset()
							write("250 queued")
							continue
						}
						body.WriteString(strings.TrimPrefix(line, "."))
						continue
					}
					switch strings.ToUpper(strings.Fields(line + " x")[0]) {
					case "DATA":
						inData = true
						write("354 go ahead")
					case "QUIT":
						write("221 bye")
						return
					default:
						write("250 ok")
					}
				}
			}()
		}
	}()
	return s
}

// TestReportEmailOverSMTP sends one recipient's report email through the
// real SMTP mailer and reads it back off the wire: the PDF attachment, both
// RFC 8058 unsubscribe headers pointing at the one-click route, and the
// plain-text and HTML parts with the report link and the confirm page.
func TestReportEmailOverSMTP(t *testing.T) {
	sink := startSMTPSink(t)
	mailer, err := email.NewSMTPMailer(email.SMTPParams{Host: sink.host, Port: sink.port, From: "reports@probara.example.com"})
	require.NoError(t, err)
	s := NewService(nil, nil, Config{Mailer: mailer, Links: actionlink.New(testKeys{}), AppBaseURL: "https://probara.example.com"})
	s.now = func() time.Time { return time.Date(2026, 10, 1, 1, 30, 0, 0, time.UTC) }

	pct, met := 99.95, true
	report := &models.SLAReport{
		SLAID:      uuid.New(),
		Definition: models.SLAReportDefinition{Name: "Checkout", TargetPct: 99.9, Aggregation: models.SLAAggregationSerial, Period: "monthly", Timezone: "UTC"},
		Period: models.SLAReportPeriod{Key: "2026-09", Start: time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC),
			End: time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC), EffectiveEnd: time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC), IsClosed: true},
		Summary: models.SLAReportSummary{HasData: true, AvailabilityPct: &pct, Met: &met, EligibleSeconds: 2592000},
		Budget:  models.SLAReportBudget{AllowedSeconds: 2592, ConsumedSeconds: 1296, RemainingSeconds: 1296, AllowedFullPeriodSeconds: 2592},
		Burn: &models.SLAReportBurn{
			Windows:    []models.SLAReportBurnWindow{{Key: models.SLABurnWindowPeriod, AvailabilityPct: &pct, BurnRate: ptr(0.5)}},
			Exhaustion: models.SLABudgetNotProjected, BurnDown: []models.SLAReportBurnPoint{},
		},
		Issued: &models.SLAReportIssue{ID: uuid.New(), IssuedAt: time.Date(2026, 10, 1, 1, 0, 0, 0, time.UTC), IssuedBy: "report schedule"},
	}
	attachments, err := renderAttachments(report, false)
	require.NoError(t, err)
	sc := &row{TenantID: uuid.New()}
	sc.ID = uuid.New()
	sla := &models.SLA{ID: report.SLAID, TenantID: sc.TenantID, Name: "Checkout"}

	msg, err := s.message(sc, sla, report, "ops@example.com", attachments)
	require.NoError(t, err)
	require.NoError(t, mailer.SendMessage(context.Background(), msg))
	require.Len(t, sink.data, 1)

	parsed, err := netmail.ReadMessage(strings.NewReader(sink.data[0]))
	require.NoError(t, err)
	dec := new(mime.WordDecoder)
	subject, err := dec.DecodeHeader(parsed.Header.Get("Subject"))
	require.NoError(t, err)
	require.Equal(t, "SLA report: Checkout, 2026-09: met (99.950%, target 99.9%)", subject)
	require.Equal(t, "<ops@example.com>", parsed.Header.Get("To"))

	oneClick := parsed.Header.Get("List-Unsubscribe")
	require.Regexp(t, `^<https://probara\.example\.com/api/v1/report-unsubscribe\?token=[A-Za-z0-9_-]{95}>$`, oneClick)
	require.Equal(t, "List-Unsubscribe=One-Click", parsed.Header.Get("List-Unsubscribe-Post"))
	token := strings.TrimSuffix(strings.SplitN(oneClick, "token=", 2)[1], ">")
	claims, err := actionlink.New(testKeys{}).Verify(token, actionlink.ActionUnsubscribeReport)
	require.NoError(t, err)
	require.Equal(t, sc.TenantID, claims.TenantID)
	require.Equal(t, sc.ID, claims.AlertID)
	require.NotContains(t, oneClick, "ops", "the link names no address")

	mediaType, params, err := mime.ParseMediaType(parsed.Header.Get("Content-Type"))
	require.NoError(t, err)
	require.Equal(t, "multipart/mixed", mediaType)
	mr := multipart.NewReader(parsed.Body, params["boundary"])

	body, err := mr.NextPart()
	require.NoError(t, err)
	bodyType, bodyParams, err := mime.ParseMediaType(body.Header.Get("Content-Type"))
	require.NoError(t, err)
	require.Equal(t, "multipart/alternative", bodyType)
	alt := multipart.NewReader(body, bodyParams["boundary"])
	parts := map[string]string{}
	for {
		p, err := alt.NextPart()
		if err == io.EOF {
			break
		}
		require.NoError(t, err)
		raw, err := io.ReadAll(base64.NewDecoder(base64.StdEncoding, p))
		require.NoError(t, err)
		ct, _, _ := mime.ParseMediaType(p.Header.Get("Content-Type"))
		parts[ct] = string(raw)
	}
	link := "https://probara.example.com/slas?sla=" + sla.ID.String() + "&tenant=" + sc.TenantID.String()
	require.Contains(t, parts["text/plain"], link)
	require.Contains(t, parts["text/html"], strings.ReplaceAll(link, "&", "&amp;"))
	for _, ct := range []string{"text/plain", "text/html"} {
		require.Contains(t, parts[ct], "https://probara.example.com/report-unsubscribe#"+token, ct)
		require.Contains(t, parts[ct], "99.950%", ct)
		require.Contains(t, parts[ct], "50.0% (21m 36s of 43m 12s allowed)", ct)
		require.Contains(t, parts[ct], "0.50x", ct)
	}
	require.NotContains(t, parts["text/html"], "data:")

	pdf, err := mr.NextPart()
	require.NoError(t, err)
	require.Equal(t, "application/pdf", pdf.Header.Get("Content-Type"))
	_, dispParams, err := mime.ParseMediaType(pdf.Header.Get("Content-Disposition"))
	require.NoError(t, err)
	require.Equal(t, "sla-checkout-2026-09.pdf", dispParams["filename"])
	raw, err := io.ReadAll(base64.NewDecoder(base64.StdEncoding, pdf))
	require.NoError(t, err)
	require.True(t, strings.HasPrefix(string(raw), "%PDF"), "the attachment is the rendered PDF")
	_, err = mr.NextPart()
	require.ErrorIs(t, err, io.EOF, "no CSV unless asked for")
}

func ptr[T any](v T) *T { return &v }

func TestSubjectHasNoControlCharacters(t *testing.T) {
	v := reportView{SLAName: "Check\r\nout", PeriodKey: "2026-09", Verdict: "Met", Availability: "100.000%", Target: "99.9%"}
	require.Equal(t, "SLA report: Check  out, 2026-09: met (100.000%, target 99.9%)", v.subject())
}

func TestNormalizeRecipients(t *testing.T) {
	got, err := normalizeRecipients([]string{" A@Example.com", "a@example.com", "", "b+tag@example.com"})
	require.NoError(t, err)
	require.Equal(t, []string{"a@example.com", "b+tag@example.com"}, got)
	for _, bad := range []string{"ops", "Ops <ops@example.com>", "ops@", `"x"@example.com`, "a@b@example.com"} {
		_, err := normalizeRecipients([]string{bad})
		require.Error(t, err, bad)
	}
}

func TestAddressKey(t *testing.T) {
	a, b := uuid.New(), uuid.New()
	require.Equal(t, addressKey(a, "Ops@Example.com "), addressKey(a, "ops@example.com"))
	require.NotEqual(t, addressKey(a, "ops@example.com"), addressKey(b, "ops@example.com"), "salted per schedule")
	id, err := keyID(addressKey(a, "ops@example.com"))
	require.NoError(t, err)
	require.Len(t, addressKey(a, "x@example.com"), 32)
	require.NotEqual(t, uuid.Nil, id)
}
