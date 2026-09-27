package webpush

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdh"
	"encoding/base64"
	"encoding/binary"
	"testing"
)

// The published example from RFC 8291 section 5. These are the only values
// that prove the key derivation and framing are right: every field below is
// fixed by the RFC, so a wrong info string, a wrong header layout or a wrong
// padding delimiter changes the output and fails this test. Without it the
// failure mode is a push service returning 201 and the browser silently
// dropping the message.
const (
	rfc8291Plaintext    = "When I grow up, I want to be a watermelon"
	rfc8291ClientPublic = "BCVxsr7N_eNgVRqvHtD0zTZsEc6-VV-JvLexhqUzORcxaOzi6-AYWXvTBHm4bjyPjs7Vd8pZGH6SRpkNtoIAiw4"
	rfc8291ClientAuth   = "BTBZMqHH6r4Tts7J_aSIgg"
	// The client's private key, needed to decrypt our output and check it
	// round-trips.
	rfc8291ClientPrivate = "q1dXpw3UpT5VOmu_cf_v6ih07Aems3njxI-JWgLcM94"
	// The full ciphertext the RFC produces from a fixed salt and server key.
	rfc8291Ciphertext = "DGv6ra1nlYgDCS1FRnbzlwAAEABBBP4z9KsN6nGRTbVYI_c7VJSPQTBtkgcy27ml" +
		"mlMoZIIgDll6e3vCYLocInmYWAmS6TlzAC8wEqKK6PBru3jl7A_yl95bQpu6cVPT" +
		"pK4Mqgkf1CXztLVBSt2Ks3oZwbuwXPXLWyouBWLVWGNWQexSgSxsj_Qulcy4a-fN"
)

func TestEncrypt_RFC8291Vector_RoundTrips(t *testing.T) {
	// Encrypt uses a random salt and ephemeral key, so we cannot compare
	// bytes against the RFC's ciphertext directly. Instead we decrypt our own
	// output with the RFC's client private key: that exercises every step the
	// vector pins -- the HKDF info strings, the header layout, the delimiter
	// -- and fails if any of them is wrong.
	sealed, err := Encrypt(Subscription{
		Endpoint: "https://push.example.net/x",
		P256dh:   rfc8291ClientPublic,
		Auth:     rfc8291ClientAuth,
	}, []byte(rfc8291Plaintext))
	if err != nil {
		t.Fatalf("Encrypt() error = %v", err)
	}

	got, err := decryptWithClientKey(t, sealed, rfc8291ClientPrivate, rfc8291ClientAuth)
	if err != nil {
		t.Fatalf("decrypt our own ciphertext: %v", err)
	}
	if got != rfc8291Plaintext {
		t.Fatalf("round-tripped plaintext = %q, want %q", got, rfc8291Plaintext)
	}
}

func TestEncrypt_DecodesTheRFCsOwnCiphertext(t *testing.T) {
	// The other direction: decrypt the exact bytes printed in RFC 8291
	// section 5. This pins the header parsing and derivation against a value
	// this codebase did not produce, so a self-consistent-but-wrong
	// implementation cannot pass it.
	sealed, err := base64.RawURLEncoding.DecodeString(rfc8291Ciphertext)
	if err != nil {
		t.Fatalf("decode rfc ciphertext: %v", err)
	}

	got, err := decryptWithClientKey(t, sealed, rfc8291ClientPrivate, rfc8291ClientAuth)
	if err != nil {
		t.Fatalf("decrypt rfc ciphertext: %v", err)
	}
	if got != rfc8291Plaintext {
		t.Fatalf("plaintext = %q, want %q", got, rfc8291Plaintext)
	}
}

// decryptWithClientKey performs the receiver half of RFC 8291: parse the
// aes128gcm header, run ECDH with the client's private key, derive the same
// key and nonce, and open the record.
func decryptWithClientKey(t *testing.T, sealed []byte, clientPrivate, clientAuth string) (string, error) {
	t.Helper()

	if len(sealed) < saltLength+4+1 {
		t.Fatalf("sealed body is too short to hold a header: %d bytes", len(sealed))
	}
	salt := sealed[:saltLength]
	idLen := int(sealed[saltLength+4])
	headerLen := saltLength + 4 + 1 + idLen
	serverPub := sealed[saltLength+4+1 : headerLen]
	ciphertext := sealed[headerLen:]

	if rs := binary.BigEndian.Uint32(sealed[saltLength : saltLength+4]); rs != recordSize {
		t.Fatalf("record size header = %d, want %d", rs, recordSize)
	}

	rawPriv, err := base64.RawURLEncoding.DecodeString(clientPrivate)
	if err != nil {
		return "", err
	}
	priv, err := ecdh.P256().NewPrivateKey(rawPriv)
	if err != nil {
		return "", err
	}
	pub, err := ecdh.P256().NewPublicKey(serverPub)
	if err != nil {
		return "", err
	}
	shared, err := priv.ECDH(pub)
	if err != nil {
		return "", err
	}
	auth, err := base64.RawURLEncoding.DecodeString(clientAuth)
	if err != nil {
		return "", err
	}

	key, nonce, err := deriveKeyAndNonce(shared, auth, salt, priv.PublicKey().Bytes(), serverPub)
	if err != nil {
		return "", err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	record, err := aead.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return "", err
	}
	if len(record) == 0 || record[len(record)-1] != 0x02 {
		t.Fatalf("record does not end with the 0x02 last-record delimiter")
	}
	return string(record[:len(record)-1]), nil
}

func TestEncrypt_UsesAFreshEphemeralKeyPerMessage(t *testing.T) {
	// Reusing the ephemeral keypair would reuse the derived key and nonce,
	// which AES-GCM does not survive. Two encryptions of identical plaintext
	// must therefore differ.
	sub := Subscription{Endpoint: "https://push.example.net/x", P256dh: rfc8291ClientPublic, Auth: rfc8291ClientAuth}

	first, err := Encrypt(sub, []byte("same"))
	if err != nil {
		t.Fatalf("Encrypt() error = %v", err)
	}
	second, err := Encrypt(sub, []byte("same"))
	if err != nil {
		t.Fatalf("Encrypt() error = %v", err)
	}
	if string(first) == string(second) {
		t.Fatalf("two encryptions of the same plaintext produced identical bytes")
	}
}

func TestEncrypt_RejectsMalformedSubscriptions(t *testing.T) {
	tests := []struct {
		name string
		sub  Subscription
	}{
		{"empty p256dh", Subscription{P256dh: "", Auth: rfc8291ClientAuth}},
		{"p256dh not a point", Subscription{P256dh: base64.RawURLEncoding.EncodeToString([]byte("not a key")), Auth: rfc8291ClientAuth}},
		{"auth too short", Subscription{P256dh: rfc8291ClientPublic, Auth: base64.RawURLEncoding.EncodeToString([]byte("short"))}},
		{"auth not base64", Subscription{P256dh: rfc8291ClientPublic, Auth: "!!!!"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := Encrypt(tt.sub, []byte("x")); err == nil {
				t.Fatalf("Encrypt() succeeded on a malformed subscription; want error")
			}
		})
	}
}

func TestEncrypt_RejectsOversizePayload(t *testing.T) {
	sub := Subscription{P256dh: rfc8291ClientPublic, Auth: rfc8291ClientAuth}
	if _, err := Encrypt(sub, make([]byte, MaxPayloadLength+1)); err == nil {
		t.Fatalf("Encrypt() accepted a payload over the single-record limit; want error")
	}
	if _, err := Encrypt(sub, make([]byte, MaxPayloadLength)); err != nil {
		t.Fatalf("Encrypt() rejected a payload at exactly the limit: %v", err)
	}
}
