package main

import (
	"fmt"
	"math/rand/v2"
	"path/filepath"
	"strings"
)

// Use case 5: completeness. Forty job offers of an advertising agency;
// nine offer a company car in varied wording (Dienstwagen, Firmenwagen,
// Dienstfahrzeug), one of them only on its second page. Others mention
// look-alikes (bike leasing, public transport ticket, parking) that a
// question about company cars must not return.

const agencyName = "Blaupause Kommunikation GmbH"

type offer struct {
	n         int
	position  string
	candidate string
	car       string // sentence offering a company car, "" for none
	lookalike string // distracting sentence, "" for none
	late      bool   // the car sentence is on page 2
}

var positions = []string{
	"Art Director", "Senior Texterin", "Junior Texter", "Social Media Manager", "Projektmanagerin",
	"Kundenberater", "Grafikdesignerin", "Motion Designer", "UX-Designerin", "Frontend-Entwickler",
	"Mediaplanerin", "Content-Strategin", "PR-Beraterin", "Eventmanager", "Fotograf",
	"Creative Director", "Account Director", "Werbetexter", "SEO-Spezialistin", "Performance-Marketing-Manager",
	"Datenanalystin", "Produktionsleiter", "Illustratorin", "Videoredakteur", "Community-Managerin",
	"Office-Managerin", "Buchhalter", "Personalreferentin", "IT-Administrator", "Junior Art Director",
	"Strategieberater", "Markenberaterin", "Redakteur", "Konzepterin", "Webentwicklerin",
	"Kampagnenmanager", "Lektorin", "3D-Designer", "Sprecherin", "Teamassistenz",
}

// firstNames by salutation, so that "Frau" goes with a female name.
var firstNames = map[string][]string{
	"Frau": {"Anna", "Clara", "Elena", "Greta", "Isabel", "Karla", "Marie", "Olga", "Rosa", "Tanja"},
	"Herr": {"Bernhard", "Daniel", "Fabian", "Hannes", "Jakob", "Leon", "Niklas", "Philipp", "Sebastian", "Valentin"},
}
var lastNames = []string{"Adler", "Berger", "Conrad", "Dorn", "Eckert", "Falk", "Graf", "Haas", "Imhof", "Jansen", "Keller", "Lang", "Maurer", "Nagel", "Otto", "Pfeiffer", "Quandt", "Roth", "Sauer", "Thiel"}

// carSentences offer a company car; offers with these numbers get them.
var carSentences = map[int]string{
	2:  "Sie erhalten einen Dienstwagen der Mittelklasse, den Sie auch privat nutzen dürfen; die private Nutzung wird nach der 1-Prozent-Regelung versteuert.",
	7:  "Für Ihre Kundentermine stellen wir Ihnen einen Firmenwagen (Elektrofahrzeug) zur Verfügung; die Ladekosten übernimmt die Agentur.",
	11: "Ab dem siebten Monat steht Ihnen ein Dienstwagen aus unserem Fuhrpark zu, alternativ eine monatliche Mobilitätspauschale von 450 €.",
	16: "Zur Position gehört ein Dienstwagen der oberen Mittelklasse mit Tankkarte, auch zur privaten Nutzung.",
	19: "Zusätzlich stellen wir Ihnen ein Dienstfahrzeug zur Verfügung, das Sie für Dienstreisen und den Weg zur Arbeit nutzen können.",
	24: "Für Außentermine bei Kunden können Sie jederzeit einen Dienstwagen aus dem Agentur-Pool buchen.",
	28: "Sie erhalten einen Firmenwagen nach Wahl bis zu einem Listenpreis von 45.000 €, Versicherung und Wartung trägt die Agentur.",
	33: "Ein Dienstwagen mit Hybridantrieb steht Ihnen ab Arbeitsbeginn zur Verfügung; die Agentur übernimmt alle laufenden Kosten.",
	37: "Für Ihre Einsätze bei Dreharbeiten stellen wir Ihnen einen Dienstwagen (Kleintransporter) für Technik und Ausrüstung bereit.",
}

// lateCar is the offer whose car sentence is on page 2.
const lateCar = 19

var lookalikes = []string{
	"Über unser Dienstrad-Leasing können Sie ein Fahrrad oder E-Bike zu günstigen Konditionen nutzen.",
	"Die Agentur übernimmt die Kosten für das Deutschlandticket.",
	"Ein Stellplatz in der Tiefgarage des Bürogebäudes steht Ihnen kostenfrei zur Verfügung.",
	"Für die Fahrt zur Arbeit zahlen wir einen Fahrtkostenzuschuss von 50 € im Monat.",
	"Bei Dienstreisen erstatten wir Bahnfahrten in der ersten Klasse.",
}

func candidate(rng *rand.Rand) string {
	sal := []string{"Frau", "Herr"}[rng.IntN(2)]
	names := firstNames[sal]
	return fmt.Sprintf("%s %s %s", sal, names[rng.IntN(len(names))], lastNames[rng.IntN(len(lastNames))])
}

func buildOffers() []offer {
	rng := rand.New(rand.NewPCG(5, 2025))
	var out []offer
	for i, p := range positions {
		n := i + 1
		o := offer{
			n:         n,
			position:  p,
			candidate: candidate(rng),
			car:       carSentences[n],
			late:      n == lateCar,
		}
		if o.car == "" && rng.Float64() < 0.5 {
			o.lookalike = lookalikes[rng.IntN(len(lookalikes))]
		}
		out = append(out, o)
	}
	return out
}

func (o offer) file() string {
	slug := strings.NewReplacer("ä", "ae", "ö", "oe", "ü", "ue", "ß", "ss", " ", "-").Replace(strings.ToLower(o.position))
	return fmt.Sprintf("angebot-%02d-%s.pdf", o.n, slug)
}

func (o offer) layout(rng *rand.Rand) *layout {
	l := newLayout(agencyName + " · Personal")
	l.at(marginL, 11, true, agencyName)
	l.at(150, 10, false, fmt.Sprintf("Musterstadt, %02d.%02d.2025", 1+rng.IntN(28), 1+rng.IntN(12)))
	l.gap(5)
	l.line(marginL, 9, false, "Personalabteilung", 14)
	street := []string{"Gartenstraße", "Am Markt", "Lindenallee", "Bahnhofstraße", "Wiesenweg"}[rng.IntN(5)]
	l.line(marginL, 10, false, o.candidate, 5)
	l.line(marginL, 10, false, fmt.Sprintf("%s %d", street, 1+rng.IntN(80)), 5)
	l.line(marginL, 10, false, fmt.Sprintf("%05d Beispielstadt", 10000+rng.IntN(89999)), 14)
	l.line(marginL, 12, true, "Ihr Vertragsangebot als "+o.position, 10)

	salutation := "Sehr geehrte Frau "
	if strings.HasPrefix(o.candidate, "Herr") {
		salutation = "Sehr geehrter Herr "
	}
	last := o.candidate[strings.LastIndex(o.candidate, " ")+1:]
	l.para(10, false, salutation+last+",")
	l.para(10, false, fmt.Sprintf("vielen Dank für die guten Gespräche. Wir freuen uns, Ihnen die Position %s in unserer Agentur anzubieten, und fassen die wesentlichen Bedingungen hier zusammen.", o.position))
	l.line(marginL, 10, true, "1. Beginn und Arbeitszeit", 6)
	l.para(10, false, fmt.Sprintf("Das Arbeitsverhältnis beginnt am 1. %s 2026 und ist unbefristet. Die wöchentliche Arbeitszeit beträgt %d Stunden; Sie können an bis zu drei Tagen pro Woche mobil arbeiten.", months[rng.IntN(12)], []int{32, 36, 40}[rng.IntN(3)]))
	l.line(marginL, 10, true, "2. Vergütung", 6)
	l.para(10, false, fmt.Sprintf("Ihr Bruttojahresgehalt beträgt %d.000 €, zahlbar in zwölf Monatsraten. Hinzu kommt eine erfolgsabhängige Prämie von bis zu %d Prozent.", 42+rng.IntN(40), 5+rng.IntN(11)))
	l.line(marginL, 10, true, "3. Urlaub und Zusatzleistungen", 6)
	l.para(10, false, fmt.Sprintf("Sie erhalten %d Tage Urlaub im Jahr sowie einen Zuschuss zur betrieblichen Altersvorsorge.", 28+rng.IntN(3)))
	if o.lookalike != "" {
		l.para(10, false, o.lookalike)
	}
	if o.car != "" && !o.late {
		l.para(10, false, o.car)
	}
	if o.late {
		l.pages++
		l.y = marginTop
		l.line(marginL, 10, true, "4. Mobilität", 6)
		l.para(10, false, o.car)
	}
	l.line(marginL, 10, true, "Weiteres Vorgehen", 6)
	l.para(10, false, "Wir bitten Sie, uns innerhalb von zwei Wochen mitzuteilen, ob Sie das Angebot annehmen. Den Arbeitsvertrag senden wir Ihnen dann zur Unterschrift zu.")
	l.para(10, false, "Mit freundlichen Grüßen")
	l.line(marginL, 10, false, "Johanna Wolff, Geschäftsführerin", 5)
	return l
}

func writeOffers(dir string, os []offer) error {
	rng := rand.New(rand.NewPCG(7, 2025))
	for _, o := range os {
		if err := o.layout(rng).writePDF(filepath.Join(dir, o.file()), "Vertragsangebot "+o.position); err != nil {
			return err
		}
	}
	return nil
}

// carFiles are the offers with a company car; lookalikeFiles the ones
// with a distracting sentence.
func carFiles(os []offer) (hits, lookalike []string) {
	for _, o := range os {
		switch {
		case o.car != "":
			hits = append(hits, o.file())
		case o.lookalike != "":
			lookalike = append(lookalike, o.file())
		}
	}
	return hits, lookalike
}
