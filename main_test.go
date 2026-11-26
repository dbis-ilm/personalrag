package main

import (
	"os"
	"testing"

	"personalrag/internal/secrets"
)

// TestMain keeps every test away from the real system keychain.
func TestMain(m *testing.M) {
	secrets.UseMemoryStore()
	os.Exit(m.Run())
}
