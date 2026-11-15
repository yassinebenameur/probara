package domainexpiry

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestEmbeddedBootstrap(t *testing.T) {
	d := NewDirectory(nil)
	for tld, want := range map[string]string{
		"com": "https://rdap.verisign.com/com/v1/",
		"COM": "https://rdap.verisign.com/com/v1/",
		"fr":  "https://rdap.nic.fr/",
		"uk":  "https://rdap.nominet.uk/uk/",
	} {
		if got := d.RDAPServers(tld); len(got) != 1 || got[0] != want {
			t.Errorf("RDAPServers(%q) = %v, want [%s]", tld, got, want)
		}
	}
	// .de has no RDAP service in the registry; .kg lists only plain http,
	// which is dropped so the TLD falls back to WHOIS.
	for _, tld := range []string{"de", "kg", "notatld"} {
		if got := d.RDAPServers(tld); len(got) != 0 {
			t.Errorf("RDAPServers(%q) = %v, want none", tld, got)
		}
	}
}

func TestParseBootstrapKeepsOnlyPlainHTTPSBases(t *testing.T) {
	services, err := parseBootstrap([]byte(`{"services":[
		[["a"],["https://rdap.a.example/v1"]],
		[["b", "B."],["http://rdap.b.example/", "https://rdap.b.example/"]],
		[["c"],["http://rdap.c.example/"]],
		[["d"],["https://rdap.d.example:8443/"]],
		[["e"],["https://u:p@rdap.e.example/"]],
		[["f"],["https://127.0.0.1/"]],
		[["g"]]
	]}`))
	if err != nil {
		t.Fatal(err)
	}
	if got := services["a"]; len(got) != 1 || got[0] != "https://rdap.a.example/v1/" {
		t.Errorf("a = %v", got)
	}
	if got := services["b"]; len(got) != 1 || got[0] != "https://rdap.b.example/" {
		t.Errorf("b = %v", got)
	}
	for _, tld := range []string{"c", "d", "e", "f", "g"} {
		if _, ok := services[tld]; ok {
			t.Errorf("%s kept: %v", tld, services[tld])
		}
	}
	for _, bad := range []string{`{}`, `{"services":[]}`, `not json`} {
		if _, err := parseBootstrap([]byte(bad)); err == nil {
			t.Errorf("parseBootstrap(%s): want an error", bad)
		}
	}
}

func TestDirectoryRefresh(t *testing.T) {
	fetched := make(chan string, 4)
	answer := make(chan error, 4)
	d := NewDirectory(func(_ context.Context, url string) ([]byte, error) {
		fetched <- url
		if err := <-answer; err != nil {
			return nil, err
		}
		return []byte(`{"services":[[["com"],["https://rdap.new.example/"]]]}`), nil
	})
	clock := time.Now()
	d.now = func() time.Time { return clock }

	// The first lookup serves the embedded table and refreshes behind it.
	if got := d.RDAPServers("com"); got[0] != "https://rdap.verisign.com/com/v1/" {
		t.Fatalf("before refresh: %v", got)
	}
	if url := <-fetched; url != IANABootstrapURL {
		t.Fatalf("fetched %q", url)
	}
	// While one refresh runs, lookups start no other.
	d.RDAPServers("com")
	answer <- nil
	waitFor(t, func() bool { return d.RDAPServers("com")[0] == "https://rdap.new.example/" })
	if len(fetched) != 0 {
		t.Fatalf("a second refresh started while the first ran")
	}

	// Not due again before a day; then a failed refresh keeps the table.
	clock = clock.Add(23 * time.Hour)
	d.RDAPServers("com")
	if len(fetched) != 0 {
		t.Fatal("refreshed before the day was up")
	}
	clock = clock.Add(2 * time.Hour)
	d.RDAPServers("com")
	<-fetched
	answer <- errors.New("unreachable")
	waitFor(t, func() bool {
		d.mu.Lock()
		defer d.mu.Unlock()
		return !d.refreshing
	})
	if got := d.RDAPServers("com"); got[0] != "https://rdap.new.example/" {
		t.Fatalf("a failed refresh replaced the table: %v", got)
	}
}

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatal("condition not met")
		}
		time.Sleep(5 * time.Millisecond)
	}
}
