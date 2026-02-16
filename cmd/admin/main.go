package main

import (
	"context"
	"fmt"
	"os"
	"strconv"

	"github.com/yassinebenameur/probara/shared/auth"
	"github.com/yassinebenameur/probara/shared/db"
)

func main() {
	postgresURL := os.Getenv("POSTGRES_URL")
	if postgresURL == "" {
		fmt.Fprintln(os.Stderr, "POSTGRES_URL is required")
		os.Exit(1)
	}

	username := os.Getenv("ADMIN_USERNAME")
	password := os.Getenv("ADMIN_PASSWORD")
	if username == "" || password == "" {
		fmt.Fprintln(os.Stderr, "ADMIN_USERNAME and ADMIN_PASSWORD are required")
		os.Exit(1)
	}

	bcryptCost := 12
	if costStr := os.Getenv("ADMIN_BCRYPT_COST"); costStr != "" {
		cost, err := strconv.Atoi(costStr)
		if err != nil {
			fmt.Fprintln(os.Stderr, "Invalid ADMIN_BCRYPT_COST")
			os.Exit(1)
		}
		bcryptCost = cost
	}

	client, err := db.NewClient(postgresURL)
	if err != nil {
		fmt.Fprintln(os.Stderr, "Failed to connect to database:", err)
		os.Exit(1)
	}
	defer client.Close()

	passwordHash, err := auth.HashPassword(password, bcryptCost)
	if err != nil {
		fmt.Fprintln(os.Stderr, "Failed to hash password:", err)
		os.Exit(1)
	}

	query := `
		INSERT INTO admin_users (username, password_hash)
		VALUES ($1, $2)
		ON CONFLICT (username)
		DO UPDATE SET password_hash = EXCLUDED.password_hash, updated_at = NOW(), disabled_at = NULL
	`

	if _, err := client.ExecContext(context.Background(), query, username, passwordHash); err != nil {
		fmt.Fprintln(os.Stderr, "Failed to create admin user:", err)
		os.Exit(1)
	}

	fmt.Println("Admin user created/updated:", username)
}
