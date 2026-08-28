package webpush

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdh"
	"crypto/rand"
	"encoding/binary"
	"fmt"
	"io"

	"golang.org/x/crypto/hkdf"
)

const (
	// saltLength and keyLength are fixed by RFC 8188 / RFC 8291.
	saltLength  = 16
	keyLength   = 16 // AES-128
	nonceLength = 12

	// recordSize is the aes128gcm "rs" header field. One record is enough:
	// our payloads are a few hundred bytes and push services cap the whole
	// encrypted body at 4096 bytes anyway.
	recordSize = 4096

	// MaxPayloadLength is the largest plaintext that fits a single record
	// once the 86-byte aes128gcm header, the 0x02 delimiter and the 16-byte
	// GCM tag are accounted for. Push services reject anything larger with
	// 413, so callers should keep payloads far below this.
	MaxPayloadLength = recordSize - 86 - 1 - 16
)

// Subscription is the browser-minted PushSubscription, as delivered by
// pushManager.subscribe().
type Subscription struct {
	Endpoint string
	// P256dh is the client's uncompressed P-256 public point, base64url.
	P256dh string
	// Auth is the client's 16-byte authentication secret, base64url.
	Auth string
}

// Encrypt seals payload for one subscription using the aes128gcm content
// encoding (RFC 8188) with the key derivation of RFC 8291 section 3.3.
//
// The returned bytes are the complete request body: a header block carrying
// the salt, record size and the ephemeral public key, followed by one
// encrypted record.
func Encrypt(sub Subscription, payload []byte) ([]byte, error) {
	if len(payload) > MaxPayloadLength {
		return nil, fmt.Errorf("payload is %d bytes, exceeds the %d-byte single-record limit", len(payload), MaxPayloadLength)
	}

	clientPub, auth, err := sub.parseKeys()
	if err != nil {
		return nil, err
	}

	salt := make([]byte, saltLength)
	if _, err := io.ReadFull(rand.Reader, salt); err != nil {
		return nil, fmt.Errorf("generate salt: %w", err)
	}

	// A fresh ephemeral keypair per message: reusing it across messages would
	// reuse the derived key and nonce, which GCM does not survive.
	serverPriv, err := ecdh.P256().GenerateKey(rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("generate ephemeral key: %w", err)
	}
	serverPub := serverPriv.PublicKey().Bytes()

	shared, err := serverPriv.ECDH(clientPub)
	if err != nil {
		return nil, fmt.Errorf("ecdh with subscription key: %w", err)
	}

	key, nonce, err := deriveKeyAndNonce(shared, auth, salt, clientPub.Bytes(), serverPub)
	if err != nil {
		return nil, err
	}

	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("aes cipher: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("gcm: %w", err)
	}

	// RFC 8188 section 2: every record ends with a padding delimiter. 0x02
	// marks the last record; 0x01 would mark a non-final one. Getting this
	// byte wrong is accepted by the push service and silently dropped by the
	// browser, which is exactly the failure this package exists to avoid.
	record := append(append([]byte{}, payload...), 0x02)
	ciphertext := aead.Seal(nil, nonce, record, nil)

	return append(buildHeader(salt, serverPub), ciphertext...), nil
}

// buildHeader lays out the aes128gcm header of RFC 8188 section 2.1:
// salt (16) || record size (4, big endian) || key id length (1) || key id.
// For Web Push the key id is the server's ephemeral public key.
func buildHeader(salt, serverPub []byte) []byte {
	header := make([]byte, 0, saltLength+4+1+len(serverPub))
	header = append(header, salt...)
	header = binary.BigEndian.AppendUint32(header, recordSize)
	header = append(header, byte(len(serverPub)))
	return append(header, serverPub...)
}

// deriveKeyAndNonce implements RFC 8291 section 3.3.
//
// The two-stage derivation is what makes the client's auth secret bind the
// key to this subscription: the shared ECDH secret is first mixed with the
// auth secret and both public keys, and only then expanded into the content
// encryption key and nonce.
func deriveKeyAndNonce(shared, auth, salt, clientPub, serverPub []byte) (key, nonce []byte, err error) {
	// info = "WebPush: info" || 0x00 || client public || server public
	keyInfo := make([]byte, 0, len("WebPush: info")+1+len(clientPub)+len(serverPub))
	keyInfo = append(keyInfo, []byte("WebPush: info")...)
	keyInfo = append(keyInfo, 0x00)
	keyInfo = append(keyInfo, clientPub...)
	keyInfo = append(keyInfo, serverPub...)

	ikm := make([]byte, 32)
	if _, err := io.ReadFull(hkdf.New(sha256New, shared, auth, keyInfo), ikm); err != nil {
		return nil, nil, fmt.Errorf("derive ikm: %w", err)
	}

	key = make([]byte, keyLength)
	if _, err := io.ReadFull(hkdf.New(sha256New, ikm, salt, []byte("Content-Encoding: aes128gcm\x00")), key); err != nil {
		return nil, nil, fmt.Errorf("derive content encryption key: %w", err)
	}

	nonce = make([]byte, nonceLength)
	if _, err := io.ReadFull(hkdf.New(sha256New, ikm, salt, []byte("Content-Encoding: nonce\x00")), nonce); err != nil {
		return nil, nil, fmt.Errorf("derive nonce: %w", err)
	}

	return key, nonce, nil
}

// parseKeys decodes and validates the browser-supplied subscription keys.
// Validating here means a malformed subscription is rejected at subscribe
// time rather than discovered as an unexplained send failure much later.
func (s Subscription) parseKeys() (*ecdh.PublicKey, []byte, error) {
	rawPub, err := decodeBase64(s.P256dh)
	if err != nil {
		return nil, nil, fmt.Errorf("decode p256dh: %w", err)
	}
	clientPub, err := ecdh.P256().NewPublicKey(rawPub)
	if err != nil {
		return nil, nil, fmt.Errorf("invalid p256dh key: %w", err)
	}

	auth, err := decodeBase64(s.Auth)
	if err != nil {
		return nil, nil, fmt.Errorf("decode auth secret: %w", err)
	}
	if len(auth) != 16 {
		return nil, nil, fmt.Errorf("auth secret must be 16 bytes, got %d", len(auth))
	}

	return clientPub, auth, nil
}
