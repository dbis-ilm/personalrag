package store

import "strings"

// GermanStopWords is a compact stop-word list for keyword search over German
// (and some English) questions: function words plus question words that
// never carry the topic.
var GermanStopWords = func() map[string]bool {
	words := `aber alle allem allen aller alles als also am an ander andere anderen anderer anderes auch auf aus bei beim
		bin bis bist da damit dann das dass dein deine dem den denn der des dessen dich die dies diese diesem diesen dieser
		dieses dir doch dort du durch ein eine einem einen einer eines er es etwas euch euer für gegen gibt gilt hab habe haben
		hat hatte hätte ich ihm ihn ihnen ihr ihre im in ist ja jede jedem jeden jeder jedes kann kein keine können könnte
		man mein meine meinem meinen meiner meines mich mir mit muss müssen nach nicht noch nun nur ob oder ohne sehr sein
		seine sich sie sind so soll sollte steht über um und uns unser unter vom von vor war waren was weil welche welchem
		welchen welcher welches wenn wer werden wie wieso wieviel wird wir wo woher wohin worden wurde würde zu zum zur
		bitte sagen zeige zeig finde gibt's
		the and are for how what when where which who why with does from this that`
	m := map[string]bool{}
	for _, w := range strings.Fields(words) {
		m[w] = true
	}
	return m
}()
