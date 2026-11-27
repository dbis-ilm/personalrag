package main

import (
	"os"
	"testing"

	"github.com/dbis-ilm/personalrag/internal/secrets"
)

// TestMain keeps every test away from the real system keychain.
func TestMain(m *testing.M) {
	secrets.UseMemoryStore()
	// User-facing texts in German, whatever the machine's language.
	os.Setenv("PERSONALRAG_LANG", "de")
	os.Exit(m.Run())
}
