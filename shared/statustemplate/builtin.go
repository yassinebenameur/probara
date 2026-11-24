package statustemplate

import (
	_ "embed"
	"strings"
)

// ClassicSource is the "classic" built-in template: a banner, a component
// list with history bars and incident history by day, drawn with the
// operator UI's tokens. A page selects it with the template setting.
//
//go:embed classic.gohtml
var ClassicSource string

// Built-in template names: the values of a page's template setting and of
// the API's ?name= on GET /status-pages/{id}/template/default.
const (
	BuiltinDefault = "default"
	BuiltinClassic = "classic"
)

// Builtin is one built-in status page template.
type Builtin struct {
	// Name is the stored setting value.
	Name string
	// Label is the name operators see.
	Label string
	// Source is the template source.
	Source string
}

// Builtins is the single list of built-in templates, default first. API
// validation, the template export handler and the renderer all read it, so
// a new built-in is one entry here (plus its embedded file).
func Builtins() []Builtin {
	return []Builtin{
		{Name: BuiltinDefault, Label: "Default", Source: DefaultSource},
		{Name: BuiltinClassic, Label: "Classic", Source: ClassicSource},
	}
}

// BuiltinNames lists the built-in names in registry order.
func BuiltinNames() []string {
	all := Builtins()
	names := make([]string, len(all))
	for i, b := range all {
		names[i] = b.Name
	}
	return names
}

// LookupBuiltin returns the built-in with that name (case-insensitive,
// trimmed). "" is not a name; callers that treat it as the default use
// NormalizeBuiltin.
func LookupBuiltin(name string) (Builtin, bool) {
	name = strings.ToLower(strings.TrimSpace(name))
	for _, b := range Builtins() {
		if b.Name == name {
			return b, true
		}
	}
	return Builtin{}, false
}

// NormalizeBuiltin maps a stored or submitted setting to a built-in name:
// a known name in its canonical form, anything else (empty, unknown, a row
// written by a newer release) the default.
func NormalizeBuiltin(name string) string {
	if b, ok := LookupBuiltin(name); ok {
		return b.Name
	}
	return BuiltinDefault
}
