package main

import (
	"database/sql"
	"fmt"
	"os"

	_ "github.com/lib/pq"

	"github.com/google/uuid"
	"github.com/yassinebenameur/probara/shared/auth"
)

func main() {
	postgresURL := os.Getenv("POSTGRES_URL")
	if postgresURL == "" {
		postgresURL = "postgres://probara:probara@localhost:5432/probara?sslmode=disable"
	}

	db, err := sql.Open("postgres", postgresURL)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to connect to database: %v\n", err)
		os.Exit(1)
	}
	defer db.Close()

	// Test connection
	if err := db.Ping(); err != nil {
		fmt.Fprintf(os.Stderr, "Failed to ping database: %v\n", err)
		fmt.Fprintf(os.Stderr, "Make sure PostgreSQL is running and accessible at: %s\n", postgresURL)
		os.Exit(1)
	}

	// Create test tenant
	tenantID := uuid.New()
	_, err = db.Exec("INSERT INTO tenants (id, name) VALUES ($1, $2) ON CONFLICT (name) DO NOTHING", tenantID, "test-tenant")
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to create tenant: %v\n", err)
		os.Exit(1)
	}

	// Get tenant ID (in case it already existed)
	var existingTenantID string
	err = db.QueryRow("SELECT id FROM tenants WHERE name = $1", "test-tenant").Scan(&existingTenantID)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to get tenant ID: %v\n", err)
		os.Exit(1)
	}

	// Generate test API key
	testAPIKey := fmt.Sprintf("test-api-key-%d", os.Getpid())
	hashResult, err := auth.HashAPIKey(testAPIKey)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to hash API key: %v\n", err)
		os.Exit(1)
	}

	// Create API key with both bcrypt hash and key_prefix
	apiKeyID := uuid.New()
	_, err = db.Exec("INSERT INTO api_keys (id, tenant_id, name, key_hash, key_prefix) VALUES ($1, $2, $3, $4, $5)", apiKeyID, existingTenantID, "test-key", hashResult.BcryptHash, hashResult.KeyPrefix)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to create API key: %v\n", err)
		os.Exit(1)
	}

	fmt.Println("=========================================")
	fmt.Println("Test data created successfully!")
	fmt.Println("=========================================")
	fmt.Printf("Tenant ID: %s\n", existingTenantID)
	fmt.Printf("API Key: %s\n", testAPIKey)
	fmt.Println("")
	fmt.Println("Use this header in your API requests:")
	fmt.Printf("Authorization: Bearer %s\n", testAPIKey)
	fmt.Println("=========================================")
}
