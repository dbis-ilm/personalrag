// Package ocr reads scanned PDFs with the operating system's text
// recognition: Apple Vision on macOS. Other systems report ErrUnavailable
// for now, and such documents stay marked "needs OCR".
//
// Recognised text is cached per file content (SHA-256), so indexing, agents
// reading the document and re-indexing after a model change pay for the
// recognition once.
package ocr

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/dbis-ilm/askyourdocs"
)

// ErrUnavailable: no text recognition on this system.
var ErrUnavailable = errors.New("text recognition is not available on this system")

// MaxPages caps the pages recognised per document; a longer scan is read up
// to this page.
const MaxPages = 500

// Line is one recognised line on a page, in Vision's reading order. Y and H
// are the top edge and height as fractions of the page height, from the top.
type Line struct {
	Text string  `json:"t"`
	X    float64 `json:"x"`
	Y    float64 `json:"y"`
	H    float64 `json:"h"`
}

// cacheVersion changes when recognition or grouping changes, so old cache
// entries are ignored.
const cacheVersion = 1

var (
	mu       sync.Mutex
	cacheDir string
)

// SetCacheDir sets where recognised text is kept; "" disables the cache.
func SetCacheDir(dir string) {
	mu.Lock()
	cacheDir = dir
	mu.Unlock()
}

// PruneCache removes cache entries not used for maxAge (reads refresh an
// entry's time), so text of long-gone files does not pile up.
func PruneCache(maxAge time.Duration) (removed int) {
	mu.Lock()
	dir := cacheDir
	mu.Unlock()
	if dir == "" {
		return 0
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return 0
	}
	cutoff := time.Now().Add(-maxAge)
	for _, e := range entries {
		info, err := e.Info()
		if err != nil || info.IsDir() || info.ModTime().After(cutoff) {
			continue
		}
		if os.Remove(filepath.Join(dir, e.Name())) == nil {
			removed++
		}
	}
	return removed
}

// Available reports whether this system can recognise text.
func Available() bool { return available() }

// PDF recognises the text of a scanned PDF and returns it as paragraphs
// with page numbers, from the cache when the file is unchanged.
func PDF(ctx context.Context, path string) ([]askyourdocs.Paragraph, error) {
	if !available() {
		return nil, ErrUnavailable
	}
	key, err := fileKey(path)
	if err != nil {
		return nil, err
	}
	if paras, ok := cached(key); ok {
		return paras, nil
	}
	pages, err := pageCount(path)
	if err != nil {
		return nil, err
	}
	var paras []askyourdocs.Paragraph
	for p := 1; p <= min(pages, MaxPages); p++ {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		lines, err := recognizePage(path, p)
		if err != nil {
			return nil, fmt.Errorf("page %d: %w", p, err)
		}
		for _, text := range Group(lines) {
			paras = append(paras, askyourdocs.Paragraph{Text: text, PageNum: p})
		}
	}
	store(key, paras)
	return paras, nil
}

// Group joins lines into paragraphs: a new paragraph starts where the gap
// to the previous line is larger than about one line height, or where the
// text goes back up the page (next column). Hyphenated line ends are joined.
func Group(lines []Line) []string {
	var (
		out  []string
		cur  strings.Builder
		prev *Line
	)
	flush := func() {
		if s := strings.TrimSpace(cur.String()); s != "" {
			out = append(out, s)
		}
		cur.Reset()
	}
	for i := range lines {
		l := &lines[i]
		text := strings.TrimSpace(l.Text)
		if text == "" {
			continue
		}
		if prev != nil {
			gap := l.Y - (prev.Y + prev.H)
			h := max(prev.H, l.H)
			if gap > 0.9*h || l.Y < prev.Y-0.5*h {
				flush()
			}
		}
		if cur.Len() > 0 {
			s := cur.String()
			switch {
			case strings.HasSuffix(s, "-") && isLower(text):
				// "Kündi-" + "gung" → "Kündigung"
				cur.Reset()
				cur.WriteString(strings.TrimSuffix(s, "-"))
			case strings.HasSuffix(s, "-"):
				// "Bindestrich-" + "Wort" → "Bindestrich-Wort"
			default:
				cur.WriteByte(' ')
			}
		}
		cur.WriteString(text)
		prev = l
	}
	flush()
	return out
}

func isLower(s string) bool {
	for _, r := range s {
		return r >= 'a' && r <= 'z' || r == 'ä' || r == 'ö' || r == 'ü' || r == 'ß'
	}
	return false
}

func fileKey(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return fmt.Sprintf("v%d-%s", cacheVersion, hex.EncodeToString(h.Sum(nil))), nil
}

func cachePath(key string) string {
	mu.Lock()
	dir := cacheDir
	mu.Unlock()
	if dir == "" {
		return ""
	}
	return filepath.Join(dir, key+".json")
}

func cached(key string) ([]askyourdocs.Paragraph, bool) {
	p := cachePath(key)
	if p == "" {
		return nil, false
	}
	b, err := os.ReadFile(p)
	if err != nil {
		return nil, false
	}
	var paras []askyourdocs.Paragraph
	if json.Unmarshal(b, &paras) != nil {
		return nil, false
	}
	now := time.Now()
	_ = os.Chtimes(p, now, now) // still in use; keeps PruneCache away
	return paras, true
}

func store(key string, paras []askyourdocs.Paragraph) {
	p := cachePath(key)
	if p == "" {
		return
	}
	b, err := json.Marshal(paras)
	if err != nil {
		return
	}
	_ = os.MkdirAll(filepath.Dir(p), 0o700)
	tmp := p + ".tmp"
	if os.WriteFile(tmp, b, 0o600) == nil {
		_ = os.Rename(tmp, p)
	}
}
