package main

import (
	"fmt"
	"math/rand/v2"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
)

// Use case 1: minutes of a university senate. Twelve sessions with the
// layout that broke real extraction: participants in a left column next to
// guests and excused members on the right, headings sometimes one text
// object padded with spaces, titles, a deputy "i. V.", names in the running
// text, and one session only as a scan. All names are invented.

const (
	firstSession = 41
	sessions     = 12
	scanSession  = 47 // only as an image; needs OCR
	colLeft      = marginL
	colRight     = 115.0
	listSize     = 10.0
	listLH       = 4.6
)

// member is a senate member with the chance to attend a session.
type member struct {
	name   string // with salutation and title, as printed
	chance float64
}

var members = []member{
	{"Herr Prof. Dr. Martin Albers", 1}, // president, chairs every session
	{"Frau Prof. Dr. Helga Brandt", 0.9},
	{"Herr Prof. Dr. Stefan Kuhlmann", 0.8},
	{"Frau Prof. Dr. Ines Wörner", 0.55},
	{"Herr Prof. Dr. Jörg Feldmann", 0.95},
	{"Frau Prof. Dr. Katrin Lenz", 0.7},
	{"Herr Prof. Dr. Peter Sommer", 0.45},
	{"Herr Prof. Dr. Ralf Ostermann", 0.85},
	{"Frau Prof. Dr. Birgit Hollmann", 0.75},
	{"Herr Prof. Dr. Thomas Engel", 0.6},
	{"Herr Prof. Dr. Andreas Vogt", 0.9},
	{"Frau Prof. Dr. Susanne Pohl", 0.65},
	{"Herr Prof. Dr. Klaus Reimers", 0.8},
	{"Herr Dr. Frank Lindner", 0.95},
	{"Frau Dr. Julia Hartwig", 0.7},
	{"Herr Dr. Oliver Kranz", 0.5},
	{"Frau Sabine Möller", 0.85},
	{"Herr Uwe Brenner", 0.75},
	{"Frau Lea Schubert", 0.6},
	{"Herr Felix Neumeyer", 0.8},
	{"Frau Mia Rösler", 0.4},
	{"Herr Jonas Petersen", 0.7},
	{"Herr Lukas Wendt", 0.55},
	{"Frau Petra Gärtner", 0.9},
	{"Herr Bernd Ulrich", 0.65},
	{"Frau Anja Voigt", 0.8},
	{"Frau Dr. Carola Seidel", 0.85},
	{"Herr Dirk Hansen", 0.5},
}

// Guests attend often but are not participants; the chancellor and the
// minute taker attend every session.
var (
	regularGuests = []string{"Frau Dr. Renate Kühn", "Herr Michael Bauer"}
	otherGuests   = []string{"Frau Nina Albrecht", "Herr Dr. Georg Wiese", "Frau Claudia Reuter", "Herr Simon Krause", "Frau Dr. Lisa Hoffmann"}
	topGuests     = []string{"Frau Prof. Dr. Eva Kirchner", "Herr Prof. Dr. Paul Ritter", "Herr Prof. Dr. Henrik Lorenz", "Frau Prof. Dr. Marion Busch"}
	// deputy stands in for an absent student member in some sessions.
	deputy       = "Herr Tim Köhler"
	deputyFor    = 18 // Frau Lea Schubert
	deputyInSess = []int{44, 49, 52}
)

type session struct {
	number   int
	date     string   // "4. März 2025"
	dateNum  string   // "04.03.2025"
	present  []string // participant list as printed (members, deputy line)
	guests   []string // as printed
	excused  []string
	tops     []top
	splitHdr bool // headings as one text object padded with spaces
}

type top struct {
	title    string
	body     []string
	decision string // "" when no decision is taken
}

var months = []string{"Januar", "Februar", "März", "April", "Mai", "Juni", "Juli", "August", "September", "Oktober", "November", "Dezember"}

// buildSessions creates the twelve sessions deterministically.
func buildSessions() []session {
	rng := rand.New(rand.NewPCG(41, 2025))
	var out []session
	for i := range sessions {
		n := firstSession + i
		day := 2 + rng.IntN(6)
		s := session{
			number:   n,
			date:     fmt.Sprintf("%d. %s 2025", day, months[i]),
			dateNum:  fmt.Sprintf("%02d.%02d.2025", day, i+1),
			splitHdr: n%2 == 0,
		}
		withDeputy := slices.Contains(deputyInSess, n)
		for k, m := range members {
			if withDeputy && k == deputyFor {
				s.excused = append(s.excused, m.name)
				continue
			}
			if rng.Float64() < m.chance {
				name := m.name
				if k == 0 {
					name += " (Vorsitz)"
				}
				s.present = append(s.present, name)
			} else {
				s.excused = append(s.excused, m.name)
			}
		}
		if withDeputy {
			s.present = append(s.present, deputy+" (i. V. Frau Schubert)")
		}
		s.guests = append(s.guests, regularGuests[0]+" (Kanzlerin)", regularGuests[1]+" (Protokoll)")
		for _, g := range otherGuests {
			if rng.Float64() < 0.3 {
				s.guests = append(s.guests, g)
			}
		}
		s.tops = sessionTops(n, s, rng)
		out = append(out, s)
	}
	return out
}

// voters is the number of members present (the deputy votes, too).
func (s session) voters() int { return len(s.present) }

func votes(s session, rng *rand.Rand, against int) string {
	abst := rng.IntN(3)
	yes := s.voters() - against - abst
	return fmt.Sprintf("Abstimmung: %d Ja, %d Nein, %d Enthaltungen.", yes, against, abst)
}

func sessionTops(n int, s session, rng *rand.Rand) []top {
	absent := s.excused[rng.IntN(len(s.excused))]
	guest := topGuests[n%len(topGuests)]
	tops := []top{{
		title: "Begrüßung, Feststellung der Beschlussfähigkeit, Genehmigung der Tagesordnung",
		body:  []string{fmt.Sprintf("Der Präsident, %s, begrüßt die Mitglieder und Gäste und stellt die Beschlussfähigkeit fest. Die Tagesordnung wird ohne Änderungen genehmigt. %s hat sich für die heutige Sitzung entschuldigt.", strings.TrimPrefix(members[0].name, "Herr "), absent)},
	}}
	reports := []string{
		"Der Präsident berichtet über die Ergebnisse der Zielvereinbarungen mit dem Land. Die Hochschule erhält ab dem kommenden Jahr zusätzliche Mittel für die Digitalisierung der Lehre.",
		"Der Präsident informiert über die Akkreditierung der Studiengänge der Fakultät für Informatik, die ohne Auflagen abgeschlossen wurde.",
		"Der Präsident berichtet über die Gespräche mit der Stadt zur Anbindung des Campus an den Nahverkehr. Ab dem Wintersemester verkehrt die Buslinie 5 im Zehnminutentakt.",
		"Der Präsident gratuliert den Preisträgerinnen und Preisträgern des Forschungspreises und informiert über die Exzellenzstrategie.",
	}
	ref := s.excused[rng.IntN(len(s.excused))]
	tops = append(tops, top{
		title: "Bericht des Präsidenten",
		body: []string{reports[n%len(reports)],
			fmt.Sprintf("%s hat dazu vorab eine schriftliche Stellungnahme eingereicht, die dem Protokoll beiliegt. Nachfragen beantwortet die Kanzlerin, Frau Dr. Renate Kühn.", ref)},
	})
	switch n {
	case 42:
		tops = append(tops, top{
			title: "Wirtschaftsplan 2025 der Hochschule",
			body: []string{"Die Kanzlerin, Frau Dr. Renate Kühn, erläutert den Entwurf des Wirtschaftsplans 2025. Die Personalausgaben steigen um 3,1 Prozent, die Sachmittel der Fakultäten bleiben unverändert. Für die Sanierung des Hörsaalgebäudes werden 2,4 Mio. € eingeplant.",
				"In der Aussprache wird nach den Rücklagen für Berufungen gefragt; sie betragen 1,8 Mio. €."},
			decision: "Beschluss: Der Senat stimmt dem Wirtschaftsplan 2025 zu. " + votes(s, rng, 1),
		})
	case 45:
		tops = append(tops, top{
			title: "Neufassung der Bibliotheksordnung (erste Lesung)",
			body: []string{"Die Leiterin der Bibliothek stellt den Entwurf der neuen Bibliotheksordnung vor. Neu sind die Ausleihfristen für Abschlussarbeiten und die Regeln für Gruppenarbeitsräume.",
				"Der Senat diskutiert den Entwurf. Die Beschlussfassung erfolgt in einer späteren Sitzung, wenn die Stellungnahme der Studierendenvertretung vorliegt."},
		})
	case 46:
		tops = append(tops, top{
			title: "Bericht zur Haushaltslage",
			body: []string{"Die Kanzlerin berichtet über die Haushaltslage zum Halbjahr. Die Energiekosten liegen 12 Prozent über dem Plan; ein Nachtragshaushalt wird vorbereitet.",
				"Der Senat nimmt den Bericht zur Kenntnis. Ein Beschluss wird nicht gefasst."},
		})
	case 47:
		tops = append(tops, top{
			title: "Neubau der Mensa am Campus Nord",
			body:  []string{"Das Studierendenwerk stellt die Planung für den Neubau der Mensa am Campus Nord vor. Baubeginn ist im Frühjahr 2026, die Fertigstellung ist für das Wintersemester 2027 geplant. Während der Bauzeit wird eine Interimsmensa im Foyer des Hörsaalgebäudes eingerichtet."},
		})
	case 48:
		tops = append(tops, top{
			title:    "Neufassung der Bibliotheksordnung",
			body:     []string{"Die Stellungnahme der Studierendenvertretung liegt vor; ihre Anregung zu längeren Öffnungszeiten in der Prüfungszeit wurde übernommen."},
			decision: "Beschluss: Der Senat beschließt die Neufassung der Bibliotheksordnung in der vorgelegten Fassung. " + votes(s, rng, 0),
		})
	case 51:
		tops = append(tops, top{
			title:    "Nachtragshaushalt 2025",
			body:     []string{"Die Kanzlerin legt den Nachtragshaushalt 2025 vor. Die Mehrkosten für Energie von 1,2 Mio. € werden aus Rücklagen gedeckt."},
			decision: "Beschluss: Der Senat stimmt dem Nachtragshaushalt 2025 zu. " + votes(s, rng, 2),
		})
	}
	fields := []string{"Elektrotechnik", "Maschinenbau", "Informatik", "Mathematik", "Wirtschaftswissenschaften", "Medienwissenschaft"}
	f := fields[n%len(fields)]
	tops = append(tops, top{
		title:    fmt.Sprintf("Berufungsvorschlag für die W3-Professur „%s“", "Grundlagen der "+f),
		body:     []string{fmt.Sprintf("%s berichtet für die Berufungskommission über das Verfahren. Es gingen %d Bewerbungen ein, vier Personen wurden zu Vorträgen eingeladen.", guest, 18+rng.IntN(30))},
		decision: "Beschluss: Der Senat nimmt zum Berufungsvorschlag zustimmend Stellung. " + votes(s, rng, 0),
	})
	tops = append(tops, top{
		title: "Termine und Sonstiges",
		body:  []string{fmt.Sprintf("Die nächste Sitzung findet am ersten Dienstag des Folgemonats statt. Unter Sonstiges weist %s auf die Ausschreibung der Lehrpreise hin.", s.guests[0][:strings.Index(s.guests[0], " (")])},
	})
	return tops
}

// printedGuests is the guest list including the TOP guest added by
// sessionTops.
func (s session) printedGuests() []string {
	guest := topGuests[s.number%len(topGuests)]
	return append(slices.Clone(s.guests), guest+fmt.Sprintf(" (TOP %d)", len(s.tops)-1))
}

func (s session) file() string { return fmt.Sprintf("protokoll-%d.pdf", s.number) }

func (s session) layout() (*layout, error) {
	l := newLayout(fmt.Sprintf("Protokoll der %d. Sitzung des Senats", s.number))
	l.at(colLeft, 11, true, "Hochschule Musterstadt")
	l.at(150, 10, false, "Musterstadt, "+s.dateNum)
	l.gap(5)
	l.line(colLeft, 9, false, "Der Präsident", 12)
	l.line(colLeft, 13, true, fmt.Sprintf("Protokoll der %d. Sitzung des Senats am %s", s.number, s.date), 10)

	if s.splitHdr {
		// One text object; spaces fill the gap up to the right column.
		head := "Teilnehmer/-innen:"
		n := int((colRight - colLeft - l.width(head, listSize, true)) / l.width(" ", listSize, true))
		l.line(colLeft, listSize, true, head+strings.Repeat(" ", n)+"Gäste:", listLH)
	} else {
		l.at(colLeft, listSize, true, "Teilnehmer/-innen:")
		l.line(colRight, listSize, true, "Gäste:", listLH)
	}
	right := append(s.printedGuests(), "", "Entschuldigt:")
	right = append(right, s.excused...)
	rows := max(len(s.present), len(right))
	for i := range rows {
		if i < len(s.present) {
			if w := l.width(s.present[i], listSize, false); colLeft+w > colRight-20 {
				return nil, fmt.Errorf("%s: %q too wide for the left column", s.file(), s.present[i])
			}
			l.at(colLeft, listSize, false, s.present[i])
		}
		if i < len(right) && right[i] != "" {
			l.at(colRight, listSize, right[i] == "Entschuldigt:", right[i])
		}
		l.gap(listLH)
	}
	l.gap(6)
	l.line(colLeft, 11, true, "Tagesordnung:", 6)
	for i, t := range s.tops {
		l.line(colLeft, 10, false, fmt.Sprintf("%d. %s", i+1, t.title), 5)
	}
	l.gap(8)
	for i, t := range s.tops {
		l.need(20)
		l.line(colLeft, 11, true, fmt.Sprintf("TOP %d: %s", i+1, t.title), 6.5)
		for _, p := range t.body {
			l.para(10, false, p)
		}
		if t.decision != "" {
			l.para(10, true, t.decision)
		}
		l.gap(3)
	}
	l.gap(6)
	l.line(colLeft, 10, false, "Musterstadt, "+s.dateNum, 5)
	l.line(colLeft, 10, false, "gez. Michael Bauer (Protokoll)        gez. Prof. Dr. Martin Albers (Präsident)", 5)
	return l, nil
}

var titleRE = regexp.MustCompile(`^(Frau|Herr)\s+|(Prof\.|Dr\.)\s+|\s*\(.*\)$`)

// personName strips salutation, titles and a trailing remark: "Herr Prof.
// Dr. Martin Albers (Vorsitz)" → "Martin Albers".
func personName(printed string) string {
	s := strings.TrimSpace(printed)
	for {
		t := strings.TrimSpace(titleRE.ReplaceAllString(s, ""))
		if t == s {
			return t
		}
		s = t
	}
}

// attendance counts in how many sessions each person took part.
func attendance(ss []session) map[string]int {
	counts := map[string]int{}
	for _, s := range ss {
		for _, p := range s.present {
			counts[personName(p)]++
		}
	}
	return counts
}

func writeProtocols(dir string, ss []session) error {
	for _, s := range ss {
		l, err := s.layout()
		if err != nil {
			return err
		}
		title := fmt.Sprintf("Protokoll der %d. Sitzung des Senats", s.number)
		path := filepath.Join(dir, s.file())
		if s.number == scanSession {
			err = l.writeScan(path, title)
		} else {
			err = l.writePDF(path, title)
		}
		if err != nil {
			return err
		}
	}
	return nil
}
