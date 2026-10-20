// Command seed-demo builds "Northwind", a realistic demo tenant for judging the
// operator UI and status pages against lived-in data, and keeps it alive.
//
//	go run ./scripts/seed-demo seed      # (re)create the tenant + 110 days of history
//	go run ./scripts/seed-demo targets   # mock targets, push/OTLP loops, location workers
//	go run ./scripts/seed-demo teardown  # delete the tenant and its users
//
// `make seed-demo` runs the three steps the usual way (targets restarted in
// the background, then seed). Only the Northwind tenant (fixed ID below) and
// users with an @northwind.example address are ever deleted or written;
// every other tenant is left alone.
//
// seed creates structure through the API (so audit, secret encryption and the
// monitor_groups junction behave exactly as for a user) and writes history
// with direct SQL that respects the invariants the services rely on: gapless
// state intervals whose open row matches monitors.current_state, raw
// check_results only inside the recent window (never in the future), hourly
// rollups for every hour and daily rollups equal to their sum, and never a
// synthetic rollup hour that also holds raw rows. See README.md.
//
// targets is long-running and DB-driven: it reloads the tenant every 20s, so
// re-seeding under a running targets process picks up new push tokens, agent
// IDs and location credentials without a restart.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"
)

const (
	// TenantID is fixed so seed/teardown are idempotent and can never touch
	// another tenant.
	TenantID   = "00000000-0000-0000-0000-00000000d3e0"
	TenantName = "Northwind"
	// UserEmailDomain marks the seeded users (users are platform-scoped, so
	// they do not cascade with the tenant).
	UserEmailDomain = "northwind.example"
	AdminUserID     = "29b9d6ff-acd2-4500-a46a-75c83bd07998"
)

type options struct {
	dbURL       string
	apiURL      string
	user        string
	pass        string
	rawHours    int
	historyDays int
	mockPort    int
	workerBin   string
	natsURL     string
	keyFile     string
	pidFile     string
	docker      bool
}

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	cmd := os.Args[1]
	fs := flag.NewFlagSet(cmd, flag.ExitOnError)
	var o options
	fs.StringVar(&o.dbURL, "db", envOr("SEED_POSTGRES_URL", "postgres://probara:probara@localhost:5432/probara?sslmode=disable"), "Postgres URL")
	fs.StringVar(&o.apiURL, "api", envOr("API_URL", "http://localhost:8080"), "Probara API base URL")
	fs.StringVar(&o.user, "user", envOr("ADMIN_USER", "admin"), "superadmin username")
	fs.StringVar(&o.pass, "pass", envOr("ADMIN_PASS", "change-me"), "superadmin password")
	fs.IntVar(&o.rawHours, "raw-hours", 48, "hours of raw check_results to backfill (<= 29 days)")
	fs.IntVar(&o.historyDays, "history-days", 110, "days of state/rollup history (must reach back past the last closed quarter)")
	fs.IntVar(&o.mockPort, "mock-port", 19100, "base port of the mock targets (uses base..base+99)")
	fs.StringVar(&o.workerBin, "worker-bin", "/tmp/probara-bin/worker", "worker binary used for the private-location workers")
	fs.StringVar(&o.natsURL, "nats", envOr("NATS_URL", "nats://localhost:4222"), "NATS URL for the location workers")
	fs.StringVar(&o.keyFile, "key-file", "/tmp/probara-seed-demo.otlp-key", "where seed stores the OTLP API key targets uses")
	fs.StringVar(&o.pidFile, "pid-file", "/tmp/probara-seed-targets.pid", "pid file written by targets")
	fs.BoolVar(&o.docker, "docker", true, "targets: start redis/rabbitmq containers for the redis/rabbitmq monitors")
	_ = fs.Parse(os.Args[2:])

	if o.rawHours < 1 || o.rawHours > 29*24 {
		fatalf("-raw-hours must be 1..696")
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	var err error
	switch cmd {
	case "seed":
		err = runSeed(ctx, o)
	case "targets":
		err = runTargets(ctx, o)
	case "teardown":
		err = runTeardown(ctx, o)
	default:
		usage()
		os.Exit(2)
	}
	if err != nil {
		fatalf("%s: %v", cmd, err)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, "usage: seed-demo <seed|targets|teardown> [flags]")
}

func envOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func fatalf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "seed-demo: "+format+"\n", args...)
	os.Exit(1)
}

func logf(format string, args ...any) {
	fmt.Printf("%s  "+format+"\n", append([]any{time.Now().Format("15:04:05")}, args...)...)
}
