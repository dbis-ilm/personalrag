package main

import (
	"archive/zip"
	"bytes"
	"encoding/xml"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Use case 3: check a document against rules from other documents. A
// bachelor's thesis (Word) is checked against examination regulations and
// a writing guide (PDF). Three rules are broken (length, missing
// declaration of authorship, too few sources); the others are met.

const (
	regulationsFile = "pruefungsordnung-bachelor-informatik.pdf"
	guideFile       = "leitfaden-abschlussarbeiten.pdf"
	thesisFile      = "bachelorarbeit-jana-meier.docx"
	thesisBodyPages = 85 // without appendix; the regulations allow at most 60
	thesisSources   = 12 // the guide asks for at least 20
)

type rulesDoc struct {
	title string
	paras []string // "§ …" or "N. …" lines become headings
}

var regulations = rulesDoc{
	title: "Prüfungsordnung für den Bachelorstudiengang Informatik (Auszug)",
	paras: []string{
		"Hochschule Musterstadt, Fakultät für Informatik. Fassung vom 1. Oktober 2024.",
		"§ 11 Zulassung zur Bachelorarbeit",
		"(1) Zur Bachelorarbeit wird zugelassen, wer mindestens 150 Leistungspunkte erworben hat. (2) Der Antrag auf Zulassung ist beim Prüfungsamt zu stellen; das Thema wird von der Betreuerin oder dem Betreuer ausgegeben.",
		"§ 12 Bachelorarbeit",
		"(1) Die Bachelorarbeit soll zeigen, dass die Kandidatin oder der Kandidat in der Lage ist, ein Problem der Informatik innerhalb einer vorgegebenen Frist selbstständig nach wissenschaftlichen Methoden zu bearbeiten.",
		"(2) Die Bearbeitungszeit beträgt zwölf Wochen. Auf begründeten Antrag kann sie einmalig um bis zu vier Wochen verlängert werden.",
		"(3) Der Umfang der Bachelorarbeit beträgt 40 bis 60 Seiten ohne Anhang und Verzeichnisse. Überschreitungen sind vorab mit der Betreuerin oder dem Betreuer abzustimmen und schriftlich zu begründen.",
		"(4) Die Arbeit kann in deutscher oder englischer Sprache verfasst werden.",
		"(5) Der Arbeit ist eine unterschriebene Erklärung beizufügen, dass sie selbstständig verfasst und keine anderen als die angegebenen Quellen und Hilfsmittel benutzt wurden (Eigenständigkeitserklärung). Werden Werkzeuge der generativen künstlichen Intelligenz genutzt, ist dies in der Erklärung anzugeben.",
		"(6) Die Arbeit ist fristgerecht in elektronischer Form beim Prüfungsamt einzureichen.",
		"§ 13 Bewertung der Bachelorarbeit",
		"(1) Die Bachelorarbeit wird von zwei Prüfenden bewertet. (2) Eine Arbeit ohne Eigenständigkeitserklärung wird nicht zur Bewertung angenommen.",
	},
}

var guide = rulesDoc{
	title: "Leitfaden für Abschlussarbeiten am Institut für Datenbanken",
	paras: []string{
		"Dieser Leitfaden ergänzt die Prüfungsordnung. Er beschreibt, was wir von einer Abschlussarbeit erwarten.",
		"1. Gliederung",
		"Eine Abschlussarbeit gliedert sich in Einleitung, Grundlagen, Hauptteil (Konzept und Umsetzung), Evaluation und Fazit. Die Einleitung benennt die Problemstellung und die Forschungsfrage; das Fazit beantwortet sie.",
		"2. Kurzfassung",
		"Der Arbeit ist eine Kurzfassung in deutscher und in englischer Sprache von jeweils höchstens einer Seite voranzustellen.",
		"3. Quellen und Zitierweise",
		"Die Arbeit stützt sich auf mindestens 20 Quellen, davon mindestens zehn wissenschaftliche Veröffentlichungen (Zeitschriften, Konferenzbände, Monografien). Zitiert wird einheitlich im Autor-Jahr-Format, zum Beispiel (Müller 2021). Internetquellen werden mit Abrufdatum angegeben.",
		"4. Abbildungen und Tabellen",
		"Abbildungen und Tabellen werden fortlaufend nummeriert, mit einer Beschriftung versehen und im Text referenziert. Übernommene Abbildungen nennen ihre Quelle.",
		"5. Abgabe",
		"Zusätzlich zur elektronischen Abgabe beim Prüfungsamt erhalten die Betreuenden den Quellcode der Implementierung als Archiv.",
	},
}

func (d rulesDoc) layout(footer string) *layout {
	l := newLayout(footer)
	l.at(marginL, 11, true, "Hochschule Musterstadt")
	l.line(150, 10, false, "Fakultät für Informatik", 14)
	l.line(marginL, 14, true, d.title, 10)
	for _, p := range d.paras {
		if strings.HasPrefix(p, "§ ") || (len(p) > 2 && p[1] == '.' && p[0] >= '1' && p[0] <= '9' && len(p) < 40) {
			l.need(20)
			l.gap(2)
			l.line(marginL, 11.5, true, p, 7)
			continue
		}
		l.para(10.5, false, p)
	}
	return l
}

// thesis is the bachelor's thesis as Word paragraphs; "\t" separates the
// title and page of a table of contents entry.
func thesis() []string {
	toc := []struct {
		entry string
		page  int
	}{
		{"1 Einleitung", 1}, {"2 Grundlagen", 6}, {"2.1 Zeitreihendaten", 6}, {"2.2 Indexstrukturen", 14},
		{"3 Konzept", 27}, {"4 Umsetzung", 45}, {"5 Evaluation", 63}, {"6 Fazit", 79},
		{"Literaturverzeichnis", 82}, {"Anhang A: Messergebnisse", thesisBodyPages + 1},
	}
	sources := []string{
		"Bayer, R.; McCreight, E. (1972): Organization and Maintenance of Large Ordered Indexes. Acta Informatica 1(3), S. 173-189.",
		"Comer, D. (1979): The Ubiquitous B-Tree. ACM Computing Surveys 11(2), S. 121-137.",
		"Graefe, G. (2011): Modern B-Tree Techniques. Foundations and Trends in Databases 3(4), S. 203-402.",
		"Jensen, S. K.; Pedersen, T. B.; Thomsen, C. (2017): Time Series Management Systems: A Survey. IEEE TKDE 29(11), S. 2581-2600.",
		"Kemper, A.; Eickler, A. (2015): Datenbanksysteme. Eine Einführung. 10. Aufl., De Gruyter.",
		"Lehman, P. L.; Yao, S. B. (1981): Efficient Locking for Concurrent Operations on B-Trees. ACM TODS 6(4), S. 650-670.",
		"Müller, K. (2021): Kompressionsverfahren für Sensordaten. Dissertation, Universität Beispielstadt.",
		"O'Neil, P. u. a. (1996): The Log-Structured Merge-Tree (LSM-Tree). Acta Informatica 33(4), S. 351-385.",
		"Pelkonen, T. u. a. (2015): Gorilla: A Fast, Scalable, In-Memory Time Series Database. PVLDB 8(12), S. 1816-1827.",
		"Saake, G.; Sattelberger, H.; Heuer, A. (2018): Datenbanken. Implementierungstechniken. 4. Aufl., mitp.",
		"Schmidt, L. (2023): Zeitreihendatenbanken im Vergleich. Online: https://example.org/zeitreihen (abgerufen am 12.03.2025).",
		"Wang, C. u. a. (2020): Apache IoTDB: Time-series Database for Internet of Things. PVLDB 13(12), S. 2901-2904.",
	}
	if len(sources) != thesisSources {
		panic("thesis sources")
	}
	paras := []string{
		"Effiziente Indexstrukturen für Zeitreihendaten",
		"Bachelorarbeit im Studiengang Informatik",
		"vorgelegt von Jana Meier, Matrikelnummer 4711123",
		"Betreuer: Herr Dr. Oliver Kranz. Abgabe: 30. September 2025.",
		"Kurzfassung",
		"Zeitreihendaten aus Sensoren wachsen schnell und werden meist über Zeitintervalle abgefragt. Die Arbeit vergleicht B+-Bäume, LSM-Bäume und eine auf Zeitintervalle spezialisierte Variante und zeigt, dass die spezialisierte Struktur Bereichsanfragen um bis zu 40 Prozent beschleunigt.",
		"Abstract",
		"Time series data from sensors grows quickly and is mostly queried by time intervals. This thesis compares B+-trees, LSM-trees and a variant specialised on time intervals and shows that the specialised structure speeds up range queries by up to 40 percent.",
		"Inhaltsverzeichnis",
	}
	for _, e := range toc {
		paras = append(paras, fmt.Sprintf("%s\t%d", e.entry, e.page))
	}
	paras = append(paras,
		"1 Einleitung",
		"Sensoren in Produktionsanlagen liefern Messwerte im Sekundentakt. Für die Auswertung werden fast immer Zeitintervalle abgefragt, etwa alle Werte einer Schicht (Jensen u. a. 2017). Klassische Indexstrukturen sind dafür nicht optimiert. Die Forschungsfrage dieser Arbeit lautet: Wie stark lassen sich Bereichsanfragen auf Zeitreihen durch eine spezialisierte Indexstruktur beschleunigen?",
		"2 Grundlagen",
		"Der B+-Baum ist die Standardindexstruktur relationaler Systeme (Bayer und McCreight 1972; Comer 1979). LSM-Bäume optimieren Schreibzugriffe, indem sie Änderungen zunächst im Hauptspeicher sammeln (O'Neil u. a. 1996). Spezialisierte Zeitreihendatenbanken komprimieren aufeinanderfolgende Werte (Pelkonen u. a. 2015).",
		"Abbildung 1: Aufbau eines B+-Baums (Quelle: eigene Darstellung nach Graefe 2011)",
		"3 Konzept",
		"Die vorgeschlagene Struktur partitioniert die Daten nach Zeitintervallen und legt für jedes Intervall einen kompakten B+-Baum an. Abgeschlossene Intervalle werden komprimiert und nur noch gelesen.",
		"4 Umsetzung",
		"Die Implementierung erfolgte in Go als Erweiterung einer bestehenden Speicherbibliothek. Tabelle 1 fasst die Konfigurationsparameter zusammen.",
		"Tabelle 1: Konfigurationsparameter der Implementierung",
		"5 Evaluation",
		"Mit Messdaten aus drei Produktionsanlagen wurden Einfüge- und Bereichsanfragen gemessen. Die spezialisierte Struktur beantwortet Bereichsanfragen um 25 bis 40 Prozent schneller als der B+-Baum; beim Einfügen liegt sie gleichauf mit dem LSM-Baum (vgl. Wang u. a. 2020).",
		"Abbildung 2: Antwortzeiten von Bereichsanfragen nach Intervalllänge (Quelle: eigene Messungen)",
		"6 Fazit",
		"Die Forschungsfrage lässt sich positiv beantworten: Eine nach Zeitintervallen partitionierte Indexstruktur beschleunigt typische Bereichsanfragen deutlich. Offen bleibt die Unterstützung verteilter Speicherung.",
		"Literaturverzeichnis",
	)
	paras = append(paras, sources...)
	paras = append(paras,
		"Anhang A: Messergebnisse",
		"Die vollständigen Messreihen befinden sich im beigefügten Archiv.",
	)
	return paras
}

// writeDocx writes a minimal Word document. Entries are stored, not
// compressed, with a fixed time: the bytes depend only on the content.
func writeDocx(path, title string, paras []string) error {
	var body strings.Builder
	for _, p := range paras {
		body.WriteString("<w:p>")
		for i, part := range strings.Split(p, "\t") {
			if i > 0 {
				body.WriteString("<w:r><w:tab/></w:r>")
			}
			var esc bytes.Buffer
			if err := xml.EscapeText(&esc, []byte(part)); err != nil {
				return err
			}
			fmt.Fprintf(&body, `<w:r><w:t xml:space="preserve">%s</w:t></w:r>`, esc.String())
		}
		body.WriteString("</w:p>")
	}
	var esc bytes.Buffer
	xml.EscapeText(&esc, []byte(title))
	files := []struct{ name, data string }{
		{"[Content_Types].xml", `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types"><Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/><Default Extension="xml" ContentType="application/xml"/><Override PartName="/word/document.xml" ContentType="application/vnd.openxmlformats-officedocument.wordprocessingml.document.main+xml"/><Override PartName="/docProps/core.xml" ContentType="application/vnd.openxmlformats-package.core-properties+xml"/></Types>`},
		{"_rels/.rels", `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"><Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument" Target="word/document.xml"/><Relationship Id="rId2" Type="http://schemas.openxmlformats.org/package/2006/relationships/metadata/core-properties" Target="docProps/core.xml"/></Relationships>`},
		{"docProps/core.xml", `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<cp:coreProperties xmlns:cp="http://schemas.openxmlformats.org/package/2006/metadata/core-properties" xmlns:dc="http://purl.org/dc/elements/1.1/"><dc:title>` + esc.String() + `</dc:title><dc:creator>Jana Meier</dc:creator></cp:coreProperties>`},
		{"word/document.xml", `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"><w:body>` + body.String() + `</w:body></w:document>`},
	}
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, f := range files {
		w, err := zw.CreateHeader(&zip.FileHeader{Name: f.name, Method: zip.Store, Modified: fixedDate})
		if err != nil {
			return err
		}
		if _, err := w.Write([]byte(f.data)); err != nil {
			return err
		}
	}
	if err := zw.Close(); err != nil {
		return err
	}
	return os.WriteFile(path, buf.Bytes(), 0o644)
}

func writeRules(dir string) error {
	rules := filepath.Join(dir, "regeln")
	work := filepath.Join(dir, "arbeit")
	for _, d := range []string{rules, work} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return err
		}
	}
	if err := regulations.layout("Prüfungsordnung Bachelor Informatik").writePDF(filepath.Join(rules, regulationsFile), regulations.title); err != nil {
		return err
	}
	if err := guide.layout("Leitfaden für Abschlussarbeiten").writePDF(filepath.Join(rules, guideFile), guide.title); err != nil {
		return err
	}
	return writeDocx(filepath.Join(work, thesisFile), "Effiziente Indexstrukturen für Zeitreihendaten", thesis())
}
