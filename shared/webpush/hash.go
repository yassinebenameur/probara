package webpush

import (
	"crypto/sha256"
	"hash"
)

// sha256New is the hash constructor HKDF is instantiated with. RFC 8291
// specifies HMAC-SHA-256 throughout.
func sha256New() hash.Hash { return sha256.New() }
