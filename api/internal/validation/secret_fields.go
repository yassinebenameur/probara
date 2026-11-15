package validation

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"github.com/yassinebenameur/probara/api/internal/models"
	sharedmodels "github.com/yassinebenameur/probara/shared/models"
	"github.com/yassinebenameur/probara/shared/secrets"
)

// Rules for the plain/secret map pairs (http headers, grpc metadata, synthetic
// variables) registered in shared/secrets. Validation sees the submitted
// config before the write-only merge, so a secret value may be the "***"
// placeholder; every rule here reads only names, line breaks and presence,
// which a placeholder never trips.

// validateHeaderPair checks a plain map and its secret twin: non-empty names,
// no CR/LF anywhere (a header injection vector), and no name in both maps
// (case-insensitive, as HTTP and gRPC names are).
func validateHeaderPair(plainField string, plain map[string]string, secretField string, secret map[string]string) error {
	if err := validateHeaderMap(plainField, plain); err != nil {
		return err
	}
	if err := validateHeaderMap(secretField, secret); err != nil {
		return err
	}
	for name := range secret {
		for other := range plain {
			if strings.EqualFold(strings.TrimSpace(name), strings.TrimSpace(other)) {
				return fmt.Errorf("%q is set in both %s and %s; keep it in one", strings.TrimSpace(name), plainField, secretField)
			}
		}
	}
	return nil
}

func validateHeaderMap(field string, values map[string]string) error {
	seen := make(map[string]string, len(values))
	for name, value := range values {
		trimmed := strings.TrimSpace(name)
		if trimmed == "" {
			return fmt.Errorf("%s names cannot be empty", field)
		}
		if strings.ContainsAny(name, "\r\n") || strings.ContainsAny(value, "\r\n") {
			return fmt.Errorf("%s %q cannot contain line breaks", field, trimmed)
		}
		// The worker sets each name with Header.Set, so two spellings of one
		// name would silently send only one of the values.
		folded := strings.ToLower(trimmed)
		if prev, dup := seen[folded]; dup {
			return fmt.Errorf("%s %q and %q name the same header; keep one", field, prev, trimmed)
		}
		seen[folded] = trimmed
	}
	return nil
}

// validateHTTPCredentials applies the header-pair rules to an http config and
// refuses basic auth beside an explicit Authorization header: the worker
// would send only one of them, silently.
func validateHTTPCredentials(config sharedmodels.HTTPMonitorConfig) error {
	if err := validateHeaderPair("headers", config.Headers, "secret_headers", config.SecretHeaders); err != nil {
		return err
	}
	if strings.ContainsAny(config.BasicAuthUsername, "\r\n") || strings.ContainsAny(config.BasicAuthPassword, "\r\n") {
		return fmt.Errorf("basic auth credentials cannot contain line breaks")
	}
	if strings.Contains(config.BasicAuthUsername, ":") {
		return fmt.Errorf("basic_auth_username cannot contain ':'")
	}
	if config.BasicAuthUsername == "" && config.BasicAuthPassword != "" {
		return fmt.Errorf("basic_auth_password requires basic_auth_username")
	}
	if config.BasicAuthUsername != "" {
		for _, headers := range []map[string]string{config.Headers, config.SecretHeaders} {
			for name := range headers {
				if strings.EqualFold(strings.TrimSpace(name), "Authorization") {
					return fmt.Errorf("basic auth and an Authorization header cannot both be set")
				}
			}
		}
	}
	return nil
}

// grpcMetadataKey is the gRPC metadata key alphabet (lowercase is canonical;
// grpc-go lowercases keys, so upper case is accepted).
var grpcMetadataKey = regexp.MustCompile(`^[0-9A-Za-z_.-]+$`)

// validateGRPCMetadata applies the header-pair rules to grpc metadata, plus
// gRPC's own key rules: its key alphabet and no reserved grpc- prefix.
func validateGRPCMetadata(config sharedmodels.GRPCMonitorConfig) error {
	if err := validateHeaderPair("metadata", config.Metadata, "secret_metadata", config.SecretMetadata); err != nil {
		return err
	}
	for _, values := range []map[string]string{config.Metadata, config.SecretMetadata} {
		for name := range values {
			key := strings.TrimSpace(name)
			if !grpcMetadataKey.MatchString(key) {
				return fmt.Errorf("metadata key %q may only contain letters, digits, '-', '_' and '.'", key)
			}
			if strings.HasPrefix(strings.ToLower(key), "grpc-") {
				return fmt.Errorf("metadata key %q uses the reserved grpc- prefix", key)
			}
		}
	}
	return nil
}

// syntheticVariableName is a name a {{ref}} can reach: the worker's template
// syntax (syntheticTemplateRegex in worker/internal/worker/synthetic_api_checker.go).
var syntheticVariableName = regexp.MustCompile(`^[a-zA-Z0-9_.-]+$`)

// validateSyntheticVariables checks a variables/secret_variables pair: secret
// names are usable in a {{ref}}, and no name is in both maps (the worker
// would silently pick one).
func validateSyntheticVariables(variables, secretVariables map[string]string) error {
	for name := range secretVariables {
		trimmed := strings.TrimSpace(name)
		if trimmed == "" {
			return fmt.Errorf("secret_variables cannot contain empty keys")
		}
		if trimmed != name || !syntheticVariableName.MatchString(name) {
			return fmt.Errorf("secret_variables key %q may only contain letters, digits, '_', '.' and '-'", trimmed)
		}
		if _, clash := variables[name]; clash {
			return fmt.Errorf("%q is set in both variables and secret_variables; keep it in one", name)
		}
	}
	return nil
}

// validateSecretKeySpelling refuses a top-level config key that matches one of
// the type's registered secret fields (shared/secrets MonitorSecretFields /
// MonitorSecretMapFields) case-insensitively but not exactly. Config structs
// decode case-insensitively, so the validators and the worker would honour
// `Basic_Auth_Password`, while encryption, masking and the write-only merge
// match keys exactly and would store and return it in plaintext. Generic over
// the registry, so a newly registered secret is covered without code here.
func validateSecretKeySpelling(monitorType models.MonitorType, config json.RawMessage) error {
	if !secrets.HasMonitorSecrets(string(monitorType)) || len(config) == 0 {
		return nil
	}
	var keys map[string]json.RawMessage
	if err := json.Unmarshal(config, &keys); err != nil {
		return nil // not an object: the type's validator reports it
	}
	fields := append(append([]string(nil), secrets.MonitorSecretFields[string(monitorType)]...),
		secrets.MonitorSecretMapFields[string(monitorType)]...)
	for key := range keys {
		for _, field := range fields {
			if key != field && strings.EqualFold(key, field) {
				return fmt.Errorf("config key %q must be spelled %q", key, field)
			}
		}
	}
	return nil
}

// ValidateMergedSecretPairs re-runs the plain/secret pair rules on a config
// after the write-only merge has restored stored secrets. Validation proper
// sees only the submitted config, where an omitted secret map or password
// reads as absent; the merge then brings the stored one back, which can pair
// it with something the request added (basic auth beside a kept
// Authorization header, a plain header beside a kept secret one). Values may
// be ciphertext envelopes here; the rules only read names, line breaks and
// presence.
func ValidateMergedSecretPairs(monitorType models.MonitorType, config json.RawMessage) error {
	switch monitorType {
	case models.MonitorTypeHTTP:
		var cfg sharedmodels.HTTPMonitorConfig
		if err := json.Unmarshal(config, &cfg); err != nil {
			return nil
		}
		return validateHTTPCredentials(cfg)
	case models.MonitorTypeGRPC:
		var cfg sharedmodels.GRPCMonitorConfig
		if err := json.Unmarshal(config, &cfg); err != nil {
			return nil
		}
		return validateHeaderPair("metadata", cfg.Metadata, "secret_metadata", cfg.SecretMetadata)
	case models.MonitorTypeSyntheticAPI:
		var cfg sharedmodels.SyntheticAPIMonitorConfig
		if err := json.Unmarshal(config, &cfg); err != nil {
			return nil
		}
		return validateSyntheticVariables(cfg.Variables, cfg.SecretVariables)
	}
	return nil
}
