package pageaccess

import (
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

const testSecret = "0123456789abcdef0123456789abcdef"

func TestVisibilityPredicates(t *testing.T) {
	cases := []struct {
		v                              string
		valid, gated, noindex, pw, tok bool
	}{
		{Public, true, false, false, false, false},
		{Unlisted, true, false, true, false, false},
		{Password, true, true, true, true, true},
		{Private, true, true, true, false, true},
		// Unknown values fail closed.
		{"", false, true, true, false, false},
		{"secret", false, true, true, false, false},
	}
	for _, c := range cases {
		require.Equal(t, c.valid, Valid(c.v), c.v)
		require.Equal(t, c.gated, Gated(c.v), c.v)
		require.Equal(t, c.noindex, NoIndex(c.v), c.v)
		require.Equal(t, c.pw, AcceptsPassword(c.v), c.v)
		require.Equal(t, c.tok, AcceptsTokens(c.v), c.v)
	}
}

func TestPasswordRules(t *testing.T) {
	require.Error(t, ValidatePassword("short"))
	require.Error(t, ValidatePassword(strings.Repeat("a", MaxPasswordBytes+1)))
	require.NoError(t, ValidatePassword("correct horse"))

	hash, err := HashPassword("correct horse")
	require.NoError(t, err)
	require.True(t, CheckPassword(hash, "correct horse"))
	require.False(t, CheckPassword(hash, "wrong horse"))
	require.False(t, CheckPassword("", "correct horse"))
	require.False(t, CheckPassword(hash, ""))
}

func TestTokens(t *testing.T) {
	token, hash, err := NewToken()
	require.NoError(t, err)
	require.True(t, strings.HasPrefix(token, TokenPrefix))
	require.Equal(t, HashToken(token), hash)
	require.NotContains(t, hash, token)
	other, _, err := NewToken()
	require.NoError(t, err)
	require.NotEqual(t, token, other)
}

func TestNewSignerSecretRules(t *testing.T) {
	_, err := NewSigner("")
	require.ErrorIs(t, err, ErrNoSessionSecret)
	_, err = NewSigner("too-short")
	require.Error(t, err)
	s, err := NewSigner(testSecret)
	require.NoError(t, err)
	require.NotNil(t, s)
}

func TestSessionRoundTripAndRejections(t *testing.T) {
	s, err := NewSigner(testSecret)
	require.NoError(t, err)
	page := uuid.New()
	now := time.Now()

	pw := s.Sign(page, Credential{Kind: KindPassword, PasswordVersion: 3}, now.Add(time.Hour))
	got, ok := s.Verify(page, pw, now)
	require.True(t, ok)
	require.Equal(t, Credential{Kind: KindPassword, PasswordVersion: 3}, got)

	tokenID := uuid.New()
	tok := s.Sign(page, Credential{Kind: KindToken, TokenID: tokenID}, now.Add(time.Hour))
	got, ok = s.Verify(page, tok, now)
	require.True(t, ok)
	require.Equal(t, KindToken, got.Kind)
	require.Equal(t, tokenID, got.TokenID)

	// Another page's session does not open this one.
	_, ok = s.Verify(uuid.New(), pw, now)
	require.False(t, ok)
	// Expired.
	_, ok = s.Verify(page, pw, now.Add(2*time.Hour))
	require.False(t, ok)
	// Another secret.
	other, err := NewSigner(strings.Repeat("z", 40))
	require.NoError(t, err)
	_, ok = other.Verify(page, pw, now)
	require.False(t, ok)
	// Tampered payload keeps the old MAC.
	_, mac, _ := strings.Cut(pw, ".")
	forged := s.Sign(page, Credential{Kind: KindPassword, PasswordVersion: 4}, now.Add(time.Hour))
	forgedPayload, _, _ := strings.Cut(forged, ".")
	_, ok = s.Verify(page, forgedPayload+"."+mac, now)
	require.False(t, ok)
	// Garbage and a nil signer.
	for _, v := range []string{"", ".", "abc", "abc.def", pw + "x"} {
		_, ok = s.Verify(page, v, now)
		require.False(t, ok, v)
	}
	var nilSigner *Signer
	_, ok = nilSigner.Verify(page, pw, now)
	require.False(t, ok)
}

func TestCookieNamePerPage(t *testing.T) {
	a, b := uuid.New(), uuid.New()
	require.NotEqual(t, CookieName(a), CookieName(b))
	require.NotContains(t, CookieName(a), "-")
}
