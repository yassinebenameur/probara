package validation

import (
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/yassinebenameur/probara/api/internal/models"
	sharedmodels "github.com/yassinebenameur/probara/shared/models"
	"github.com/yassinebenameur/probara/shared/secrets"
)

// Rules for the plain/secret map pairs (http headers, grpc metadata, synthetic
// variables) registered in shared/secrets. Validation sees the submitted
// config before the write-only merge, so a secret value may be the "***"
// placeholder; every rule here only reads names, CR/LF and references, which
// a placeholder never trips.

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
	for name, value := range values {
		if strings.TrimSpace(name) == "" {
			return fmt.Errorf("%s names cannot be empty", field)
		}
		if strings.ContainsAny(name, "\r\n") || strings.ContainsAny(value, "\r\n") {
			return fmt.Errorf("%s %q cannot contain line breaks", field, strings.TrimSpace(name))
		}
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
// gRPC's own key rules: its key alphabet, no reserved grpc- prefix, and no
// pseudo-headers.
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

// syntheticVariableRef is the worker's template syntax
// (worker/internal/worker/synthetic_api_checker.go syntheticTemplateRegex).
var syntheticVariableRef = regexp.MustCompile(`\{\{\s*([a-zA-Z0-9_.-]+)\s*\}\}`)

// validateSyntheticVariables checks a variables/secret_variables pair: names
// are non-empty and usable in a {{ref}}, and no name is in both maps (the
// worker would silently pick one).
func validateSyntheticVariables(variables, secretVariables map[string]string) error {
	for name := range secretVariables {
		trimmed := strings.TrimSpace(name)
		if trimmed == "" {
			return fmt.Errorf("secret_variables cannot contain empty keys")
		}
		if !syntheticVariableName.MatchString(trimmed) {
			return fmt.Errorf("secret_variables key %q may only contain letters, digits, '_', '.' and '-'", trimmed)
		}
		if _, clash := variables[name]; clash {
			return fmt.Errorf("%q is set in both variables and secret_variables; keep it in one", trimmed)
		}
	}
	return nil
}

var syntheticVariableName = regexp.MustCompile(`^[a-zA-Z0-9_.-]+$`)

// undefinedSyntheticRefs returns, sorted, the {{name}} references in texts
// that no defined name satisfies.
func undefinedSyntheticRefs(defined map[string]bool, texts ...string) []string {
	missing := map[string]bool{}
	for _, text := range texts {
		for _, match := range syntheticVariableRef.FindAllStringSubmatch(text, -1) {
			if name := strings.TrimSpace(match[1]); !defined[name] {
				missing[name] = true
			}
		}
	}
	out := make([]string, 0, len(missing))
	for name := range missing {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

func definedSyntheticNames(variables, secretVariables map[string]string) map[string]bool {
	defined := make(map[string]bool, len(variables)+len(secretVariables))
	for name := range variables {
		defined[strings.TrimSpace(name)] = true
	}
	for name := range secretVariables {
		defined[strings.TrimSpace(name)] = true
	}
	return defined
}

func undefinedRefError(where string, missing []string) error {
	return fmt.Errorf("%s references undefined variable %q (define it in variables or secret_variables; on update, send stored secret_variables keys with \"***\")", where, missing[0])
}

// validateSyntheticAPIRefs refuses a {{ref}} no variable, secret variable or
// extraction defines. An extraction counts wherever it sits, since a step
// order mistake surfaces in the first test run anyway.
func validateSyntheticAPIRefs(config sharedmodels.SyntheticAPIMonitorConfig) error {
	defined := definedSyntheticNames(config.Variables, config.SecretVariables)
	for _, step := range config.Steps {
		for _, extract := range step.Extract {
			defined[strings.TrimSpace(extract.Name)] = true
		}
	}
	if missing := undefinedSyntheticRefs(defined, config.BaseURL); len(missing) > 0 {
		return undefinedRefError("base_url", missing)
	}
	for i, step := range config.Steps {
		texts := []string{step.Request.URL}
		if step.Request.Body != nil {
			texts = append(texts, *step.Request.Body)
		}
		for _, value := range step.Request.Headers {
			texts = append(texts, value)
		}
		if missing := undefinedSyntheticRefs(defined, texts...); len(missing) > 0 {
			return undefinedRefError(fmt.Sprintf("steps[%d]", i), missing)
		}
	}
	return nil
}

// validateSyntheticBrowserRefs is validateSyntheticAPIRefs for browser
// journeys, whose steps interpolate start_url, url, selector and value.
func validateSyntheticBrowserRefs(config sharedmodels.SyntheticBrowserMonitorConfig) error {
	defined := definedSyntheticNames(config.Variables, config.SecretVariables)
	if missing := undefinedSyntheticRefs(defined, config.StartURL); len(missing) > 0 {
		return undefinedRefError("start_url", missing)
	}
	for i, step := range config.Steps {
		texts := []string{step.URL, step.Selector}
		if step.Value != nil {
			texts = append(texts, *step.Value)
		}
		if missing := undefinedSyntheticRefs(defined, texts...); len(missing) > 0 {
			return undefinedRefError(fmt.Sprintf("steps[%d]", i), missing)
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
