package main

import (
	"os"
	"testing"

	"github.com/dbis-ilm/personalrag/internal/secrets"
)

// TestMain keeps every test away from the real system keychain.
func TestMain(m *testing.M) {
	secrets.UseMemoryStore()
	os.Exit(m.Run())
}
