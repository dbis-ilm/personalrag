package config

import (
	"crypto/sha256"
	"encoding/hex"
	"path/filepath"
	"runtime"
	"strings"
)

// InstanceID returns the single-instance lock ID for a data directory. One
// running app per data directory: a second launch on the same index hands
// over to the first, while a test run with its own PERSONALRAG_DATA does not
// collide with the user's app. Symlinks and, on Windows, letter case are
// normalised so two spellings of one directory share a lock.
func InstanceID(dataDir string) string {
	p := dataDir
	if abs, err := filepath.Abs(p); err == nil {
		p = abs
	}
	if real, err := filepath.EvalSymlinks(p); err == nil {
		p = real
	}
	p = filepath.Clean(p)
	if runtime.GOOS == "windows" {
		p = strings.ToLower(p)
	}
	sum := sha256.Sum256([]byte(p))
	return "app.personalrag." + hex.EncodeToString(sum[:6])
}
