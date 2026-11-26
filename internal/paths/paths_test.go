package paths

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// The platform rules are tested on every OS through the internal helpers,
// so Windows behaviour is checked on the macOS runner too.
func TestWithinRules(t *testing.T) {
	cases := []struct {
		child, dir string
		sep        byte
		fold, want bool
	}{
		{`/Users/kai/Docs/a.pdf`, `/Users/kai/Docs`, '/', false, true},
		{`/Users/kai/Docs`, `/Users/kai/Docs`, '/', false, true},
		{`/Users/kai/Docs2/a.pdf`, `/Users/kai/Docs`, '/', false, false}, // sibling with common prefix
		{`/Users/kai/docs/a.pdf`, `/Users/kai/Docs`, '/', false, false},  // case matters on macOS
		{`/a`, `/`, '/', false, true},
		{`C:\Users\kai\Dokumente\a.pdf`, `C:\Users\kai\Dokumente`, '\\', true, true},
		{`c:\users\KAI\dokumente\a.pdf`, `C:\Users\kai\Dokumente`, '\\', true, true}, // case-insensitive on Windows
		{`C:\Users\kai\Dokumente2\a.pdf`, `C:\Users\kai\Dokumente`, '\\', true, false},
		{`D:\a.pdf`, `D:\`, '\\', true, true},
	}
	for _, c := range cases {
		if got := within(c.child, c.dir, c.sep, c.fold); got != c.want {
			t.Errorf("within(%q, %q, fold=%v) = %v, want %v", c.child, c.dir, c.fold, got, c.want)
		}
	}
}

func TestCanonicalResolvesSymlinks(t *testing.T) {
	base := t.TempDir()
	real := filepath.Join(base, "Verträge")
	os.MkdirAll(real, 0o755)
	link := filepath.Join(base, "link")
	if err := os.Symlink(real, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if Canonical(link) != Canonical(real) {
		t.Fatalf("symlink not resolved: %q vs %q", Canonical(link), Canonical(real))
	}
	// Relative and unclean input gives the same result.
	wd, _ := os.Getwd()
	defer os.Chdir(wd)
	os.Chdir(base)
	if Canonical("./Verträge/../Verträge/") != Canonical(real) {
		t.Fatalf("relative path not canonical: %q", Canonical("./Verträge/../Verträge/"))
	}
}

func TestCanonicalTrueCaseOnWindows(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("case folding only applies on Windows")
	}
	base := t.TempDir()
	dir := filepath.Join(base, "Dokumente", "Verträge")
	os.MkdirAll(dir, 0o755)
	f := filepath.Join(dir, "Mietvertrag.pdf")
	os.WriteFile(f, []byte("x"), 0o644)
	odd := filepath.Join(base, "DOKUMENTE", "verträge", "MIETVERTRAG.PDF")
	if got := Canonical(odd); got != Canonical(f) {
		t.Fatalf("Canonical(%q) = %q, want %q", odd, got, Canonical(f))
	}
}
