// Package paths compares and normalises file system paths the way the
// platform does: case-insensitively on Windows, with symlinks resolved, so a
// file reached by two spellings is one document.
package paths

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

var foldCase = runtime.GOOS == "windows"

// Canonical makes p absolute and clean, resolves symlinks (for paths that
// exist) and, on Windows, restores each component's case as stored on disk.
// macOS keeps the spelling it is given: its paths come from the system in
// their stored case.
func Canonical(p string) string {
	if abs, err := filepath.Abs(p); err == nil {
		p = abs
	}
	if real, err := filepath.EvalSymlinks(p); err == nil {
		p = real
	}
	p = filepath.Clean(p)
	if foldCase {
		p = trueCase(p)
	}
	return p
}

// Equal reports whether a and b name the same path on this platform.
func Equal(a, b string) bool {
	return equal(filepath.Clean(a), filepath.Clean(b), foldCase)
}

// Within reports whether child is dir itself or lies below it.
func Within(child, dir string) bool {
	return within(filepath.Clean(child), filepath.Clean(dir), filepath.Separator, foldCase)
}

func equal(a, b string, fold bool) bool {
	if fold {
		return strings.EqualFold(a, b)
	}
	return a == b
}

func within(child, dir string, sep byte, fold bool) bool {
	if equal(child, dir, fold) {
		return true
	}
	prefix := dir
	if !strings.HasSuffix(prefix, string(sep)) {
		prefix += string(sep)
	}
	if len(child) < len(prefix) {
		return false
	}
	return equal(child[:len(prefix)], prefix, fold)
}

// trueCase rebuilds p from the directory entries on disk, component by
// component, so "c:\users\KAI\dokumente" becomes "C:\Users\kai\Dokumente".
// Components that cannot be read are kept as given.
func trueCase(p string) string {
	vol := filepath.VolumeName(p)
	rest := strings.TrimPrefix(p[len(vol):], string(filepath.Separator))
	cur := strings.ToUpper(vol) + string(filepath.Separator)
	if rest == "" {
		return cur
	}
	for _, part := range strings.Split(rest, string(filepath.Separator)) {
		name := part
		if entries, err := os.ReadDir(cur); err == nil {
			for _, e := range entries {
				if strings.EqualFold(e.Name(), part) {
					name = e.Name()
					break
				}
			}
		}
		cur = filepath.Join(cur, name)
	}
	return cur
}

// Display shortens p for people: the home directory becomes ~.
func Display(p string) string {
	if home, err := os.UserHomeDir(); err == nil && Within(p, home) {
		return "~" + p[len(filepath.Clean(home)):]
	}
	return p
}
