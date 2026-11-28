package main

import (
	"bytes"
	"fmt"
	"image"
	"image/draw"
	"image/png"
	"strings"
	"time"

	"github.com/go-pdf/fpdf"
	"golang.org/x/image/font"
	"golang.org/x/image/font/gofont/gobold"
	"golang.org/x/image/font/gofont/goregular"
	"golang.org/x/image/font/opentype"
	"golang.org/x/image/math/fixed"
)

// A document is laid out once as positioned text runs and then rendered
// either as a PDF with a text layer or as a scan (one image per page, no
// text layer). Each run becomes its own text object, so columns drawn side
// by side are separate objects as in real office PDFs.

const (
	pageW, pageH = 210.0, 297.0 // A4, mm
	marginL      = 20.0
	marginR      = 190.0
	marginTop    = 22.0
	marginBottom = 270.0
	scanDPI      = 200
)

// fixedDate keeps the generated PDFs byte-identical between runs.
var fixedDate = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

type run struct {
	page int
	x, y float64 // mm; y is the baseline
	size float64 // pt
	bold bool
	text string
}

type layout struct {
	runs    []run
	pages   int
	footer  string // printed on every page with "Seite i von n"
	measure *fpdf.Fpdf
	tr      func(string) string
	y       float64
}

func newLayout(footer string) *layout {
	m := fpdf.New("P", "mm", "A4", "")
	return &layout{pages: 1, footer: footer, measure: m, tr: m.UnicodeTranslatorFromDescriptor(""), y: marginTop}
}

// width is the width of s in mm in Helvetica at size pt.
func (l *layout) width(s string, size float64, bold bool) float64 {
	l.measure.SetFont("Helvetica", style(bold), size)
	return l.measure.GetStringWidth(l.tr(s))
}

func style(bold bool) string {
	if bold {
		return "B"
	}
	return ""
}

// at places s at x on the current line without advancing.
func (l *layout) at(x, size float64, bold bool, s string) {
	l.runs = append(l.runs, run{page: l.pages, x: x, y: l.y, size: size, bold: bold, text: s})
}

// need starts a new page if h mm do not fit any more.
func (l *layout) need(h float64) {
	if l.y+h > marginBottom {
		l.pages++
		l.y = marginTop
	}
}

// line writes one line at x and advances by lh mm.
func (l *layout) line(x, size float64, bold bool, s string, lh float64) {
	l.need(lh)
	l.at(x, size, bold, s)
	l.y += lh
}

// gap advances by h mm.
func (l *layout) gap(h float64) { l.y += h }

// para writes s wrapped to the text width, followed by a small gap.
func (l *layout) para(size float64, bold bool, s string) {
	lh := size * 0.48
	for _, ln := range l.wrap(s, marginR-marginL, size, bold) {
		l.line(marginL, size, bold, ln, lh)
	}
	l.gap(lh * 0.6)
}

func (l *layout) wrap(s string, width, size float64, bold bool) []string {
	var lines []string
	cur := ""
	for _, w := range strings.Fields(s) {
		next := w
		if cur != "" {
			next = cur + " " + w
		}
		if cur != "" && l.width(next, size, bold) > width {
			lines = append(lines, cur)
			next = w
		}
		cur = next
	}
	if cur != "" {
		lines = append(lines, cur)
	}
	return lines
}

func newPDF(title string) *fpdf.Fpdf {
	pdf := fpdf.New("P", "mm", "A4", "")
	pdf.SetCreationDate(fixedDate)
	pdf.SetModificationDate(fixedDate)
	pdf.SetCatalogSort(true)
	pdf.SetCompression(true)
	pdf.SetAutoPageBreak(false, 0)
	pdf.SetTitle(title, true)
	pdf.SetCreator("PersonalRAG demogen", true)
	return pdf
}

func (l *layout) footerText(page int) string {
	return fmt.Sprintf("%s · Seite %d von %d", l.footer, page, l.pages)
}

// writePDF renders the layout as a PDF with a text layer.
func (l *layout) writePDF(path, title string) error {
	pdf := newPDF(title)
	tr := pdf.UnicodeTranslatorFromDescriptor("")
	for p := 1; p <= l.pages; p++ {
		pdf.AddPage()
		for _, r := range l.runs {
			if r.page == p {
				pdf.SetFont("Helvetica", style(r.bold), r.size)
				pdf.Text(r.x, r.y, tr(r.text))
			}
		}
		if l.footer != "" {
			pdf.SetFont("Helvetica", "", 8)
			pdf.Text(marginL, 285, tr(l.footerText(p)))
		}
	}
	return pdf.OutputFileAndClose(path)
}

// writeScan renders the layout as a scanned document: each page an image
// at scanDPI, no text layer, so only OCR can read it.
func (l *layout) writeScan(path, title string) error {
	faces := map[string]font.Face{}
	face := func(size float64, bold bool) (font.Face, error) {
		key := fmt.Sprintf("%v-%v", size, bold)
		if f, ok := faces[key]; ok {
			return f, nil
		}
		data := goregular.TTF
		if bold {
			data = gobold.TTF
		}
		ft, err := opentype.Parse(data)
		if err != nil {
			return nil, err
		}
		f, err := opentype.NewFace(ft, &opentype.FaceOptions{Size: size, DPI: scanDPI, Hinting: font.HintingFull})
		if err != nil {
			return nil, err
		}
		faces[key] = f
		return f, nil
	}
	px := func(mm float64) int { return int(mm / 25.4 * scanDPI) }

	pdf := newPDF(title)
	for p := 1; p <= l.pages; p++ {
		// fpdf orders images by width only, ties in map order; one more
		// pixel column per page keeps the output byte-identical.
		img := image.NewGray(image.Rect(0, 0, px(pageW)+p, px(pageH)))
		draw.Draw(img, img.Bounds(), image.White, image.Point{}, draw.Src)
		runs := l.runs
		if l.footer != "" {
			runs = append(runs[:len(runs):len(runs)], run{page: p, x: marginL, y: 285, size: 8, text: l.footerText(p)})
		}
		for _, r := range runs {
			if r.page != p {
				continue
			}
			f, err := face(r.size, r.bold)
			if err != nil {
				return err
			}
			d := font.Drawer{Dst: img, Src: image.Black, Face: f, Dot: fixed.P(px(r.x), px(r.y))}
			d.DrawString(r.text)
		}
		var buf bytes.Buffer
		if err := png.Encode(&buf, img); err != nil {
			return err
		}
		name := fmt.Sprintf("page%d", p)
		opts := fpdf.ImageOptions{ImageType: "PNG"}
		pdf.AddPage()
		pdf.RegisterImageOptionsReader(name, opts, &buf)
		pdf.ImageOptions(name, 0, 0, pageW, pageH, false, opts, 0, "")
	}
	return pdf.OutputFileAndClose(path)
}
