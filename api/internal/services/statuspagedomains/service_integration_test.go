//go:build !unit

package statuspagedomains

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	testcontainers "github.com/testcontainers/testcontainers-go"

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

func newTestService(dbClient *db.Client, r Resolver, pub Publisher) *Service {
	return NewService(dbClient, Config{
		CNAMETarget:   testTarget,
		ReservedHosts: ReservedHosts("https://probara.platform.example", "https://api.platform.example/api", "wss://nats.platform.example/ws", "https://"+testTarget, testTarget),
		Resolver:      r,
		Publisher:     pub,
	})
}

// publishDNS makes hostname verifiable with the token stored for it.
func publishDNS(t *testing.T, ctx context.Context, dbClient *db.Client, f *fakeResolver, hostname string) {
	t.Helper()
	var token string
	if err := dbClient.QueryRowContext(ctx, `SELECT verification_token FROM status_page_domains WHERE hostname = $1`, hostname).Scan(&token); err != nil {
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

	// Hostnames are unique across the installation: another workspace, or
	// another page of the same one, cannot take one in use, pending or not.
	if _, err := svc.Create(ctx, tenantB, pageB, "status.customer.example"); !errors.Is(err, ErrHostnameTaken) {
		t.Fatalf("another workspace: err = %v", err)
	}
	if _, err := svc.Create(ctx, tenantA, pageA2, "status.customer.example"); !errors.Is(err, ErrHostnameTaken) {
		t.Fatalf("another page: err = %v", err)
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
	if list, err := svc.List(ctx, tenantB, nil); err != nil || len(list.Items) != 0 {
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
