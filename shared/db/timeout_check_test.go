package db_test

import (
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"testing"

	"github.com/yassinebenameur/probara/shared/models"
)

// timeoutCheckRE matches the passive-exempt shape of monitors_timeout_check
// (migration 000099): the passive types named, every other type held to
// 0 < timeout < interval. Group 1 is the quoted passive-type list.
var timeoutCheckRE = regexp.MustCompile(`(?is)ADD\s+CONSTRAINT\s+monitors_timeout_check\s+CHECK\s*\(\s*type\s+IN\s*\(([^)]*)\)\s*OR\s*\(\s*timeout_seconds\s*>\s*0\s+AND\s+timeout_seconds\s*<\s*interval_seconds\s*\)\s*\)`)

// The database exempts the passive types from the timeout rule by name, and
// the Go side partitions types through shared/models.PassiveMonitorTypes
// (TestRegistryTypesArePartitionedActiveOrPassive in api/internal/validation).
// If the two lists drift, the database either refuses a passive type's
// monitors or lets an active type store a timeout the worker cannot honour.
// The latest up migration that defines the constraint must also keep the
// passive-exempt shape: going back to listing active types would make every
// new active type need a migration again.
func TestTimeoutCheckPassiveTypesMatchGo(t *testing.T) {
	_, file, _, _ := runtime.Caller(0)
	dir := filepath.Join(filepath.Dir(file), "migrations")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var latest, body string
	for _, e := range entries {
		name := e.Name()
		if !strings.HasSuffix(name, ".up.sql") {
			continue
		}
		b, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatal(err)
		}
		// Names start with the zero-padded number, so lexical order is
		// migration order and the last match is the definition in force.
		if strings.Contains(string(b), "ADD CONSTRAINT monitors_timeout_check") && name > latest {
			latest, body = name, string(b)
		}
	}
	if latest == "" {
		t.Fatal("no up migration defines monitors_timeout_check")
	}
	m := timeoutCheckRE.FindStringSubmatch(body)
	if m == nil {
		t.Fatalf("%s: monitors_timeout_check must keep the shape `type IN (<passive types>) OR (timeout_seconds > 0 AND timeout_seconds < interval_seconds)`", latest)
	}
	var inSQL []string
	for _, item := range strings.Split(m[1], ",") {
		inSQL = append(inSQL, strings.Trim(strings.TrimSpace(item), "'"))
	}
	inGo := append([]string(nil), models.PassiveMonitorTypes...)
	sort.Strings(inSQL)
	sort.Strings(inGo)
	if strings.Join(inSQL, ",") != strings.Join(inGo, ",") {
		t.Fatalf("%s exempts passive types %v, shared/models.PassiveMonitorTypes is %v", latest, inSQL, inGo)
	}
}
