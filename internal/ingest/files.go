// Package ingest turns local files into askyourdocs paragraphs and indexable
// chunk documents.
package ingest

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/firebase/genkit/go/ai"

	"github.com/dbis-ilm/askyourdocs"

	"github.com/dbis-ilm/personalrag/internal/store"
)

// MaxFileSize skips files above this size.
const MaxFileSize = 200 << 20

var formats = map[string]string{
	".pdf": "PDF", ".docx": "Word",
	".md": "Markdown", ".markdown": "Markdown", ".txt": "Text",
	".html": "HTML", ".htm": "HTML",
}

// Supported reports whether path has an extension we can extract.
func Supported(path string) bool {
	_, ok := formats[strings.ToLower(filepath.Ext(path))]
	return ok
}

// Format names the file type for display.
func Format(path string) string { return formats[strings.ToLower(filepath.Ext(path))] }

// SkipDir reports whether a directory is excluded by default.
func SkipDir(name string) bool {
	return strings.HasPrefix(name, ".") || name == "node_modules" || name == "__pycache__"
}

// SkipFile reports whether a file is excluded by default: hidden files
// (including iCloud ".icloud" placeholders) and Office lock/temp files.
func SkipFile(name string) bool {
	return strings.HasPrefix(name, ".") || strings.HasPrefix(name, "~$") || strings.HasSuffix(name, ".tmp")
}

// ValidatePattern checks an exclusion pattern: a file or folder name, with
// the wildcards of filepath.Match (* ? [..]); no path separators.
func ValidatePattern(p string) error {
	if strings.ContainsAny(p, `/\`) {
		return fmt.Errorf("%q: nur Datei- oder Ordnernamen, keine Pfade", p)
	}
	if _, err := filepath.Match(p, ""); err != nil {
		return fmt.Errorf("%q ist kein gültiges Muster", p)
	}
	return nil
}

// MatchesAny reports whether name matches one of the patterns. Matching
// ignores letter case, as users expect from file names on macOS and Windows.
func MatchesAny(name string, patterns []string) bool {
	n := strings.ToLower(name)
	for _, p := range patterns {
		if ok, _ := filepath.Match(strings.ToLower(p), n); ok {
			return true
		}
	}
	return false
}

// HashFile returns the SHA-256 of a file's content.
func HashFile(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// ErrNeedsOCR marks a PDF without a text layer.
var ErrNeedsOCR = fmt.Errorf("keine Textebene erkannt")

// Extract returns a file's paragraphs. Scanned PDFs return ErrNeedsOCR.
func Extract(path string) ([]askyourdocs.Paragraph, error) {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".pdf", ".docx":
		paras, err := askyourdocs.ExtractParagraphs(path)
		if err != nil {
			return nil, err
		}
		if strings.EqualFold(filepath.Ext(path), ".pdf") && !askyourdocs.HasTextLayer(paras) {
			return nil, ErrNeedsOCR
		}
		return paras, nil
	case ".html", ".htm":
		f, err := os.Open(path)
		if err != nil {
			return nil, err
		}
		defer f.Close()
		return askyourdocs.HTMLToStructuredText(f)
	case ".md", ".markdown":
		return extractText(path, true)
	case ".txt":
		return extractText(path, false)
	}
	return nil, fmt.Errorf("nicht unterstütztes Format: %s", filepath.Ext(path))
}

// extractText splits plain text or Markdown into paragraphs at blank lines.
// Markdown ATX headings become heading paragraphs and a breadcrumb.
func extractText(path string, markdown bool) ([]askyourdocs.Paragraph, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var (
		paras    []askyourdocs.Paragraph
		buf      []string
		headings []string // index = level-1
		inFence  bool
	)
	crumb := func() string {
		var parts []string
		for _, h := range headings {
			if h != "" {
				parts = append(parts, h)
			}
		}
		return strings.Join(parts, " › ")
	}
	flush := func() {
		text := strings.TrimSpace(strings.Join(buf, "\n"))
		buf = buf[:0]
		if text != "" {
			paras = append(paras, askyourdocs.Paragraph{Text: text, PageNum: 0, Breadcrumb: crumb()})
		}
	}

	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for sc.Scan() {
		line := strings.TrimRight(sc.Text(), " \t\r")
		trimmed := strings.TrimSpace(line)
		if markdown && strings.HasPrefix(trimmed, "```") {
			inFence = !inFence
			buf = append(buf, line)
			continue
		}
		if markdown && !inFence {
			if level, title, ok := atxHeading(trimmed); ok {
				flush()
				for len(headings) < level {
					headings = append(headings, "")
				}
				headings = append(headings[:level-1], title)
				paras = append(paras, askyourdocs.Paragraph{Text: title, PageNum: 0, Heading: true, Breadcrumb: crumb()})
				continue
			}
		}
		if trimmed == "" && !inFence {
			flush()
			continue
		}
		buf = append(buf, line)
	}
	flush()
	return paras, sc.Err()
}

func atxHeading(line string) (int, string, bool) {
	level := 0
	for level < len(line) && line[level] == '#' {
		level++
	}
	if level == 0 || level > 6 || level >= len(line) || line[level] != ' ' {
		return 0, "", false
	}
	return level, strings.TrimSpace(strings.TrimRight(line[level:], "# ")), true
}

// FileSource is the askyourdocs.IngestSource for one local file whose
// paragraphs were already extracted (so the caller could check for OCR
// first).
type FileSource struct {
	Path         string
	DocumentID   int64
	CollectionID string
	Paragraphs   []askyourdocs.Paragraph
}

func (s *FileSource) Name() string { return s.Path }

func (s *FileSource) Fetch(context.Context) ([]askyourdocs.Paragraph, error) {
	return s.Paragraphs, nil
}

func (s *FileSource) Documents(chunks []askyourdocs.Chunk) []*ai.Document {
	docs := make([]*ai.Document, len(chunks))
	for i, c := range chunks {
		section := c.Section
		if section == "" {
			section = c.Top
		}
		docs[i] = ai.DocumentFromText(c.Text, map[string]any{
			store.MetaPath:         s.Path,
			store.MetaDocumentID:   s.DocumentID,
			store.MetaCollectionID: s.CollectionID,
			store.MetaParentText:   c.ParentText,
			store.MetaPages:        c.Pages,
			store.MetaSection:      section,
			store.MetaBreadcrumb:   c.Breadcrumb,
		})
	}
	return docs
}

func (s *FileSource) DeleteStale(ctx context.Context, vs askyourdocs.VectorStore) (int, error) {
	return vs.DeleteByURL(ctx, s.Path)
}

// PlainText renders paragraphs as readable text for an agent: paragraphs
// separated by blank lines, headings as Markdown headings, and a "[Seite n]"
// marker wherever the page changes (paged formats only).
func PlainText(paras []askyourdocs.Paragraph) string {
	var b strings.Builder
	page := 0
	for _, p := range paras {
		text := strings.TrimSpace(p.Text)
		if text == "" {
			continue
		}
		if p.PageNum > 0 && p.PageNum != page {
			page = p.PageNum
			if b.Len() > 0 {
				b.WriteString("\n\n")
			}
			fmt.Fprintf(&b, "[Seite %d]", page)
		}
		if b.Len() > 0 {
			b.WriteString("\n\n")
		}
		if p.Heading {
			b.WriteString("## ")
		}
		b.WriteString(text)
	}
	return b.String()
}

// SplitText cuts text into parts of at most max runes, at paragraph
// boundaries where possible, so each part can go to a model on its own.
func SplitText(text string, max int) []string {
	var parts []string
	var cur strings.Builder
	curLen := 0
	flush := func() {
		if s := strings.TrimSpace(cur.String()); s != "" {
			parts = append(parts, s)
		}
		cur.Reset()
		curLen = 0
	}
	for _, para := range strings.Split(text, "\n\n") {
		n := len([]rune(para)) + 2
		if curLen > 0 && curLen+n > max {
			flush()
		}
		// A single paragraph longer than max is cut hard.
		for r := []rune(para); len(r) > max; r = []rune(para) {
			parts = append(parts, string(r[:max]))
			para = string(r[max:])
		}
		cur.WriteString(para)
		cur.WriteString("\n\n")
		curLen += len([]rune(para)) + 2
	}
	flush()
	return parts
}
