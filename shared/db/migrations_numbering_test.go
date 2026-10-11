package db_test

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"testing"
)

// migrationFileRE is the golang-migrate naming contract for this repo:
// NNNNNN_snake_name.{up,down}.sql. golang-migrate keys on the number alone,
// so two names sharing a number (two branches taking the same "next free"
// number) would make one of them silently unreachable.
var migrationFileRE = regexp.MustCompile(`^([0-9]{6})_([a-z0-9]+(?:_[a-z0-9]+)*)\.(up|down)\.sql$`)

// checkMigrationNumbering returns one message per violation in a migrations
// directory listing: badly named files, a number claimed by more than one
// name, and an up without its down (or vice versa). Gaps in the sequence are
// deliberately allowed — 000019 was never shipped.
func checkMigrationNumbering(files []string) []string {
	var problems []string
	namesByNumber := map[string]map[string]bool{}
	directions := map[string]map[string]bool{} // "NNNNNN_name" -> {"up","down"}
	for _, f := range files {
		m := migrationFileRE.FindStringSubmatch(f)
		if m == nil {
			problems = append(problems, fmt.Sprintf("%s: does not match NNNNNN_name.{up,down}.sql", f))
			continue
		}
		num, name, dir := m[1], m[2], m[3]
		if namesByNumber[num] == nil {
			namesByNumber[num] = map[string]bool{}
		}
		namesByNumber[num][name] = true
		key := num + "_" + name
		if directions[key] == nil {
			directions[key] = map[string]bool{}
		}
		directions[key][dir] = true
	}
	for num, names := range namesByNumber {
		if len(names) > 1 {
			problems = append(problems, fmt.Sprintf("%s: number used by %d names: %s", num, len(names), strings.Join(sortedKeys(names), ", ")))
		}
	}
	for key, dirs := range directions {
		for _, want := range []string{"up", "down"} {
			if !dirs[want] {
				problems = append(problems, fmt.Sprintf("%s: missing %s.sql", key, want))
			}
		}
	}
	sort.Strings(problems)
	return problems
}

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func TestMigrationNumbering(t *testing.T) {
	_, file, _, _ := runtime.Caller(0)
	entries, err := os.ReadDir(filepath.Join(filepath.Dir(file), "migrations"))
	if err != nil {
		t.Fatal(err)
	}
	var files []string
	for _, e := range entries {
		if !e.IsDir() {
			files = append(files, e.Name())
		}
	}
	if len(files) == 0 {
		t.Fatal("no migration files found")
	}
	for _, p := range checkMigrationNumbering(files) {
		t.Error(p)
	}
}

func TestMigrationNumberingChecker(t *testing.T) {
	cases := []struct {
		name  string
		files []string
		want  []string
	}{
		{
			name:  "valid with gap",
			files: []string{"000001_a.up.sql", "000001_a.down.sql", "000003_b_c.up.sql", "000003_b_c.down.sql"},
		},
		{
			name:  "duplicate number",
			files: []string{"000002_a.up.sql", "000002_a.down.sql", "000002_b.up.sql", "000002_b.down.sql"},
			want:  []string{"000002: number used by 2 names: a, b"},
		},
		{
			name:  "up without down",
			files: []string{"000004_a.up.sql"},
			want:  []string{"000004_a: missing down.sql"},
		},
		{
			name:  "down without up",
			files: []string{"000004_a.down.sql"},
			want:  []string{"000004_a: missing up.sql"},
		},
		{
			name:  "pair split across names",
			files: []string{"000005_a.up.sql", "000005_b.down.sql"},
			want: []string{
				"000005: number used by 2 names: a, b",
				"000005_a: missing down.sql",
				"000005_b: missing up.sql",
			},
		},
		{
			name:  "bad names",
			files: []string{"5_a.up.sql", "000006_Bad.up.sql", "000007_a.sql", "000008_a-b.down.sql"},
			want: []string{
				"000006_Bad.up.sql: does not match NNNNNN_name.{up,down}.sql",
				"000007_a.sql: does not match NNNNNN_name.{up,down}.sql",
				"000008_a-b.down.sql: does not match NNNNNN_name.{up,down}.sql",
				"5_a.up.sql: does not match NNNNNN_name.{up,down}.sql",
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := checkMigrationNumbering(tc.files)
			if strings.Join(got, "\n") != strings.Join(tc.want, "\n") {
				t.Fatalf("got %q\nwant %q", got, tc.want)
			}
		})
	}
}
