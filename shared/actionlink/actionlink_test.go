package actionlink

import (
	"bytes"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/yassinebenameur/probara/shared/notifications/present"
)

// keyring is an in-memory secrets keyring: versions present, current the
// highest.
type keyring map[int][]byte

func (k keyring) CurrentKey() ([]byte, int, error) {
	cur := 0
	for v := range k {
		if v > cur {
			cur = v
		}
	}
	if cur == 0 {
		return nil, 0, errors.New("no key")
	}
	return k[cur], cur, nil
}

func (k keyring) KeyForVersion(v int) ([]byte, error) {
	key, ok := k[v]
	if !ok {
		return nil, fmt.Errorf("no key for version %d", v)
	}
	return key, nil
}

var (
	key1 = bytes.Repeat([]byte{1}, 32)
	key2 = bytes.Repeat([]byte{2}, 32)

	tenant  = uuid.MustParse("8f14e45f-ceea-467a-9f0b-2c1a3b4d5e6f")
	alert   = uuid.MustParse("b17c2e90-4a1d-4f3e-9c88-71f0a2d5c311")
	channel = uuid.MustParse("3f2a91cc-11de-4b7a-9a2e-6c5f8d0e1234")

	t0 = time.Date(2026, 11, 15, 9, 0, 0, 0, time.UTC)
)

func signerAt(keys keyring, now time.Time) *Signer {
	s := New(keys)
	s.now = func() time.Time { return now }
	return s
}

func mint(t *testing.T, s *Signer, issued time.Time) string {
	t.Helper()
	tok, err := s.ActionToken(ActionAcknowledge, tenant.String(), alert.String(), channel.String(), issued)
	if err != nil {
		t.Fatalf("ActionToken: %v", err)
	}
	return tok
}

func TestRoundTrip(t *testing.T) {
	s := signerAt(keyring{1: key1}, t0)
	tok := mint(t, s, t0)
	if len(tok) != EncodedLen || EncodedLen != 95 {
		t.Fatalf("token length %d (EncodedLen %d), want 95", len(tok), EncodedLen)
	}
	c, err := s.Verify(tok, ActionAcknowledge)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	want := Claims{Action: ActionAcknowledge, TenantID: tenant, AlertID: alert, ChannelID: channel, ExpiresAt: t0.Add(TTL), KeyVersion: 1}
	if c != want {
		t.Fatalf("claims = %+v, want %+v", c, want)
	}
}

// The lifetime runs from when the notification was built, and a future or
// missing build time counts from now so it can never stretch the link.
func TestExpiry(t *testing.T) {
	issued := t0.Add(-10 * time.Hour)
	tok := mint(t, signerAt(keyring{1: key1}, t0), issued)

	if _, err := signerAt(keyring{1: key1}, issued.Add(TTL-time.Second)).Verify(tok, ActionAcknowledge); err != nil {
		t.Fatalf("one second before expiry: %v", err)
	}
	c, err := signerAt(keyring{1: key1}, issued.Add(TTL)).Verify(tok, ActionAcknowledge)
	if !errors.Is(err, ErrExpired) {
		t.Fatalf("at expiry: err = %v, want ErrExpired", err)
	}
	if c.AlertID != alert || c.TenantID != tenant {
		t.Fatalf("an expired authentic token keeps its claims, got %+v", c)
	}

	for name, at := range map[string]time.Time{"future": t0.Add(48 * time.Hour), "zero": {}} {
		c, err := signerAt(keyring{1: key1}, t0).Verify(mint(t, signerAt(keyring{1: key1}, t0), at), ActionAcknowledge)
		if err != nil || !c.ExpiresAt.Equal(t0.Add(TTL)) {
			t.Fatalf("%s issue time: expires %v (err %v), want %v", name, c.ExpiresAt, err, t0.Add(TTL))
		}
	}
}

// Flipping any bit of the token, or any byte of its claims, invalidates it,
// and every failure looks the same.
func TestTampering(t *testing.T) {
	s := signerAt(keyring{1: key1}, t0)
	tok := mint(t, s, t0)
	raw, _ := base64.RawURLEncoding.DecodeString(tok)
	for i := range raw {
		for _, bit := range []byte{0x01, 0x80} {
			mut := bytes.Clone(raw)
			mut[i] ^= bit
			c, err := s.Verify(base64.RawURLEncoding.EncodeToString(mut), ActionAcknowledge)
			if !errors.Is(err, ErrInvalid) || c != (Claims{}) {
				t.Fatalf("byte %d bit %#x: err = %v claims = %+v, want ErrInvalid and none", i, bit, err, c)
			}
		}
	}
	for _, bad := range []string{"", "abc", tok + "A", tok[:len(tok)-1], "!" + tok[1:], tok[:10] + "." + tok[11:]} {
		if _, err := s.Verify(bad, ActionAcknowledge); !errors.Is(err, ErrInvalid) {
			t.Fatalf("Verify(%q) = %v, want ErrInvalid", bad, err)
		}
	}
}

// Swapping the tenant (or alert) bytes for another workspace's is caught by
// the MAC: a token cannot be re-pointed at another tenant's alert.
func TestWrongTenantRejected(t *testing.T) {
	s := signerAt(keyring{1: key1}, t0)
	raw, _ := base64.RawURLEncoding.DecodeString(mint(t, s, t0))
	other := uuid.New()
	copy(raw[2:18], other[:])
	if _, err := s.Verify(base64.RawURLEncoding.EncodeToString(raw), ActionAcknowledge); !errors.Is(err, ErrInvalid) {
		t.Fatalf("re-pointed tenant: err = %v, want ErrInvalid", err)
	}
}

// The last character carries two unused bits; only the canonical spelling
// verifies, so a token has exactly one form.
func TestTrailingCharacterVariantsRejected(t *testing.T) {
	const alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789-_"
	s := signerAt(keyring{1: key1}, t0)
	tok := mint(t, s, t0)
	last := strings.IndexByte(alphabet, tok[len(tok)-1])
	if last < 0 || last&3 != 0 {
		t.Fatalf("canonical last character %q must have its two unused bits clear", tok[len(tok)-1])
	}
	for low := 1; low <= 3; low++ {
		variant := tok[:len(tok)-1] + string(alphabet[last|low])
		if _, err := s.Verify(variant, ActionAcknowledge); !errors.Is(err, ErrInvalid) {
			t.Fatalf("variant %q verified (err %v); only %q may", variant[len(variant)-3:], err, tok[len(tok)-3:])
		}
	}
	if _, err := s.Verify(tok, ActionAcknowledge); err != nil {
		t.Fatalf("canonical token: %v", err)
	}
}

func TestWrongAction(t *testing.T) {
	s := signerAt(keyring{1: key1}, t0)
	tok := mint(t, s, t0)
	for _, action := range []string{"resolve", "", "ACK"} {
		if _, err := s.Verify(tok, action); !errors.Is(err, ErrInvalid) {
			t.Fatalf("Verify(action %q) = %v, want ErrInvalid", action, err)
		}
	}
	if _, err := s.ActionToken("resolve", tenant.String(), alert.String(), channel.String(), t0); err == nil {
		t.Fatal("minted a token for an action that does not exist")
	}
}

// A token from another key (another installation, or a guessed key) never
// verifies, and the derived MAC key is not the raw secrets key.
func TestOtherKeyRejected(t *testing.T) {
	tok := mint(t, signerAt(keyring{1: key1}, t0), t0)
	if _, err := signerAt(keyring{1: key2}, t0).Verify(tok, ActionAcknowledge); !errors.Is(err, ErrInvalid) {
		t.Fatalf("other key: err = %v, want ErrInvalid", err)
	}
	s := signerAt(keyring{1: key1}, t0)
	derived, err := s.key(1)
	if err != nil || bytes.Equal(derived, key1) {
		t.Fatalf("derived key must differ from the raw key (err %v)", err)
	}
}

// After a rotation new links use the new version, old links verify while
// their version stays configured, and retiring it revokes them.
func TestKeyRotation(t *testing.T) {
	old := mint(t, signerAt(keyring{1: key1}, t0), t0)

	rotated := signerAt(keyring{1: key1, 2: key2}, t0)
	fresh := mint(t, rotated, t0)
	for name, tok := range map[string]string{"old": old, "fresh": fresh} {
		if _, err := rotated.Verify(tok, ActionAcknowledge); err != nil {
			t.Fatalf("%s token after rotation: %v", name, err)
		}
	}
	if c, _ := rotated.Verify(fresh, ActionAcknowledge); c.KeyVersion != 2 {
		t.Fatalf("new links must use the current version, got %d", c.KeyVersion)
	}

	retired := signerAt(keyring{2: key2}, t0)
	if _, err := retired.Verify(old, ActionAcknowledge); !errors.Is(err, ErrInvalid) {
		t.Fatalf("retired version: err = %v, want ErrInvalid", err)
	}
	if _, err := retired.Verify(fresh, ActionAcknowledge); err != nil {
		t.Fatalf("current version after retiring v1: %v", err)
	}
}

func TestNoKey(t *testing.T) {
	var s *Signer
	if _, err := s.ActionToken(ActionAcknowledge, tenant.String(), alert.String(), channel.String(), t0); !errors.Is(err, ErrNotConfigured) {
		t.Fatalf("nil signer mint: err = %v, want ErrNotConfigured", err)
	}
	tok := mint(t, signerAt(keyring{1: key1}, t0), t0)
	if _, err := s.Verify(tok, ActionAcknowledge); !errors.Is(err, ErrInvalid) {
		t.Fatalf("nil signer verify: err = %v, want ErrInvalid", err)
	}

	t.Setenv("PROBARA_SECRETS_KEY", "")
	fromEnv, err := FromEnv()
	if err != nil || fromEnv != nil {
		t.Fatalf("FromEnv with no key = %v, %v; want nil, nil", fromEnv, err)
	}
	t.Setenv("PROBARA_SECRETS_KEY", base64.StdEncoding.EncodeToString(key1))
	fromEnv, err = FromEnv()
	if err != nil || fromEnv == nil {
		t.Fatalf("FromEnv with a key = %v, %v", fromEnv, err)
	}
	t.Setenv("PROBARA_SECRETS_KEY", "not base64!")
	if _, err := FromEnv(); err == nil {
		t.Fatal("FromEnv accepted a malformed key")
	}
}

// Only real alerts get links: the test-send alert ID ("test-…") and missing
// IDs mint nothing.
func TestRequiresIDs(t *testing.T) {
	s := signerAt(keyring{1: key1}, t0)
	for _, ids := range [][3]string{
		{tenant.String(), "test-" + alert.String(), channel.String()},
		{"", alert.String(), channel.String()},
		{tenant.String(), alert.String(), ""},
		{tenant.String(), uuid.Nil.String(), channel.String()},
	} {
		if _, err := s.ActionToken(ActionAcknowledge, ids[0], ids[1], ids[2], t0); err == nil {
			t.Fatalf("minted a token for %v", ids)
		}
	}
}

// The signer is the present.LinkSigner the plugin runtime carries, and the
// action present asks for is the one it mints.
func TestImplementsPresentSigner(t *testing.T) {
	if ActionAcknowledge != present.ActionAcknowledge {
		t.Fatalf("actionlink.ActionAcknowledge = %q, present.ActionAcknowledge = %q", ActionAcknowledge, present.ActionAcknowledge)
	}
	var _ present.LinkSigner = (*Signer)(nil)
}
