package main

import (
	"database/sql"
	"fmt"
	"os"
	"time"

	_ "github.com/lib/pq"
	"github.com/yassinebenameur/probara/shared/db"
)

func main() {
	postgresURL := os.Getenv("POSTGRES_URL")
	if postgresURL == "" {
		fmt.Fprintf(os.Stderr, "POSTGRES_URL environment variable is required\n")
		os.Exit(1)
	}

	migrationsPath := "./migrations"
	if path := os.Getenv("MIGRATIONS_PATH"); path != "" {
		migrationsPath = path
	}

	fmt.Printf("Running database migrations from %s...\n", migrationsPath)
	fmt.Printf("Connecting to database...\n")

	// Retry connection for up to 30 seconds (in case DB is starting up)
	var dbConn *sql.DB
	var err error
	maxRetries := 30
	for i := 0; i < maxRetries; i++ {
		dbConn, err = sql.Open("postgres", postgresURL)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Failed to open database connection: %v\n", err)
			os.Exit(1)
		}

		err = dbConn.Ping()
		if err == nil {
			break
		}

		if i < maxRetries-1 {
			fmt.Printf("Database not ready, retrying in 1 second... (%d/%d)\n", i+1, maxRetries)
			time.Sleep(1 * time.Second)
		}
	}

	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to connect to database after %d attempts: %v\n", maxRetries, err)
		os.Exit(1)
	}

	defer dbConn.Close()

	fmt.Println("Database connected, running migrations...")

	// Run migrations
	if err := db.Migrate(dbConn, migrationsPath); err != nil {
		fmt.Fprintf(os.Stderr, "Migration failed: %v\n", err)
		os.Exit(1)
	}

	fmt.Println("✅ Migrations completed successfully!")
}
