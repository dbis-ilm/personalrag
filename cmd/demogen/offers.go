package main

import (
	"fmt"
	"math/rand/v2"
	"path/filepath"
	"strings"
)

// Use case 5: completeness. Forty offer letters for professorships; nine
// mention large-scale equipment ("Großgerät") in varied wording, one of them
// only on its second page. Others mention look-alikes (small equipment,
// open-plan labs) that a question about Großgeräte must not return.

type offer struct {
	n         int
	field     string
	grade     string
	candidate string
	equipment string // sentence about Großgeräte, "" for none
	lookalike string // distracting sentence, "" for none
	late      bool   // the Großgeräte sentence is on page 2
}

var offerFields = []string{
	"Theoretische Physik", "Organische Chemie", "Datenbanksysteme", "Regelungstechnik", "Werkstoffwissenschaft",
	"Analytische Chemie", "Festkörperphysik", "Softwaretechnik", "Mikrosystemtechnik", "Biomedizinische Technik",
	"Volkswirtschaftslehre", "Marketing", "Medienpsychologie", "Angewandte Mathematik", "Statistik",
	"Thermodynamik", "Strömungsmechanik", "Elektrische Energietechnik", "Nachrichtentechnik", "Robotik",
	"Maschinelles Lernen", "IT-Sicherheit", "Rechnerarchitektur", "Optik", "Quantentechnologie",
	"Umweltverfahrenstechnik", "Fahrzeugtechnik", "Produktionstechnik", "Bauinformatik", "Wirtschaftsinformatik",
	"Unternehmensrechnung", "Medienwissenschaft", "Didaktik der Informatik", "Mathematische Optimierung", "Geoinformatik",
	"Biophysik", "Halbleitertechnik", "Akustik", "Kommunikationsnetze", "Technische Mechanik",
}

var firstNames = []string{"Anna", "Bernhard", "Clara", "Daniel", "Elena", "Fabian", "Greta", "Hannes", "Isabel", "Jakob", "Karla", "Leon", "Marie", "Niklas", "Olga", "Philipp", "Rosa", "Sebastian", "Tanja", "Valentin"}
var lastNames = []string{"Adler", "Berger", "Conrad", "Dorn", "Eckert", "Falk", "Graf", "Haas", "Imhof", "Jansen", "Keller", "Lang", "Maurer", "Nagel", "Otto", "Pfeiffer", "Quandt", "Roth", "Sauer", "Thiel"}

// equipmentSentences mention Großgeräte; offers with these numbers get them.
var equipmentSentences = map[int]string{
	2:  "Darüber hinaus sagt die Hochschule Ihnen ein Großgerät (Rasterelektronenmikroskop) im Wert von bis zu 650.000 € zu, das als Großgeräteantrag nach Art. 91b GG gemeinsam mit Ihnen beantragt wird.",
	7:  "Die Hochschule unterstützt Sie bei der Beantragung eines Großgeräts bei der Deutschen Forschungsgemeinschaft und übernimmt den Eigenanteil von 15 Prozent.",
	11: "Sie werden an der Beschaffung des Großgeräts „Hochleistungsrechner“ beteiligt, das ab 2026 im Rechenzentrum betrieben wird.",
	16: "Die Großgeräte der Fakultät, insbesondere der Kernspintomograph, stehen Ihnen zur Mitnutzung zur Verfügung.",
	19: "Für die Großgeräteinvestition „Laserlabor“ stellt die Hochschule eine Kofinanzierung von 200.000 € bereit.",
	24: "Für ein Massenspektrometer (Großgerät) werden einmalig 420.000 € aus Mitteln der Hochschule bereitgestellt.",
	28: "Sie erhalten Zugang zu den Großgeräten des Zentrums für Mikroskopie und Nanoanalytik.",
	33: "Die Hochschule wird für Ihre Forschung einen Großgeräteantrag im Programm Forschungsbauten stellen.",
	37: "Ein Großgerät für die Halbleiterfertigung (Lithografieanlage) wird im Reinraum des Instituts aufgestellt und steht Ihrer Arbeitsgruppe zur Verfügung.",
}

// lateEquipment is the offer whose Großgeräte sentence is on page 2.
const lateEquipment = 19

var lookalikes = []string{
	"Für Geräte und Software in der Lehre stehen jährlich 8.000 € zur Verfügung.",
	"Kleingeräte bis 10.000 € können aus den laufenden Sachmitteln beschafft werden.",
	"Ihre Arbeitsgruppe erhält Flächen in einem Großraumlabor mit 120 m².",
	"Die Werkstatt der Fakultät fertigt Versuchsaufbauten und Laborgeräte für Sie.",
	"Ein großes Büro mit Besprechungsbereich wird Ihnen im Neubau zugewiesen.",
}

func buildOffers() []offer {
	rng := rand.New(rand.NewPCG(5, 2025))
	var out []offer
	for i, f := range offerFields {
		n := i + 1
		o := offer{
			n:         n,
			field:     f,
			grade:     []string{"W2", "W3"}[rng.IntN(2)],
			candidate: fmt.Sprintf("%s %s %s", []string{"Frau Dr.", "Herr Dr."}[rng.IntN(2)], firstNames[rng.IntN(len(firstNames))], lastNames[rng.IntN(len(lastNames))]),
			equipment: equipmentSentences[n],
			late:      n == lateEquipment,
		}
		if o.equipment == "" && rng.Float64() < 0.5 {
			o.lookalike = lookalikes[rng.IntN(len(lookalikes))]
		}
		out = append(out, o)
	}
	return out
}

func (o offer) file() string {
	slug := strings.NewReplacer("ä", "ae", "ö", "oe", "ü", "ue", "ß", "ss", " ", "-").Replace(strings.ToLower(o.field))
	return fmt.Sprintf("berufungsangebot-%02d-%s.pdf", o.n, slug)
}

func (o offer) layout(rng *rand.Rand) *layout {
	l := newLayout("Hochschule Musterstadt · Der Präsident")
	l.at(marginL, 11, true, "Hochschule Musterstadt")
	l.at(150, 10, false, fmt.Sprintf("Musterstadt, %02d.%02d.2025", 1+rng.IntN(28), 1+rng.IntN(12)))
	l.gap(5)
	l.line(marginL, 9, false, "Der Präsident", 14)
	l.line(marginL, 10, false, o.candidate, 5)
	l.line(marginL, 10, false, "Universität Beispielstadt", 5)
	l.line(marginL, 10, false, "Institut für "+o.field, 14)
	l.line(marginL, 12, true, fmt.Sprintf("Berufung auf die %s-Professur „%s“", o.grade, o.field), 10)

	salutation := "Sehr geehrte Frau Dr. "
	if strings.HasPrefix(o.candidate, "Herr") {
		salutation = "Sehr geehrter Herr Dr. "
	}
	last := o.candidate[strings.LastIndex(o.candidate, " ")+1:]
	l.para(10, false, salutation+last+",")
	l.para(10, false, fmt.Sprintf("ich freue mich, Ihnen den Ruf auf die %s-Professur „%s“ an der Hochschule Musterstadt zu erteilen. Mit diesem Schreiben unterbreite ich Ihnen das Angebot der Hochschule zur Ausstattung der Professur.", o.grade, o.field))
	l.line(marginL, 10, true, "1. Personal", 6)
	l.para(10, false, fmt.Sprintf("Der Professur werden %d Stellen für wissenschaftliche Mitarbeiterinnen und Mitarbeiter (E13, 100 Prozent) sowie eine halbe Stelle im Sekretariat zugeordnet.", 2+rng.IntN(3)))
	l.line(marginL, 10, true, "2. Sachmittel", 6)
	l.para(10, false, fmt.Sprintf("Für laufende Sachausgaben erhalten Sie jährlich %d.000 €. Hinzu kommt eine einmalige Erstausstattung von %d.000 € für Laborausstattung, Rechner und Literatur.", 10+rng.IntN(20), 50+rng.IntN(150)))
	if o.lookalike != "" {
		l.para(10, false, o.lookalike)
	}
	if o.equipment != "" && !o.late {
		l.para(10, false, o.equipment)
	}
	l.line(marginL, 10, true, "3. Räume", 6)
	l.para(10, false, fmt.Sprintf("Ihnen werden Büro- und Laborflächen von insgesamt %d m² im Gebäude %s zugewiesen.", 60+rng.IntN(200), []string{"A", "B", "C", "Nord"}[rng.IntN(4)]))
	if o.late {
		l.pages++
		l.y = marginTop
		l.line(marginL, 10, true, "4. Investitionen", 6)
		l.para(10, false, o.equipment)
	}
	l.line(marginL, 10, true, "Weiteres Verfahren", 6)
	l.para(10, false, "Ich bitte Sie, mir innerhalb von acht Wochen mitzuteilen, ob Sie das Angebot annehmen. Für Rückfragen steht Ihnen das Berufungsmanagement gern zur Verfügung.")
	l.para(10, false, "Mit freundlichen Grüßen")
	l.line(marginL, 10, false, "Prof. Dr. Martin Albers", 5)
	return l
}

func writeOffers(dir string, os []offer) error {
	rng := rand.New(rand.NewPCG(7, 2025))
	for _, o := range os {
		if err := o.layout(rng).writePDF(filepath.Join(dir, o.file()), "Berufungsangebot "+o.field); err != nil {
			return err
		}
	}
	return nil
}

// equipmentFiles are the offers mentioning Großgeräte; lookalikeFiles the
// ones with a distracting sentence.
func equipmentFiles(os []offer) (hits, lookalike []string) {
	for _, o := range os {
		switch {
		case o.equipment != "":
			hits = append(hits, o.file())
		case o.lookalike != "":
			lookalike = append(lookalike, o.file())
		}
	}
	return hits, lookalike
}
