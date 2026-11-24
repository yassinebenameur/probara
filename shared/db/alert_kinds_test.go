package db_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/yassinebenameur/probara/shared/notifications"
	"github.com/yassinebenameur/probara/shared/notifications/present"
)

// alertKindCheckRE captures the quoted kind list of an alerts_kind_check
// definition.
var alertKindCheckRE = regexp.MustCompile(`(?is)ADD\s+CONSTRAINT\s+alerts_kind_check\s+CHECK\s*\(\s*kind\s+IN\s*\(([^)]*)\)\s*\)`)

// goAlertKinds returns the values of the Kind* string constants in
// shared/notifications/event.go, read from the source so a constant added
// there is seen without a second hand-kept list.
func goAlertKinds(t *testing.T, dir string) []string {
	t.Helper()
	file, err := parser.ParseFile(token.NewFileSet(), filepath.Join(dir, "..", "notifications", "event.go"), nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	var kinds []string
	for _, decl := range file.Decls {
		gen, ok := decl.(*ast.GenDecl)
		if !ok || gen.Tok != token.CONST {
			continue
		}
		for _, spec := range gen.Specs {
			vs := spec.(*ast.ValueSpec)
			for i, name := range vs.Names {
				if !strings.HasPrefix(name.Name, "Kind") || i >= len(vs.Values) {
					continue
				}
				lit, ok := vs.Values[i].(*ast.BasicLit)
				if !ok || lit.Kind != token.STRING {
					continue
				}
				v, err := strconv.Unquote(lit.Value)
				if err != nil {
					t.Fatal(err)
				}
				kinds = append(kinds, v)
			}
		}
	}
	sort.Strings(kinds)
	return kinds
}

// The alerts_kind_check constraint and the notifications.Kind* constants list
// the same kinds: a kind only in Go cannot be stored, and one only in SQL has
// no constant for the code that raises or renders it. Every kind other than
// availability also needs its own wording in package present, or its
// notifications read as a generic outage.
func TestAlertKindCheckMatchesGo(t *testing.T) {
	_, file, _, _ := runtime.Caller(0)
	dir := filepath.Dir(file)
	migrations := filepath.Join(dir, "migrations")
	entries, err := os.ReadDir(migrations)
	if err != nil {
		t.Fatal(err)
	}
	var latest, body string
	for _, e := range entries {
		name := e.Name()
		if !strings.HasSuffix(name, ".up.sql") {
			continue
		}
		b, err := os.ReadFile(filepath.Join(migrations, name))
		if err != nil {
			t.Fatal(err)
		}
		// Zero-padded numbers sort lexically, so the last match is the
		// definition in force.
		if strings.Contains(string(b), "ADD CONSTRAINT alerts_kind_check") && name > latest {
			latest, body = name, string(b)
		}
	}
	if latest == "" {
		t.Fatal("no up migration defines alerts_kind_check")
	}
	m := alertKindCheckRE.FindStringSubmatch(body)
	if m == nil {
		t.Fatalf("%s: alerts_kind_check must keep the shape `CHECK (kind IN (<kinds>))`", latest)
	}
	var inSQL []string
	for _, item := range strings.Split(m[1], ",") {
		inSQL = append(inSQL, strings.Trim(strings.TrimSpace(item), "'"))
	}
	sort.Strings(inSQL)
	inGo := goAlertKinds(t, dir)
	if strings.Join(inSQL, ",") != strings.Join(inGo, ",") {
		t.Fatalf("%s allows alert kinds %v, shared/notifications Kind* constants are %v", latest, inSQL, inGo)
	}

	generic := map[string]bool{}
	for _, eventType := range []string{present.EventCreated, present.EventReminder, present.EventResolved} {
		ev := notifications.AlertEvent{Alert: notifications.AlertDetails{Kind: notifications.KindAvailability, MonitorName: "m"}}
		generic[eventType+"/"+present.Build(ev, eventType, "").Label] = true
	}
	for _, kind := range inGo {
		if kind == notifications.KindAvailability {
			continue
		}
		for _, eventType := range []string{present.EventCreated, present.EventReminder, present.EventResolved} {
			ev := notifications.AlertEvent{Alert: notifications.AlertDetails{Kind: kind, MonitorName: "m"}}
			if label := present.Build(ev, eventType, "").Label; generic[eventType+"/"+label] {
				t.Errorf("alert kind %s has no %s wording in package present (renders the generic %q)", kind, eventType, label)
			}
		}
	}
}
