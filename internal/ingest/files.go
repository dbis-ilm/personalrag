// Package ingest turns local files into askyourdocs paragraphs and indexable
// chunk documents.
package ingest

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/firebase/genkit/go/ai"

	"github.com/dbis-ilm/askyourdocs"

	"github.com/dbis-ilm/personalrag/internal/i18n"
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
		return i18n.Errorf("exclusions.notName", p)
	}
	if _, err := filepath.Match(p, ""); err != nil {
		return i18n.Errorf("exclusions.invalid", p)
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
var ErrNeedsOCR = errors.New("no text layer (scanned document)")

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
		return extractTextFile(path, true)
	case ".txt":
		return extractTextFile(path, false)
	}
	return nil, fmt.Errorf("unsupported format: %s", filepath.Ext(path))
}

// extractTextFile reads a Markdown or plain-text file into paragraphs.
func extractTextFile(path string, markdown bool) ([]askyourdocs.Paragraph, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return askyourdocs.ExtractTextParagraphs(f, markdown)
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
