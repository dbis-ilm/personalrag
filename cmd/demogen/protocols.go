package main

import (
	"fmt"
	"math/rand/v2"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
)

// Use case 1: minutes of a sports club's committee. Twelve sessions with
// the layout that broke real extraction: participants in a left column next
// to guests and excused members on the right, headings sometimes one text
// object padded with spaces, a deputy "i. V.", names in the running text,
// and one session only as a scan. All names are invented.

const (
	clubName     = "TSV Musterstadt 1890 e.V."
	firstSession = 41
	sessions     = 12
	scanSession  = 47 // only as an image; needs OCR
	colLeft      = marginL
	colRight     = 115.0
	listSize     = 10.0
	listLH       = 4.6
)

// member is a committee member with the chance to attend a session.
type member struct {
	name   string // with salutation and title, as printed
	chance float64
}

var members = []member{
	{"Herr Martin Albers", 1}, // chairman, chairs every session
	{"Frau Helga Brandt", 0.9},
	{"Herr Stefan Kuhlmann", 0.8},
	{"Frau Dr. Ines Wörner", 0.55},
	{"Herr Jörg Feldmann", 0.95},
	{"Frau Katrin Lenz", 0.7},
	{"Herr Peter Sommer", 0.45},
	{"Herr Ralf Ostermann", 0.85},
	{"Frau Birgit Hollmann", 0.75},
	{"Herr Dr. Thomas Engel", 0.6},
	{"Herr Andreas Vogt", 0.9},
	{"Frau Susanne Pohl", 0.65},
	{"Herr Klaus Reimers", 0.8},
	{"Herr Frank Lindner", 0.95},
	{"Frau Julia Hartwig", 0.7},
	{"Herr Oliver Kranz", 0.5},
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

// Guests attend often but are not participants; the treasurer's auditor
// and the minute taker attend every session.
var (
	regularGuests = []string{"Frau Renate Kühn", "Herr Michael Bauer"}
	otherGuests   = []string{"Frau Nina Albrecht", "Herr Georg Wiese", "Frau Claudia Reuter", "Herr Simon Krause", "Frau Lisa Hoffmann"}
	topGuests     = []string{"Frau Eva Kirchner", "Herr Paul Ritter", "Herr Henrik Lorenz", "Frau Marion Busch"}
	// deputy stands in for the absent youth representative in some
	// sessions.
	deputy       = "Herr Tim Köhler"
	deputyFor    = 18 // Frau Lea Schubert
	deputyInSess = []int{44, 49, 52}
)

type session struct {
	number   int
	date     string   // "4. März 2025"
	dateNum  string   // "04.03.2025"
	present  []string // participant list as printed (members, deputy line)
	guests   []string // as printed, without the TOP guest
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
		s.guests = append(s.guests, regularGuests[0]+" (Kassenprüferin)", regularGuests[1]+" (Protokoll)")
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
		body:  []string{fmt.Sprintf("Der Vorsitzende, %s, begrüßt die Mitglieder des Ausschusses und die Gäste und stellt die Beschlussfähigkeit fest. Die Tagesordnung wird ohne Änderungen genehmigt. %s hat sich für die heutige Sitzung entschuldigt.", strings.TrimPrefix(members[0].name, "Herr "), absent)},
	}}
	reports := []string{
		"Der Vorsitzende berichtet über die Mitgliederentwicklung. Der Verein hat derzeit 1.284 Mitglieder, 37 mehr als zu Jahresbeginn; der größte Zuwachs entfällt auf die Abteilung Turnen.",
		"Der Vorsitzende informiert über die Gespräche mit der Stadt zur Nutzung der Sporthalle am Lindenweg. Ab dem Herbst stehen dem Verein dienstags und donnerstags zusätzliche Hallenzeiten zur Verfügung.",
		"Der Vorsitzende berichtet vom Kreissporttag. Die Fußball-A-Jugend ist in die Bezirksliga aufgestiegen; der Ausschuss gratuliert Mannschaft und Trainerteam.",
		"Der Vorsitzende informiert über die Ehrungen langjähriger Mitglieder beim Neujahrsempfang und die Planung des Sommerfests am Sportplatz.",
	}
	ref := s.excused[rng.IntN(len(s.excused))]
	tops = append(tops, top{
		title: "Bericht des Vorsitzenden",
		body: []string{reports[n%len(reports)],
			fmt.Sprintf("%s hat dazu vorab eine schriftliche Stellungnahme eingereicht, die dem Protokoll beiliegt. Nachfragen beantwortet die Kassenprüferin, Frau Renate Kühn.", ref)},
	})
	switch n {
	case 42:
		tops = append(tops, top{
			title: "Haushaltsplan 2025",
			body: []string{"Der Kassenwart erläutert den Entwurf des Haushaltsplans 2025. Die Mitgliedsbeiträge bringen 186.000 €, die Hallenmieten steigen um 6 Prozent. Für die Erneuerung der Flutlichtanlage werden 42.000 € eingeplant.",
				"In der Aussprache wird nach der Rücklage für das Vereinsheim gefragt; sie beträgt 58.000 €."},
			decision: "Beschluss: Der Vereinsausschuss stimmt dem Haushaltsplan 2025 zu und empfiehlt ihn der Mitgliederversammlung. " + votes(s, rng, 1),
		})
	case 45:
		tops = append(tops, top{
			title: "Neue Beitragsordnung (erste Lesung)",
			body: []string{"Der Kassenwart stellt den Entwurf der neuen Beitragsordnung vor. Neu sind ein Familienbeitrag und ermäßigte Beiträge für Auszubildende und Studierende.",
				"Der Ausschuss diskutiert den Entwurf. Die Beschlussfassung erfolgt in einer späteren Sitzung, wenn die Stellungnahmen der Abteilungen vorliegen."},
		})
	case 46:
		tops = append(tops, top{
			title: "Kassenbericht zum Halbjahr",
			body: []string{"Der Kassenwart berichtet über die Kassenlage zum Halbjahr. Die Energiekosten des Vereinsheims liegen 15 Prozent über dem Plan; ein Nachtragshaushalt wird vorbereitet.",
				"Der Ausschuss nimmt den Bericht zur Kenntnis. Ein Beschluss wird nicht gefasst."},
		})
	case 47:
		tops = append(tops, top{
			title: "Neubau des Vereinsheims",
			body:  []string{"Der Bauausschuss stellt die Planung für den Neubau des Vereinsheims am Sportplatz vor. Baubeginn ist im Frühjahr 2026, die Fertigstellung ist für den Herbst 2027 geplant. Während der Bauzeit nutzt der Verein Container als Umkleiden und Geschäftsstelle."},
		})
	case 48:
		tops = append(tops, top{
			title:    "Neue Beitragsordnung",
			body:     []string{"Die Stellungnahmen der Abteilungen liegen vor; die Anregung der Abteilung Schwimmen zu einem Kursbeitrag wurde übernommen."},
			decision: "Beschluss: Der Vereinsausschuss beschließt die neue Beitragsordnung in der vorgelegten Fassung. " + votes(s, rng, 0),
		})
	case 51:
		tops = append(tops, top{
			title:    "Nachtragshaushalt 2025",
			body:     []string{"Der Kassenwart legt den Nachtragshaushalt 2025 vor. Die Mehrkosten für Energie von 9.500 € werden aus der Rücklage gedeckt."},
			decision: "Beschluss: Der Vereinsausschuss stimmt dem Nachtragshaushalt 2025 zu. " + votes(s, rng, 2),
		})
	}
	sections := []string{"Fußball", "Handball", "Turnen", "Leichtathletik", "Schwimmen", "Tischtennis"}
	sec := sections[n%len(sections)]
	tops = append(tops, top{
		title:    fmt.Sprintf("Bericht der Abteilung %s", sec),
		body:     []string{fmt.Sprintf("%s berichtet für die Abteilung %s über den Trainingsbetrieb. Die Abteilung hat %d aktive Mitglieder; für die neue Saison werden zwei weitere Übungsleiter gesucht.", guest, sec, 40+rng.IntN(120))},
		decision: fmt.Sprintf("Beschluss: Der Vereinsausschuss bewilligt der Abteilung %s einen Zuschuss von %d € für Trainingsmaterial. ", sec, 500+100*rng.IntN(20)) + votes(s, rng, 0),
	})
	tops = append(tops, top{
		title: "Termine und Sonstiges",
		body:  []string{fmt.Sprintf("Die nächste Sitzung findet am ersten Dienstag des Folgemonats im Vereinsheim statt. Unter Sonstiges weist %s auf die Anmeldung zum Vereinslauf hin.", s.guests[0][:strings.Index(s.guests[0], " (")])},
	})
	return tops
}

// printedGuests is the guest list including the guest reporting for a
// section.
func (s session) printedGuests() []string {
	guest := topGuests[s.number%len(topGuests)]
	return append(slices.Clone(s.guests), guest+fmt.Sprintf(" (TOP %d)", len(s.tops)-1))
}

func (s session) file() string { return fmt.Sprintf("protokoll-%d.pdf", s.number) }

func (s session) title() string {
	return fmt.Sprintf("Protokoll der %d. Sitzung des Vereinsausschusses", s.number)
}

func (s session) layout() (*layout, error) {
	l := newLayout(s.title())
	l.at(colLeft, 11, true, clubName)
	l.at(150, 10, false, "Musterstadt, "+s.dateNum)
	l.gap(5)
	l.line(colLeft, 9, false, "Der Vorstand", 12)
	l.line(colLeft, 13, true, fmt.Sprintf("%s am %s", s.title(), s.date), 10)

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
	l.line(colLeft, 10, false, "gez. Michael Bauer (Protokoll)        gez. Martin Albers (Vorsitzender)", 5)
	return l, nil
}

var titleRE = regexp.MustCompile(`^(Frau|Herr)\s+|(Prof\.|Dr\.)\s+|\s*\(.*\)$`)

// personName strips salutation, titles and a trailing remark: "Herr Dr.
// Thomas Engel (Vorsitz)" → "Thomas Engel".
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
		path := filepath.Join(dir, s.file())
		if s.number == scanSession {
			err = l.writeScan(path, s.title())
		} else {
			err = l.writePDF(path, s.title())
		}
		if err != nil {
			return err
		}
	}
	return nil
}
