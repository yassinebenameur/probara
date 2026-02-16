package main

import (
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"log"
	"os"

	_ "github.com/lib/pq"
	"golang.org/x/crypto/bcrypt"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintf(os.Stderr, "Usage: %s <tenant-id>\n", os.Args[0])
		os.Exit(1)
	}

	tenantID := os.Args[1]
	postgresURL := os.Getenv("POSTGRES_URL")
	if postgresURL == "" {
		postgresURL = "postgres://probara:probara@localhost:5432/probara?sslmode=disable"
	}

	// Generate a secure random API key
	apiKey := generateAPIKey()
	fmt.Printf("Generated API Key: %s\n", apiKey)

	// Compute SHA256 prefix for fast lookup
	sha256Hash := sha256.Sum256([]byte(apiKey))
	keyPrefix := hex.EncodeToString(sha256Hash[:])

	// Generate bcrypt hash
	bcryptHash, err := bcrypt.GenerateFromPassword([]byte(apiKey), 12)
	if err != nil {
		log.Fatalf("Failed to hash API key: %v", err)
	}

	// Connect to database
	db, err := sql.Open("postgres", postgresURL)
	if err != nil {
		log.Fatalf("Failed to connect to database: %v", err)
	}
	defer db.Close()

	// Insert the API key
	_, err = db.Exec(
		"INSERT INTO api_keys (tenant_id, name, key_prefix, key_hash) VALUES ($1, $2, $3, $4)",
		tenantID, "agent-key", keyPrefix, string(bcryptHash),
	)
	if err != nil {
		log.Fatalf("Failed to insert API key: %v", err)
	}

	fmt.Println("\n=========================================")
	fmt.Println("API Key created successfully!")
	fmt.Println("=========================================")
	fmt.Printf("Tenant ID: %s\n", tenantID)
	fmt.Printf("API Key: %s\n", apiKey)
	fmt.Println("\nUse this in your curl commands:")
	fmt.Printf("  -H \"Authorization: Bearer %s\"\n", apiKey)
	fmt.Println("=========================================")
}

func generateAPIKey() string {
	b := make([]byte, 32)
	_, err := rand.Read(b)
	if err != nil {
		log.Fatalf("Failed to generate random key: %v", err)
	}
	return hex.EncodeToString(b)
}
