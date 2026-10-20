package statuspage

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/google/uuid"

	shareddb "github.com/yassinebenameur/probara/shared/db"
)

// sipMediaMetrics is a SIP test call's latest metrics_data with a full
// call.media block: media addresses, the numbers dialled and the quality
// estimate, none of which a public page may show.
const sipMediaMetrics = `{"sip":{"method":"INVITE","transport":"tls","request_uri":"sips:+33123456789@carrier.example.com",
"tls":{"not_after":"2027-02-01T00:00:00Z","days_until_expiry":12},
"call":{"mode":"answer","answered":true,"sdp_answer":{"codec":"PCMU/8000","remote_media":"198.51.100.7:40012"},
"answered_by":{"source":"203.0.113.9:5061","contact_host":"203.0.113.10"},
"media":{"expect":"echo","status":"measured","codec":"PCMU/8000","destination":"198.51.100.7:40012",
"tx":{"packets":250,"reliable":true},"rx":{"packets":251,"sources":["198.51.100.7:40012","192.0.2.44:31000"]},
"inbound":{"ssrc":"9f00aa01","expected":225,"lost":1,"loss_pct":0.44,"quality":{"r_factor":89.2,"mos_cqe":4.31,"delay_ms":99,"delay_source":"echo"}},
"round_trip":{"matched_by":"ssrc","expected":200,"matched":200,"rtt_ms":{"p50":38,"p95":44,"max":51},"quality":{"r_factor":90.8,"mos_cqe":4.36,"delay_ms":99,"delay_source":"echo"}},
"model":"G.107 E-model","two_way":"verified"}}}}`

// The status page reads a check's metrics_data only for its certificate
// (tlscert.Extract): a SIP test call's media block, its addresses and its
// MOS reach neither the rendered page nor its JSON, while the certificate
// still does.
func TestStatusPage_SIPCallMediaDoesNotLeak(t *testing.T) {
	sqlDB, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer sqlDB.Close()
	tenantID, monitorID := uuid.New(), uuid.New()
	mock.ExpectQuery("(?s)JOIN monitors mon.*LEFT JOIN LATERAL").
		WithArgs(sqlmock.AnyArg(), tenantID).
		WillReturnRows(sqlmock.NewRows([]string{
			"monitor_id", "current_state", "status", "http_status", "latency_ms", "created_at", "metrics_data", "in_maintenance",
		}).AddRow(monitorID, "up", "success", 200, 140, time.Date(2026, 10, 20, 9, 0, 0, 0, time.UTC), []byte(sipMediaMetrics), false))

	svc := NewService(&shareddb.Client{DB: sqlDB}, nil)
	statuses, err := svc.batchCurrentStatus(context.Background(), []uuid.UUID{monitorID}, tenantID)
	if err != nil {
		t.Fatal(err)
	}
	monitor := MonitorStatus{ID: monitorID.String(), Name: "Carrier trunk", MonitorType: "sip"}
	assignRegularShortRange(&monitor, monitorID, statuses, nil, nil, nil)
	if monitor.TLSDaysUntilExpiry == nil || *monitor.TLSDaysUntilExpiry != 12 {
		t.Fatalf("TLSDaysUntilExpiry = %v, want the certificate still read", monitor.TLSDaysUntilExpiry)
	}

	data := &StatusPageData{ID: "page-1", Slug: "status", Title: "Status", Monitors: []MonitorStatus{monitor}}
	html, err := renderPublicStatusPage(data, true)
	if err != nil {
		t.Fatal(err)
	}
	asJSON, err := json.Marshal(data)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(html, "Carrier trunk") {
		t.Fatal("the page did not render the monitor")
	}
	for _, out := range []struct{ name, body string }{{"html", html}, {"json", string(asJSON)}} {
		for _, secret := range []string{
			"198.51.100.7", "40012", "192.0.2.44", "203.0.113.9", "203.0.113.10", "+33123456789", "carrier.example.com",
			"mos_cqe", "4.31", "4.36", "destination", "sources", "round_trip", "9f00aa01",
		} {
			if strings.Contains(out.body, secret) {
				t.Errorf("the public %s contains %q", out.name, secret)
			}
		}
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
