package config

import (
	"os"
	"path/filepath"
	"regexp"
	"testing"
)

func TestInstanceID(t *testing.T) {
	base := t.TempDir()
	a := filepath.Join(base, "a")
	b := filepath.Join(base, "b")
	os.MkdirAll(a, 0o755)
	os.MkdirAll(b, 0o755)
	link := filepath.Join(base, "link-to-a")
	if err := os.Symlink(a, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	idA := InstanceID(a)
	if !regexp.MustCompile(`^app\.personalrag\.[0-9a-f]{12}$`).MatchString(idA) {
		t.Fatalf("unexpected format %q", idA)
	}
	if InstanceID(a) != idA || InstanceID(a+string(filepath.Separator)) != idA {
		t.Error("same directory must give the same ID")
	}
	if InstanceID(link) != idA {
		t.Error("symlink to the same directory must share the lock")
	}
	if InstanceID(b) == idA {
		t.Error("different data directories must not share a lock")
	}
}
