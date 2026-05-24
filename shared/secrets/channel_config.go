package secrets

import (
	"fmt"

	"github.com/yassinebenameur/probara/shared/notifications/plugin"
)

// MaskedSecret is what API responses substitute for secret fields so the
// frontend can show a placeholder without ever round-tripping ciphertext.
const MaskedSecret = "***"

// EncryptConfig walks the plugin manifest and encrypts any field marked
// Secret. Non-secret fields pass through unchanged. The input is not mutated.
func EncryptConfig(enc Encryptor, manifest plugin.Manifest, in map[string]any) (map[string]any, error) {
	out := copyMap(in)
	for _, f := range manifest.Fields {
		if !f.Secret {
			continue
		}
		v, ok := out[f.Key]
		if !ok {
			continue
		}
		s, ok := v.(string)
		if !ok || s == "" {
			continue
		}
		if LooksLikeEnvelope(s) {
			// Already encrypted (e.g. a passthrough on Update) — leave as-is.
			continue
		}
		ct, err := enc.Encrypt(s)
		if err != nil {
			return nil, fmt.Errorf("encrypt field %q: %w", f.Key, err)
		}
		out[f.Key] = ct
	}
	return out, nil
}

// DecryptConfig walks the plugin manifest and decrypts any secret field that
// looks like a ciphertext envelope. Plain values pass through, which keeps
// the alerter tolerant of rows that haven't been encrypted yet.
func DecryptConfig(enc Encryptor, manifest plugin.Manifest, in map[string]any) (map[string]any, error) {
	out := copyMap(in)
	for _, f := range manifest.Fields {
		if !f.Secret {
			continue
		}
		v, ok := out[f.Key]
		if !ok {
			continue
		}
		s, ok := v.(string)
		if !ok || s == "" {
			continue
		}
		plain, err := enc.Decrypt(s)
		if err != nil {
			return nil, fmt.Errorf("decrypt field %q: %w", f.Key, err)
		}
		out[f.Key] = plain
	}
	return out, nil
}

// MaskConfig replaces every secret field's value with MaskedSecret. Use this
// on API responses so plaintext never reaches the browser.
func MaskConfig(manifest plugin.Manifest, in map[string]any) map[string]any {
	out := copyMap(in)
	for _, f := range manifest.Fields {
		if !f.Secret {
			continue
		}
		if _, ok := out[f.Key]; ok {
			out[f.Key] = MaskedSecret
		}
	}
	return out
}

// MergePreserveSecrets fills in any missing secret fields in `incoming` from
// `existing`. The frontend submits empty secret fields when the user wants to
// keep the current value; this helper makes that semantics concrete on the
// backend.
func MergePreserveSecrets(manifest plugin.Manifest, incoming, existing map[string]any) map[string]any {
	out := copyMap(incoming)
	for _, f := range manifest.Fields {
		if !f.Secret {
			continue
		}
		v, present := out[f.Key]
		if present {
			s, ok := v.(string)
			if ok && s != "" && s != MaskedSecret {
				continue
			}
		}
		if prev, ok := existing[f.Key]; ok {
			out[f.Key] = prev
		} else {
			delete(out, f.Key)
		}
	}
	return out
}

func copyMap(in map[string]any) map[string]any {
	if in == nil {
		return map[string]any{}
	}
	out := make(map[string]any, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}
