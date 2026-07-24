package secrets

import (
	"encoding/json"
	"fmt"
	"strings"
)

// MonitorSecretFields maps a monitor type to the top-level config keys that
// hold secrets. Types not listed here have no secret fields and their configs
// pass through every helper unchanged. Keep this in sync with the config
// structs in shared/models and the validators in api/internal/validation.
// connection_string is secret in its entirety because URIs commonly embed
// credentials (redis://user:pass@host, postgres://user:pass@host/db, …);
// tls_client_key_pem is the private half of an mTLS client pair.
var MonitorSecretFields = map[string][]string{
	"redis":    {"password", "connection_string", "tls_client_key_pem"},
	"postgres": {"password", "connection_string", "tls_client_key_pem"},
	"mongodb":  {"password", "connection_string", "tls_client_key_pem"},
	"rabbitmq": {"password", "connection_string", "tls_client_key_pem"},
	"mysql":    {"password", "connection_string", "tls_client_key_pem"},
	"sip":      {"password"},
}

// MonitorSecretMapFields maps monitor types to config objects whose values are
// all secrets. WebSocket handshake headers are protected as a unit because
// credentials are not limited to Authorization: Cookie and arbitrary custom
// headers routinely carry bearer tokens too. Map keys remain visible so a
// client can edit the write-only values individually.
var MonitorSecretMapFields = map[string][]string{
	"websocket": {"headers"},
}

// HasMonitorSecrets reports whether a monitor type carries secret config fields.
func HasMonitorSecrets(monitorType string) bool {
	return len(MonitorSecretFields[monitorType]) > 0 || len(MonitorSecretMapFields[monitorType]) > 0
}

// EncryptMonitorConfig encrypts the secret fields of a monitor config. Values
// that already look like ciphertext envelopes (e.g. preserved on update) pass
// through unchanged. Configs for types without secret fields are returned as-is.
func EncryptMonitorConfig(enc Encryptor, monitorType string, raw json.RawMessage) (json.RawMessage, error) {
	return transformMonitorSecrets(monitorType, raw, func(field, value string) (string, error) {
		if value == "" || LooksLikeEnvelope(value) {
			return value, nil
		}
		ct, err := enc.Encrypt(value)
		if err != nil {
			return "", fmt.Errorf("encrypt field %q: %w", field, err)
		}
		return ct, nil
	})
}

// DecryptMonitorConfig decrypts the secret fields of a monitor config. Plain
// values pass through, which tolerates rows written before encryption was
// configured.
func DecryptMonitorConfig(enc Encryptor, monitorType string, raw json.RawMessage) (json.RawMessage, error) {
	return transformMonitorSecrets(monitorType, raw, func(field, value string) (string, error) {
		if value == "" {
			return value, nil
		}
		plain, err := enc.Decrypt(value)
		if err != nil {
			return "", fmt.Errorf("decrypt field %q: %w", field, err)
		}
		return plain, nil
	})
}

// MaskMonitorConfig replaces every non-empty secret field with MaskedSecret so
// plaintext (or ciphertext) never reaches API clients.
func MaskMonitorConfig(monitorType string, raw json.RawMessage) (json.RawMessage, error) {
	return transformMonitorSecrets(monitorType, raw, func(_, value string) (string, error) {
		if value == "" {
			return value, nil
		}
		return MaskedSecret, nil
	})
}

// MergeMonitorConfigSecrets resolves write-only secret placeholders in
// `incoming` against the previously stored config. Exactly the MaskedSecret
// placeholder ("***") means "keep the stored value" — the preserved value is
// the stored ciphertext, which EncryptMonitorConfig later passes through.
// Anything else is taken literally: monitor updates replace the whole config,
// so an absent or empty secret field clears it (this is what lets the UI
// switch between connection-string and discrete-field modes without a stale
// secret surviving the switch). Empty strings are dropped for cleanliness.
func MergeMonitorConfigSecrets(monitorType string, incoming, existing json.RawMessage) (json.RawMessage, error) {
	fields := MonitorSecretFields[monitorType]
	mapFields := MonitorSecretMapFields[monitorType]
	if (len(fields) == 0 && len(mapFields) == 0) || len(incoming) == 0 {
		return incoming, nil
	}

	in, err := unmarshalConfigMap(incoming)
	if err != nil {
		return nil, err
	}
	prev := map[string]any{}
	if len(existing) > 0 {
		if prev, err = unmarshalConfigMap(existing); err != nil {
			return nil, err
		}
	}

	for _, field := range fields {
		v, present := in[field]
		if !present {
			continue
		}
		s, ok := v.(string)
		if !ok {
			continue
		}
		switch s {
		case MaskedSecret:
			if prevVal, ok := prev[field]; ok {
				in[field] = prevVal
			} else {
				delete(in, field)
			}
		case "":
			delete(in, field)
		}
	}

	for _, field := range mapFields {
		incomingValues, ok := stringMap(in[field])
		if !ok {
			continue
		}
		previousValues, _ := stringMap(prev[field])
		for key, value := range incomingValues {
			switch value {
			case MaskedSecret:
				if previous, found := lookupFold(previousValues, key); found {
					incomingValues[key] = previous
				} else {
					delete(incomingValues, key)
				}
			case "":
				delete(incomingValues, key)
			}
		}
		if len(incomingValues) == 0 {
			delete(in, field)
		} else {
			in[field] = incomingValues
		}
	}

	return json.Marshal(in)
}

// transformMonitorSecrets applies fn to each secret string field of the config.
// Non-string secret values are left untouched (validation rejects them upstream).
func transformMonitorSecrets(monitorType string, raw json.RawMessage, fn func(field, value string) (string, error)) (json.RawMessage, error) {
	fields := MonitorSecretFields[monitorType]
	mapFields := MonitorSecretMapFields[monitorType]
	if (len(fields) == 0 && len(mapFields) == 0) || len(raw) == 0 {
		return raw, nil
	}

	cfg, err := unmarshalConfigMap(raw)
	if err != nil {
		return nil, err
	}

	changed := false
	for _, field := range fields {
		v, ok := cfg[field]
		if !ok {
			continue
		}
		s, ok := v.(string)
		if !ok {
			continue
		}
		out, err := fn(field, s)
		if err != nil {
			return nil, err
		}
		if out != s {
			cfg[field] = out
			changed = true
		}
	}
	for _, field := range mapFields {
		values, ok := stringMap(cfg[field])
		if !ok {
			continue
		}
		for key, value := range values {
			out, err := fn(field+"."+key, value)
			if err != nil {
				return nil, err
			}
			if out != value {
				values[key] = out
				changed = true
			}
		}
		cfg[field] = values
	}

	if !changed {
		return raw, nil
	}
	return json.Marshal(cfg)
}

func stringMap(value any) (map[string]string, bool) {
	if value == nil {
		return nil, false
	}
	switch values := value.(type) {
	case map[string]string:
		return values, true
	case map[string]any:
		out := make(map[string]string, len(values))
		for key, value := range values {
			text, ok := value.(string)
			if !ok {
				return nil, false
			}
			out[key] = text
		}
		return out, true
	default:
		return nil, false
	}
}

func lookupFold(values map[string]string, key string) (string, bool) {
	for candidate, value := range values {
		if candidate == key || strings.EqualFold(candidate, key) {
			return value, true
		}
	}
	return "", false
}

func unmarshalConfigMap(raw json.RawMessage) (map[string]any, error) {
	var cfg map[string]any
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return nil, fmt.Errorf("secrets: decode monitor config: %w", err)
	}
	if cfg == nil {
		cfg = map[string]any{}
	}
	return cfg, nil
}
