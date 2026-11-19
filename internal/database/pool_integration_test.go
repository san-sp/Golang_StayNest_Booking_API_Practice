package database

import (
	"context"
	"os"
	"testing"
	"time"
)

func TestNewPool_Integration(t *testing.T) {
	databaseURL := os.Getenv("STAYNEST_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("STAYNEST_TEST_DATABASE_URL is not set")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	pool, err := NewPool(ctx, databaseURL)
	if err != nil {
		t.Fatalf("NewPool() returned unexpected error: %v", err)
	}
	defer pool.Close()

	var result int

	err = pool.QueryRow(ctx, "SELECT 1").Scan(&result)
	if err != nil {
		t.Fatalf("failed to execute SELECT 1: %v", err)
	}

	if result != 1 {
		t.Errorf("expected query result 1, got %d", result)
	}
}
