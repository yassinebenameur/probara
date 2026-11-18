package statuspage

import (
	"bytes"
	"net/netip"
	"strings"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"

	"github.com/yassinebenameur/probara/shared/config"
	shareddb "github.com/yassinebenameur/probara/shared/db"
	"github.com/yassinebenameur/probara/shared/logger"
)

// With gated pages and no trusted proxy, every visitor behind an ingress
// shares one unattributable address: say so at startup.
func TestWarnUntrustedProxyWithGatedPages(t *testing.T) {
	for _, tc := range []struct {
		name    string
		proxies []netip.Prefix
		gated   bool
		warn    bool
	}{
		{"gated pages, no proxy", nil, true, true},
		{"no gated pages", nil, false, false},
		{"proxy configured", []netip.Prefix{netip.MustParsePrefix("10.0.0.0/8")}, true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sqlDB, mock, err := sqlmock.New()
			if err != nil {
				t.Fatal(err)
			}
			defer sqlDB.Close()
			if tc.proxies == nil {
				mock.ExpectQuery("SELECT EXISTS").WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(tc.gated))
			}
			log := logger.New("status-page", "info")
			var buf bytes.Buffer
			log.SetOutput(&buf)
			cfg := &config.StatusPageConfig{}
			cfg.TrustedProxyCIDRs = tc.proxies
			warnUntrustedProxyWithGatedPages(cfg, NewService(&shareddb.Client{DB: sqlDB}, nil), log)
			if got := strings.Contains(buf.String(), "TRUSTED_PROXY_CIDRS is unset"); got != tc.warn {
				t.Fatalf("warned = %v, want %v (%s)", got, tc.warn, buf.String())
			}
			if err := mock.ExpectationsWereMet(); err != nil {
				t.Fatal(err)
			}
		})
	}
}
