// Package secrets keeps credentials in the operating system's store
// (macOS Keychain, Windows Credential Manager) instead of settings.json.
package secrets

import (
	"errors"

	"github.com/zalando/go-keyring"
)

const service = "PersonalRAG"

// GPUStackKey is the account name of the GPUStack API key.
const GPUStackKey = "gpustack-api-key"

// Get returns a stored secret; "" without error when none is stored.
func Get(name string) (string, error) {
	v, err := keyring.Get(service, name)
	if errors.Is(err, keyring.ErrNotFound) {
		return "", nil
	}
	return v, err
}

// Set stores a secret, replacing an existing one.
func Set(name, value string) error {
	return keyring.Set(service, name, value)
}

// Delete removes a secret; removing a missing one is not an error.
func Delete(name string) error {
	err := keyring.Delete(service, name)
	if errors.Is(err, keyring.ErrNotFound) {
		return nil
	}
	return err
}

// UseMemoryStore switches to an in-memory store, for tests.
func UseMemoryStore() { keyring.MockInit() }
