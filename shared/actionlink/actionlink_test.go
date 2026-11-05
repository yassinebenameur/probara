package actionlink

import (
	"bytes"
	"encoding/base64"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
)

// keyring is an in-memory secrets keyring.
type keyring struct {
	keys    map[int][]byte
	current int
}

func (k *keyring) CurrentKey() ([]byte, int, error) { return k.keys[k.current], k.current, nil }

func (k *keyring) KeyForVersion(v int) ([]byte, error) {
	key, ok := k.keys[v]
	if !ok {
		return nil, fmt.Errorf("no key for version %d", v)
	}
	return key, nil
}

func newKeyring(versions ...int) *keyring {
	k := &keyring{keys: map[int][]byte{}}
	for _, v := range versions {
		k.keys[v] = bytes.Repeat([]byte{byte(v)}, 32)
		if v > k.current {
			k.current = v
		}
	}
	return k
}

var (
	tenant  = uuid.MustParse("8f14e45f-ceea-467a-9f0b-2c1a3b4d5e6f")
	alert   = uuid.MustParse("b17c2e90-4a1d-4f3e-9c88-71f0a2d5c311")
	channel = uuid.MustParse("3f2a91cc-11de-4b7a-9a2e-6c5f8d0e1234")
	t0      = time.Date(2026, 11, 5, 9, 0, 0, 0, time.UTC)
)

func signerAt(keys Keyring, now time.Time) *Signer {
	return New(keys).WithClock(func() time.Time { return now })
}

func mint(t *testing.T, s *Signer) string {
	t.Helper()
	token, err := s.ActionToken(ActionAcknowledge, tenant.String(), alert.String(), channel.String())
	if err != nil {
		t.Fatalf("ActionToken: %v", err)
	}
	return token
}

func TestRoundTrip(t *testing.T) {
	s := signerAt(newKeyring(1), t0)
	token := mint(t, s)
	if len(token) != 96 {
		t.Errorf("token length = %d, want 96", len(token))
	}
	claims, err := s.Verify(token, ActionAcknowledge)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	want := Claims{Action: ActionAcknowledge, TenantID: tenant, AlertID: alert, ChannelID: channel, ExpiresAt: t0.Add(DefaultTTL), KeyVersion: 1}
	if claims != want {
		t.Fatalf("claims = %+v, want %+v", claims, want)
	}
}

func TestTamperedTokenIsInvalid(t *testing.T) {
	s := signerAt(newKeyring(1), t0)
	raw, _ := base64.RawURLEncoding.DecodeString(mint(t, s))
	for i := range raw {
		forged := bytes.Clone(raw)
		forged[i] ^= 0x01
		if _, err := s.Verify(base64.RawURLEncoding.EncodeToString(forged), ActionAcknowledge); !errors.Is(err, ErrInvalid) {
			t.Fatalf("flipping byte %d: err = %v, want ErrInvalid", i, err)
		}
	}
	for _, bad := range []string{"", "not-a-token", mint(t, s) + "A", mint(t, s)[:95]} {
		if _, err := s.Verify(bad, ActionAcknowledge); !errors.Is(err, ErrInvalid) {
			t.Errorf("Verify(%q) err = %v, want ErrInvalid", bad, err)
		}
	}
}

func TestExpiry(t *testing.T) {
	keys := newKeyring(1)
	token := mint(t, signerAt(keys, t0))

	if _, err := signerAt(keys, t0.Add(DefaultTTL-time.Second)).Verify(token, ActionAcknowledge); err != nil {
		t.Fatalf("just before expiry: %v", err)
	}
	claims, err := signerAt(keys, t0.Add(DefaultTTL)).Verify(token, ActionAcknowledge)
	if !errors.Is(err, ErrExpired) {
		t.Fatalf("at expiry: err = %v, want ErrExpired", err)
	}
	if claims.TenantID != tenant || claims.AlertID != alert {
		t.Errorf("an expired authentic token still reports its claims, got %+v", claims)
	}
}

func TestPurposeConfusion(t *testing.T) {
	s := signerAt(newKeyring(1), t0)
	token := mint(t, s)
	if _, err := s.Verify(token, "resolve"); !errors.Is(err, ErrInvalid) {
		t.Fatalf("an ack token verified for another action: err = %v", err)
	}
	if _, err := s.ActionToken("resolve", tenant.String(), alert.String(), channel.String()); err == nil {
		t.Fatal("minted a token for an unknown action")
	}
	// Re-labelling the action byte without the key breaks the MAC.
	raw, _ := base64.RawURLEncoding.DecodeString(token)
	raw[1] = 2
	if _, err := s.Verify(base64.RawURLEncoding.EncodeToString(raw), ActionAcknowledge); !errors.Is(err, ErrInvalid) {
		t.Fatalf("relabelled action: err = %v, want ErrInvalid", err)
	}
}

func TestDerivedKeyIsNotTheRawKey(t *testing.T) {
	// A token signed with the raw secrets key (no HKDF) must not verify: the
	// link key is separated from the encryption key.
	keys := newKeyring(1)
	s := signerAt(keys, t0)
	raw, _ := base64.RawURLEncoding.DecodeString(mint(t, s))
	claims, _ := s.Verify(base64.RawURLEncoding.EncodeToString(raw), ActionAcknowledge)
	copy(raw[headerSize:], sign(keys.keys[1], claims))
	if _, err := s.Verify(base64.RawURLEncoding.EncodeToString(raw), ActionAcknowledge); !errors.Is(err, ErrInvalid) {
		t.Fatalf("raw-key MAC verified: err = %v", err)
	}
}

func TestKeyVersions(t *testing.T) {
	v1 := newKeyring(1)
	old := mint(t, signerAt(v1, t0))

	// Rotation: V2 added, V1 kept. New links use V2, old links still work.
	rotated := newKeyring(1, 2)
	s := signerAt(rotated, t0)
	fresh := mint(t, s)
	claims, err := s.Verify(fresh, ActionAcknowledge)
	if err != nil || claims.KeyVersion != 2 {
		t.Fatalf("fresh token: claims %+v err %v, want key version 2", claims, err)
	}
	if claims, err := s.Verify(old, ActionAcknowledge); err != nil || claims.KeyVersion != 1 {
		t.Fatalf("pre-rotation token after adding V2: claims %+v err %v", claims, err)
	}

	// V1 retired: its links are revoked.
	retired := newKeyring(2)
	if _, err := signerAt(retired, t0).Verify(old, ActionAcknowledge); !errors.Is(err, ErrInvalid) {
		t.Fatalf("token of a removed key version: err = %v, want ErrInvalid", err)
	}

	// A different V1 key (another installation) does not verify it either.
	other := &keyring{keys: map[int][]byte{1: bytes.Repeat([]byte{9}, 32)}, current: 1}
	if _, err := signerAt(other, t0).Verify(old, ActionAcknowledge); !errors.Is(err, ErrInvalid) {
		t.Fatalf("token verified under another installation's key: err = %v", err)
	}
}

func TestNilSignerFailsClosed(t *testing.T) {
	var s *Signer
	if _, err := s.ActionToken(ActionAcknowledge, tenant.String(), alert.String(), channel.String()); !errors.Is(err, ErrNotConfigured) {
		t.Fatalf("nil signer minted: err = %v", err)
	}
	token := mint(t, signerAt(newKeyring(1), t0))
	if _, err := s.Verify(token, ActionAcknowledge); !errors.Is(err, ErrInvalid) {
		t.Fatalf("nil signer verified: err = %v", err)
	}
}

func TestMintNeedsAllIDs(t *testing.T) {
	s := signerAt(newKeyring(1), t0)
	for _, ids := range [][3]string{
		{"", alert.String(), channel.String()},
		{tenant.String(), "nope", channel.String()},
		{tenant.String(), alert.String(), ""},
		{tenant.String(), alert.String(), uuid.Nil.String()},
	} {
		if _, err := s.ActionToken(ActionAcknowledge, ids[0], ids[1], ids[2]); err == nil {
			t.Errorf("minted a token for %v", ids)
		}
	}
}

func TestFromEnv(t *testing.T) {
	t.Setenv("PROBARA_SECRETS_KEY", "")
	s, err := FromEnv()
	if err != nil || s != nil {
		t.Fatalf("no key: signer %v err %v, want nil, nil", s, err)
	}
	t.Setenv("PROBARA_SECRETS_KEY", base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{7}, 32)))
	s, err = FromEnv()
	if err != nil || s == nil {
		t.Fatalf("with key: signer %v err %v", s, err)
	}
	if _, err := s.Verify(mint(t, s), ActionAcknowledge); err != nil {
		t.Fatalf("env signer round trip: %v", err)
	}
	t.Setenv("PROBARA_SECRETS_KEY", "short")
	if _, err := FromEnv(); err == nil {
		t.Fatal("malformed key accepted")
	}
}
