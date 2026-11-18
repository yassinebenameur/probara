//go:build !unit

package statuspagedomains

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	testcontainers "github.com/testcontainers/testcontainers-go"

	"github.com/yassinebenameur/probara/api/internal/services/audit"
	"github.com/yassinebenameur/probara/shared/db"
	"github.com/yassinebenameur/probara/shared/pagedomains"
	"github.com/yassinebenameur/probara/shared/statusupdates"
	"github.com/yassinebenameur/probara/shared/testutil"
)

type spyPublisher struct {
	mu     sync.Mutex
	events []statusupdates.Event
}

func (p *spyPublisher) Publish(e statusupdates.Event) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.events = append(p.events, e)
	return nil
}

func (p *spyPublisher) count(pageID uuid.UUID) int {
	p.mu.Lock()
	defer p.mu.Unlock()
	n := 0
	for _, e := range p.events {
		if e.Type == pagedomains.EventDomainsChanged && e.StatusPageID == pageID.String() {
			n++
		}
	}
	return n
}

type spyAudit struct {
	mu     sync.Mutex
	events []audit.Event
}

func (a *spyAudit) Record(e audit.Event) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.events = append(a.events, e)
}

func (a *spyAudit) actions(tenantID uuid.UUID) []string {
	a.mu.Lock()
	defer a.mu.Unlock()
	var out []string
	for _, e := range a.events {
		if e.TenantID != nil && *e.TenantID == tenantID {
			out = append(out, e.Action)
		}
	}
	return out
}

func newTestService(dbClient *db.Client, r Resolver, pub Publisher) *Service {
	return NewService(dbClient, Config{
		CNAMETarget:   testTarget,
		ReservedHosts: ReservedHosts("https://probara.platform.example", "https://api.platform.example/api", "wss://nats.platform.example/ws", "https://"+testTarget, testTarget),
		Resolver:      r,
		Publisher:     pub,
		Audit:         &spyAudit{},
	})
}

func auditOf(s *Service) *spyAudit { return s.cfg.Audit.(*spyAudit) }

// publishDNS makes hostname verifiable with the token of its oldest claim.
func publishDNS(t *testing.T, ctx context.Context, dbClient *db.Client, f *fakeResolver, hostname string) {
	t.Helper()
	var token string
	if err := dbClient.QueryRowContext(ctx, `SELECT verification_token FROM status_page_domains WHERE hostname = $1 ORDER BY created_at LIMIT 1`, hostname).Scan(&token); err != nil {
		t.Fatal(err)
	}
	f.mu.Lock()
	f.txt["_probara-verify."+hostname+"."] = []string{token}
	f.cnames[hostname+"."] = testTarget + "."
	f.mu.Unlock()
}

func TestService_CustomDomains(t *testing.T) {
	testcontainers.SkipIfProviderIsNotHealthy(t)
	ctx := context.Background()
	dbClient, cleanup := testutil.SetupPostgresDB(ctx, t)
	defer cleanup()

	tenantA := testutil.InsertTenant(ctx, t, dbClient, "domains-a")
	tenantB := testutil.InsertTenant(ctx, t, dbClient, "domains-b")
	pageA := testutil.InsertStatusPage(ctx, t, dbClient, tenantA, "domains-a", "A")
	pageA2 := testutil.InsertStatusPage(ctx, t, dbClient, tenantA, "domains-a2", "A2")
	pageB := testutil.InsertStatusPage(ctx, t, dbClient, tenantB, "domains-b", "B")

	f := newFakeResolver()
	pub := &spyPublisher{}
	svc := newTestService(dbClient, f, pub)

	created, err := svc.Create(ctx, tenantA, pageA, "status.customer.example")
	if err != nil {
		t.Fatal(err)
	}
	if created.Status != pagedomains.Pending || created.Primary || len(created.Records) != 2 {
		t.Fatalf("created %+v", created)
	}
	if created.Records[0].Type != "CNAME" || created.Records[0].Value != testTarget || created.Records[0].Found != nil {
		t.Fatalf("CNAME record %+v", created.Records[0])
	}
	if created.Records[1].Name != "_probara-verify.status.customer.example" || created.Records[1].Value == "" {
		t.Fatalf("TXT record %+v", created.Records[1])
	}

	// An unverified claim reveals nothing: another workspace, or another
	// page, may claim the same hostname. A page holds it once.
	claimB, err := svc.Create(ctx, tenantB, pageB, "status.customer.example")
	if err != nil {
		t.Fatalf("another workspace's claim: err = %v", err)
	}
	claimA2, err := svc.Create(ctx, tenantA, pageA2, "status.customer.example")
	if err != nil {
		t.Fatalf("another page's claim: err = %v", err)
	}
	if _, err := svc.Create(ctx, tenantA, pageA, "status.customer.example"); !errors.Is(err, ErrAlreadyOnPage) {
		t.Fatalf("same page twice: err = %v", err)
	}
	// A workspace cannot add a domain to another workspace's page.
	if _, err := svc.Create(ctx, tenantB, pageA, "b.customer.example"); !errors.Is(err, ErrStatusPageNotFound) {
		t.Fatalf("foreign page: err = %v", err)
	}
	for _, reserved := range []string{"probara.platform.example", "api.platform.example", "nats.platform.example", testTarget} {
		if _, err := svc.Create(ctx, tenantA, pageA, reserved); !errors.Is(err, ErrHostnameReserved) {
			t.Fatalf("%s: err = %v", reserved, err)
		}
	}

	// Tenant isolation on reads, deletes and checks.
	if list, err := svc.List(ctx, tenantB, nil); err != nil || len(list.Items) != 1 || list.Items[0].ID != claimB.ID {
		t.Fatalf("tenant B sees %v (err %v)", list, err)
	}
	if _, err := svc.List(ctx, tenantB, &pageA); !errors.Is(err, ErrStatusPageNotFound) {
		t.Fatalf("tenant B listing page A: err = %v", err)
	}
	if err := svc.Delete(ctx, tenantB, created.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("tenant B deleting: err = %v", err)
	}
	if _, err := svc.Verify(ctx, tenantB, created.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("tenant B verifying: err = %v", err)
	}

	// Verify now: records missing, then present.
	got, err := svc.Verify(ctx, tenantA, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != pagedomains.Pending || got.LastError == nil || got.Records[0].Found == nil || *got.Records[0].Found {
		t.Fatalf("unverifiable domain after check: %+v", got)
	}
	// A second click within the interval is refused, without a lookup.
	before := f.lookups
	var recent *CheckedRecentlyError
	if _, err := svc.Verify(ctx, tenantA, created.ID); !errors.As(err, &recent) || recent.RetryAfter <= 0 {
		t.Fatalf("second click: err = %v", err)
	}
	if f.lookups != before {
		t.Fatal("a refused check looked something up")
	}

	publishDNS(t, ctx, dbClient, f, "status.customer.example")
	svc.now = func() time.Time { return time.Now().Add(manualCheckInterval + time.Second) }
	got, err = svc.Verify(ctx, tenantA, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != pagedomains.Verified || !got.Primary || got.LastError != nil || got.VerifiedAt == nil {
		t.Fatalf("verified domain: %+v", got)
	}
	if pub.count(pageA) != 1 {
		t.Fatalf("verification published %d domain events, want 1", pub.count(pageA))
	}

	// The first claim to verify got the hostname: the other claims are
	// superseded, each audited in its own workspace, and it is refused to
	// new claims elsewhere.
	for _, id := range []struct {
		tenant, domain uuid.UUID
	}{{tenantB, claimB.ID}, {tenantA, claimA2.ID}} {
		d, err := svc.get(ctx, id.tenant, id.domain)
		if err != nil || d.Status != pagedomains.Superseded || !d.LastError.Valid {
			t.Fatalf("other claim after verification: %+v err=%v", d, err)
		}
	}
	if got := auditOf(svc).actions(tenantB); len(got) != 1 || got[0] != "status-page-domain.supersede" {
		t.Fatalf("tenant B audit = %v", got)
	}
	if _, err := svc.Create(ctx, tenantB, testutil.InsertStatusPage(ctx, t, dbClient, tenantB, "domains-b2", "B2"), "status.customer.example"); !errors.Is(err, ErrHostnameTaken) {
		t.Fatalf("claim of a verified hostname: err = %v", err)
	}
	// B publishing its own token does not take a hostname A holds.
	publishDNS(t, ctx, dbClient, f, "status.customer.example")
	f.mu.Lock()
	var tokenB string
	if err := dbClient.QueryRowContext(ctx, `SELECT verification_token FROM status_page_domains WHERE id = $1`, claimB.ID).Scan(&tokenB); err != nil {
		t.Fatal(err)
	}
	f.txt["_probara-verify.status.customer.example."] = append(f.txt["_probara-verify.status.customer.example."], tokenB)
	f.mu.Unlock()
	if got, err := svc.Verify(ctx, tenantB, claimB.ID); err != nil || got.Status != pagedomains.Superseded {
		t.Fatalf("superseded claim re-verified while held: %+v err=%v", got, err)
	}

	// The page's public_url names its primary domain: the oldest verified.
	second, err := svc.Create(ctx, tenantA, pageA, "www.status.customer.example")
	if err != nil {
		t.Fatal(err)
	}
	publishDNS(t, ctx, dbClient, f, "www.status.customer.example")
	svc.now = func() time.Time { return time.Now().Add(2 * (manualCheckInterval + time.Second)) }
	if got, err = svc.Verify(ctx, tenantA, second.ID); err != nil || got.Primary {
		t.Fatalf("second verified domain: %+v err=%v", got, err)
	}
	var primary string
	if err := dbClient.QueryRowContext(ctx, `SELECT `+pagedomains.PrimaryHostSQL("$1::uuid"), pageA).Scan(&primary); err != nil || primary != "status.customer.example" {
		t.Fatalf("primary = %q (err %v)", primary, err)
	}

	// The per-page cap.
	for i := 0; i < MaxDomainsPerPage-2; i++ {
		if _, err := svc.Create(ctx, tenantA, pageA, uuid.NewString()[:8]+".customer.example"); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := svc.Create(ctx, tenantA, pageA, "one-too-many.customer.example"); !errors.Is(err, ErrTooManyDomains) {
		t.Fatalf("over the cap: err = %v", err)
	}

	// Removing a domain stops it being served and says so.
	events := pub.count(pageA)
	if err := svc.Delete(ctx, tenantA, created.ID); err != nil {
		t.Fatal(err)
	}
	if pub.count(pageA) != events+1 {
		t.Fatal("removal published no domain event")
	}
	if err := svc.Delete(ctx, tenantA, created.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("second delete: err = %v", err)
	}
	// Once A lets go, B's superseded claim takes the hostname on demand.
	svc.now = func() time.Time { return time.Now().Add(4 * (manualCheckInterval + time.Second)) }
	if got, err := svc.Verify(ctx, tenantB, claimB.ID); err != nil || got.Status != pagedomains.Verified {
		t.Fatalf("superseded claim after release: %+v err=%v", got, err)
	}

	// Without a CNAME target the feature is off.
	off := NewService(dbClient, Config{Resolver: f})
	if _, err := off.Create(ctx, tenantA, pageA2, "off.customer.example"); !errors.Is(err, ErrDisabled) {
		t.Fatalf("disabled: err = %v", err)
	}
	if list, err := off.List(ctx, tenantA, nil); err != nil || list.Enabled {
		t.Fatalf("disabled list: %+v err=%v", list, err)
	}
}

// countingResolver counts lookups per name and holds each one briefly, so
// two runners overlap.
type countingResolver struct {
	*fakeResolver
	mu     sync.Mutex
	counts map[string]int
}

func (c *countingResolver) LookupTXT(ctx context.Context, name string) ([]string, error) {
	c.mu.Lock()
	c.counts[name]++
	c.mu.Unlock()
	time.Sleep(20 * time.Millisecond)
	return c.fakeResolver.LookupTXT(ctx, name)
}

// Two API replicas run the runner at once: SKIP LOCKED and the lease give
// every due domain to exactly one of them, and nothing is checked before it
// is due.
func TestRunner_TwoReplicasCheckEachDomainOnce(t *testing.T) {
	testcontainers.SkipIfProviderIsNotHealthy(t)
	ctx := context.Background()
	dbClient, cleanup := testutil.SetupPostgresDB(ctx, t)
	defer cleanup()

	tenant := testutil.InsertTenant(ctx, t, dbClient, "domains-runner")
	f := &countingResolver{fakeResolver: newFakeResolver(), counts: map[string]int{}}
	pub := &spyPublisher{}
	a := newTestService(dbClient, f, pub)
	b := newTestService(dbClient, f, pub)

	const pages, perPage = 6, 5
	var hosts []string
	for p := 0; p < pages; p++ {
		pageID := testutil.InsertStatusPage(ctx, t, dbClient, tenant, "runner-"+uuid.NewString()[:8], "R")
		for d := 0; d < perPage; d++ {
			host := uuid.NewString()[:8] + ".runner.example"
			if _, err := a.Create(ctx, tenant, pageID, host); err != nil {
				t.Fatal(err)
			}
			hosts = append(hosts, host)
			// Half of them have their records.
			if d%2 == 0 {
				publishDNS(t, ctx, dbClient, f.fakeResolver, host)
			}
		}
	}
	// One domain is not due yet.
	if _, err := dbClient.ExecContext(ctx, `UPDATE status_page_domains SET next_check_at = NOW() + INTERVAL '1 hour' WHERE hostname = $1`, hosts[1]); err != nil {
		t.Fatal(err)
	}

	var wg sync.WaitGroup
	for _, svc := range []*Service{a, b} {
		wg.Add(1)
		go func(s *Service) {
			defer wg.Done()
			if err := s.RunOnce(ctx); err != nil {
				t.Error(err)
			}
		}(svc)
	}
	wg.Wait()

	for i, host := range hosts {
		got := f.counts["_probara-verify."+host+"."]
		want := 1
		if i == 1 {
			want = 0
		}
		if got != want {
			t.Errorf("%s checked %d times, want %d", host, got, want)
		}
	}
	var verified, pending int
	if err := dbClient.QueryRowContext(ctx, `
		SELECT COUNT(*) FILTER (WHERE status = 'verified'), COUNT(*) FILTER (WHERE status = 'pending')
		FROM status_page_domains`).Scan(&verified, &pending); err != nil {
		t.Fatal(err)
	}
	if verified != pages*3 || pending != pages*2 {
		t.Fatalf("verified %d pending %d, want %d %d", verified, pending, pages*3, pages*2)
	}
	// Every checked domain is scheduled for its next check, none is left
	// leased.
	var leftDue int
	if err := dbClient.QueryRowContext(ctx, `SELECT COUNT(*) FROM status_page_domains WHERE next_check_at <= NOW() + INTERVAL '3 minutes'`).Scan(&leftDue); err != nil {
		t.Fatal(err)
	}
	if leftDue != 0 {
		t.Fatalf("%d domains left on their lease", leftDue)
	}

	// Run again at once: nothing is due, nothing is looked up.
	before := f.lookups
	if err := a.RunOnce(ctx); err != nil {
		t.Fatal(err)
	}
	if f.lookups != before {
		t.Fatal("a second run checked domains that were not due")
	}
}

// Claims that never verify expire after ClaimTTL, audited in their
// workspace; verified and failed domains stay. A workspace holds at most
// MaxDomainsPerWorkspace domains.
func TestService_ClaimExpiryAndWorkspaceCap(t *testing.T) {
	testcontainers.SkipIfProviderIsNotHealthy(t)
	ctx := context.Background()
	dbClient, cleanup := testutil.SetupPostgresDB(ctx, t)
	defer cleanup()

	tenant := testutil.InsertTenant(ctx, t, dbClient, "domains-expiry")
	f := newFakeResolver()
	svc := newTestService(dbClient, f, &spyPublisher{})
	page := testutil.InsertStatusPage(ctx, t, dbClient, tenant, "expiry", "E")
	statuses := []string{"pending", "superseded", "verified", "failed"}
	for i, status := range statuses {
		host := status + ".expiry.example"
		if _, err := svc.Create(ctx, tenant, page, host); err != nil {
			t.Fatal(err)
		}
		if _, err := dbClient.ExecContext(ctx, `UPDATE status_page_domains SET status = $2, created_at = NOW() - INTERVAL '15 days', next_check_at = NOW() + INTERVAL '1 day' WHERE hostname = $1`, host, status); err != nil {
			t.Fatal(err)
		}
		_ = i
	}
	if _, err := svc.Create(ctx, tenant, page, "young.expiry.example"); err != nil {
		t.Fatal(err)
	}
	if _, err := dbClient.ExecContext(ctx, `UPDATE status_page_domains SET next_check_at = NOW() + INTERVAL '1 day' WHERE hostname = 'young.expiry.example'`); err != nil {
		t.Fatal(err)
	}
	if err := svc.RunOnce(ctx); err != nil {
		t.Fatal(err)
	}
	var left []string
	rows, err := dbClient.QueryContext(ctx, `SELECT hostname FROM status_page_domains ORDER BY hostname`)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var h string
		_ = rows.Scan(&h)
		left = append(left, h)
	}
	rows.Close()
	if strings.Join(left, ",") != "failed.expiry.example,verified.expiry.example,young.expiry.example" {
		t.Fatalf("left after expiry: %v", left)
	}
	if got := auditOf(svc).actions(tenant); len(got) != 2 || got[0] != "status-page-domain.expire" {
		t.Fatalf("expiry audit = %v", got)
	}

	// The workspace cap counts every page.
	for i := 0; len(left)+i < MaxDomainsPerWorkspace; i++ {
		p := page
		if i%MaxDomainsPerPage == 0 || i > 0 {
			p = testutil.InsertStatusPage(ctx, t, dbClient, tenant, "cap-"+uuid.NewString()[:8], "C")
		}
		if _, err := svc.Create(ctx, tenant, p, uuid.NewString()[:8]+".cap.example"); err != nil {
			t.Fatal(err)
		}
	}
	other := testutil.InsertStatusPage(ctx, t, dbClient, tenant, "cap-last", "L")
	if _, err := svc.Create(ctx, tenant, other, "one-too-many.cap.example"); !errors.Is(err, ErrTooManyWorkspaceDomains) {
		t.Fatalf("over the workspace cap: err = %v", err)
	}
}

// cancellingResolver cancels the operator's request mid-lookup.
type cancellingResolver struct {
	*fakeResolver
	cancel context.CancelFunc
}

func (c *cancellingResolver) LookupTXT(ctx context.Context, name string) ([]string, error) {
	c.cancel()
	return c.fakeResolver.LookupTXT(ctx, name)
}

// An operator navigating away mid-check never records a cancelled lookup as
// a failure: the check completes on its own timeouts.
func TestService_VerifyOutlivesTheRequest(t *testing.T) {
	testcontainers.SkipIfProviderIsNotHealthy(t)
	ctx := context.Background()
	dbClient, cleanup := testutil.SetupPostgresDB(ctx, t)
	defer cleanup()

	tenant := testutil.InsertTenant(ctx, t, dbClient, "domains-cancel")
	page := testutil.InsertStatusPage(ctx, t, dbClient, tenant, "cancel", "C")
	f := newFakeResolver()
	reqCtx, cancel := context.WithCancel(ctx)
	svc := newTestService(dbClient, &cancellingResolver{fakeResolver: f, cancel: cancel}, &spyPublisher{})
	d, err := svc.Create(ctx, tenant, page, "status.cancel.example")
	if err != nil {
		t.Fatal(err)
	}
	publishDNS(t, ctx, dbClient, f, "status.cancel.example")
	got, err := svc.Verify(reqCtx, tenant, d.ID)
	if err != nil || got.Status != pagedomains.Verified || got.LastError != nil {
		t.Fatalf("verify with the request cancelled mid-check: %+v err=%v", got, err)
	}
}

// A deleted workspace's domains are served nowhere and hold nothing: they
// block no claim, a verification elsewhere takes their hostname (without
// auditing into the deleted workspace), and the runner never checks them.
func TestService_DeletedWorkspaceDomains(t *testing.T) {
	testcontainers.SkipIfProviderIsNotHealthy(t)
	ctx := context.Background()
	dbClient, cleanup := testutil.SetupPostgresDB(ctx, t)
	defer cleanup()

	gone := testutil.InsertTenant(ctx, t, dbClient, "domains-gone")
	live := testutil.InsertTenant(ctx, t, dbClient, "domains-live")
	gonePage := testutil.InsertStatusPage(ctx, t, dbClient, gone, "gone", "G")
	livePage := testutil.InsertStatusPage(ctx, t, dbClient, live, "live", "L")
	f := newFakeResolver()
	svc := newTestService(dbClient, f, &spyPublisher{})

	held, err := svc.Create(ctx, gone, gonePage, "status.shared.example")
	if err != nil {
		t.Fatal(err)
	}
	pending, err := svc.Create(ctx, gone, gonePage, "pending.shared.example")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := dbClient.ExecContext(ctx, `UPDATE status_page_domains SET status = 'verified' WHERE id = $1`, held.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := dbClient.ExecContext(ctx, `UPDATE tenants SET deleted_at = NOW() WHERE id = $1`, gone); err != nil {
		t.Fatal(err)
	}

	// Its verified hostname does not block a claim, and a verification takes it.
	claim, err := svc.Create(ctx, live, livePage, "status.shared.example")
	if err != nil {
		t.Fatalf("claim of a deleted workspace's hostname: err = %v", err)
	}
	publishDNS(t, ctx, dbClient, f, "status.shared.example")
	f.mu.Lock()
	var token string
	if err := dbClient.QueryRowContext(ctx, `SELECT verification_token FROM status_page_domains WHERE id = $1`, claim.ID).Scan(&token); err != nil {
		t.Fatal(err)
	}
	f.txt["_probara-verify.status.shared.example."] = []string{token}
	f.mu.Unlock()
	got, err := svc.Verify(ctx, live, claim.ID)
	if err != nil || got.Status != pagedomains.Verified {
		t.Fatalf("verification against a deleted workspace's hostname: %+v err=%v", got, err)
	}
	if actions := auditOf(svc).actions(gone); len(actions) != 0 {
		t.Fatalf("audited into the deleted workspace: %v", actions)
	}

	// The runner never checks the deleted workspace's domains.
	if _, err := dbClient.ExecContext(ctx, `UPDATE status_page_domains SET next_check_at = NOW() - INTERVAL '1 minute' WHERE id = $1`, pending.ID); err != nil {
		t.Fatal(err)
	}
	if err := svc.RunOnce(ctx); err != nil {
		t.Fatal(err)
	}
	f.mu.Lock()
	for _, name := range f.asked {
		if strings.Contains(name, "pending.shared.example") {
			t.Errorf("checked a deleted workspace's domain: %s", name)
		}
	}
	f.mu.Unlock()
}
