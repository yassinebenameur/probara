package secrets

import (
	"regexp"
	"sort"
	"strings"
	"unicode"
)

// sensitiveVariableWords and sensitiveVariablePairs decide which synthetic
// variable names hold a credential. Unlike header names, a variable name
// usually describes something about a credential (`token_url`,
// `password_reset_url`, a `forgot_password_link` selector), so only the name's
// LAST segment counts: the name is split on `_`, `.`, `-`, spaces and
// camelCase boundaries, and it is a credential when that segment is one of
// the words, or the last two segments are one of the pairs (`api_key`,
// `apiKey`). This is the one Go source of the rule; its TS twin is
// isSensitiveVariableName in web/lib/monitor-form/synthetic.ts. Both test
// suites assert equality with testdata/sensitive_variable_names.json.
var sensitiveVariableWords = []string{"password", "passphrase", "secret", "token", "apikey"}

var sensitiveVariablePairs = [][2]string{{"api", "key"}}

// IsSensitiveVariableName reports whether a synthetic variable name names a
// credential (`password`, `api_key`, `access_token`, `client_secret`,
// `authToken`) rather than something about one (`token_url`, `token_type`,
// `password_field`).
func IsSensitiveVariableName(name string) bool {
	segments := variableNameSegments(name)
	if len(segments) == 0 {
		return false
	}
	last := segments[len(segments)-1]
	for _, word := range sensitiveVariableWords {
		if last == word {
			return true
		}
	}
	if len(segments) >= 2 {
		for _, pair := range sensitiveVariablePairs {
			if segments[len(segments)-2] == pair[0] && last == pair[1] {
				return true
			}
		}
	}
	return false
}

// variableNameSegments lowercases name and splits it on `_`, `.`, `-`,
// spaces, a lower-to-upper case change (`authToken`) and the end of an
// upper-case run followed by a lower-case letter (`APIKey` is api, key).
func variableNameSegments(name string) []string {
	runes := []rune(strings.TrimSpace(name))
	var segments []string
	var current []rune
	flush := func() {
		if len(current) > 0 {
			segments = append(segments, string(current))
			current = current[:0]
		}
	}
	for i, r := range runes {
		if r == '_' || r == '.' || r == '-' || unicode.IsSpace(r) {
			flush()
			continue
		}
		if unicode.IsUpper(r) && len(current) > 0 {
			prev := runes[i-1]
			nextLower := i+1 < len(runes) && unicode.IsLower(runes[i+1])
			if unicode.IsLower(prev) || unicode.IsDigit(prev) || (unicode.IsUpper(prev) && nextLower) {
				flush()
			}
		}
		current = append(current, unicode.ToLower(r))
	}
	flush()
	return segments
}

// syntheticVariableNamePattern is a name a {{ref}} can reach (the worker's
// template syntax). A credential-named variable whose name does not match it
// is never moved: validation refuses such a secret name.
var syntheticVariableNamePattern = regexp.MustCompile(`^[a-zA-Z0-9_.-]+$`)

// movableSensitiveVariables returns the credential-named plain variables
// moveSensitiveVariables may move, and those it must leave plain: a name a
// {{ref}} cannot reach, or one that differs only by case from another secret
// name (a stored secret variable, or another credential-named variable),
// which validation refuses for secret_variables. Both lists are sorted.
func movableSensitiveVariables(variables map[string]any, secretVars map[string]string) (movable, unmovable []string) {
	var candidates []string
	for name, value := range variables {
		if text, isString := value.(string); isString && text != "" && IsSensitiveVariableName(name) {
			candidates = append(candidates, name)
		}
	}
	sort.Strings(candidates)
	folded := map[string]int{}
	for _, name := range candidates {
		folded[strings.ToLower(name)]++
	}
	for _, name := range candidates {
		clash := folded[strings.ToLower(name)] > 1
		for existing := range secretVars {
			if existing != name && strings.EqualFold(existing, name) {
				clash = true
			}
		}
		if clash || !syntheticVariableNamePattern.MatchString(name) {
			unmovable = append(unmovable, name)
		} else {
			movable = append(movable, name)
		}
	}
	return movable, unmovable
}

// UnmovableSensitiveVariables lists the credential-named plain variables of a
// synthetic config that NormalizeSensitiveHeaders leaves in plain text
// because moving them would create a secret name validation refuses. The
// cmd/admin/migrate_sensitive_headers backfill reports them so an operator
// can rename them. Other types, and configs that do not decode, have none.
func UnmovableSensitiveVariables(monitorType string, raw []byte) []string {
	if monitorType != "synthetic_api" && monitorType != "synthetic_browser" {
		return nil
	}
	cfg, err := unmarshalConfigMap(raw)
	if err != nil {
		return nil
	}
	variables, _ := cfg["variables"].(map[string]any)
	secretVars, _ := stringMap(cfg["secret_variables"])
	_, unmovable := movableSensitiveVariables(variables, secretVars)
	return unmovable
}
