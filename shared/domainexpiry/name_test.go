package domainexpiry

import (
	"errors"
	"testing"
)

func TestRegistrableDomain(t *testing.T) {
	tests := []struct {
		in      string
		want    string
		wantErr error
	}{
		{in: "example.com", want: "example.com"},
		{in: " EXAMPLE.COM. ", want: "example.com"},
		{in: "www.example.com", want: "example.com"},
		{in: "a.b.c.example.com", want: "example.com"},
		// Multi-label ICANN suffixes keep their extra labels.
		{in: "www.shop.example.co.uk", want: "example.co.uk"},
		{in: "example.co.uk", want: "example.co.uk"},
		{in: "api.example.com.au", want: "example.com.au"},
		{in: "city.kawasaki.jp", want: "city.kawasaki.jp"}, // an exception rule under a wildcard
		// Private suffixes are hosting platforms, not registries: the
		// registered name is the platform's.
		{in: "someone.github.io", want: "github.io"},
		{in: "blog.example.blogspot.com", want: "blogspot.com"},
		// Internationalized names are looked up in punycode.
		{in: "bücher.de", want: "xn--bcher-kva.de"},
		{in: "www.Bücher.DE", want: "xn--bcher-kva.de"},
		{in: "例え.jp", want: "xn--r8jz45g.jp"},
		{in: "xn--bcher-kva.de", want: "xn--bcher-kva.de"},
		// An unlisted TLD is a single-label suffix.
		{in: "host.example.notatld", want: "example.notatld"},

		{in: "", wantErr: ErrNameRequired},
		{in: "   ", wantErr: ErrNameRequired},
		{in: "com", wantErr: ErrNameSuffix},
		{in: "co.uk", wantErr: ErrNameSuffix},
		{in: ".com", wantErr: ErrNameInvalid},
		{in: "192.0.2.1", wantErr: ErrNameIP},
		{in: "2001:db8::1", wantErr: ErrNameIP},
		{in: "[2001:db8::1]", wantErr: ErrNameIP},
		{in: "10.1", wantErr: ErrNameIP},
		{in: "https://example.com", wantErr: ErrNameNotBare},
		{in: "example.com:443", wantErr: ErrNameNotBare},
		{in: "example.com/path", wantErr: ErrNameNotBare},
		{in: "user@example.com", wantErr: ErrNameNotBare},
		{in: "exa mple.com", wantErr: ErrNameNotBare},
		{in: "-bad.com", wantErr: ErrNameInvalid},
		{in: "under_score.com", wantErr: ErrNameInvalid},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			got, err := RegistrableDomain(tt.in)
			if tt.wantErr != nil {
				if !errors.Is(err, tt.wantErr) {
					t.Fatalf("RegistrableDomain(%q) = %q, %v; want error %v", tt.in, got, err, tt.wantErr)
				}
				return
			}
			if err != nil || got != tt.want {
				t.Fatalf("RegistrableDomain(%q) = %q, %v; want %q", tt.in, got, err, tt.want)
			}
		})
	}
}

func TestTLD(t *testing.T) {
	if got := TLD("example.co.uk"); got != "uk" {
		t.Fatalf("TLD = %q", got)
	}
}
