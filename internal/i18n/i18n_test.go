package i18n

import (
	"errors"
	"regexp"
	"slices"
	"strings"
	"testing"
)

var verb = regexp.MustCompile(`%[-+# 0]*[0-9]*(\.[0-9]+)?[a-zA-Z%]`)

// Every text exists in both languages with the same format verbs in the
// same order, so translations cannot drop or swap an argument.
func TestCatalog(t *testing.T) {
	for key, e := range catalog {
		if strings.TrimSpace(e.de) == "" || strings.TrimSpace(e.en) == "" {
			t.Errorf("%s: missing translation", key)
		}
		if de, en := verb.FindAllString(e.de, -1), verb.FindAllString(e.en, -1); !slices.Equal(de, en) {
			t.Errorf("%s: verbs differ: de %v, en %v", key, de, en)
		}
	}
}

func TestParseResolveAndT(t *testing.T) {
	for in, want := range map[string]Lang{"de": DE, "de-DE": DE, "de_AT.UTF-8": DE, "DE": DE, "en-US": EN, "fr_FR": EN, "": EN} {
		if got := Parse(in); got != want {
			t.Errorf("Parse(%q) = %s, want %s", in, got, want)
		}
	}
	if Resolve("de") != DE || Resolve("en") != EN {
		t.Error("explicit setting ignored")
	}
	t.Setenv("PERSONALRAG_LANG", "de")
	if Resolve("") != DE {
		t.Error("PERSONALRAG_LANG ignored")
	}
	if Resolve("en") != EN {
		t.Error("setting must win over PERSONALRAG_LANG")
	}

	Set(DE)
	if got := T("folder.notADir", "/x"); got != "/x ist kein Ordner" {
		t.Errorf("T de = %q", got)
	}
	Set(EN)
	if got := T("folder.notADir", "/x"); got != "/x is not a folder" {
		t.Errorf("T en = %q", got)
	}
	if T("no.such.key") != "no.such.key" {
		t.Error("missing key should return the key")
	}
	base := errors.New("boom")
	if err := Errorf("core.openIndex", base); !errors.Is(err, base) || err.Error() != "Opening the index: boom" {
		t.Errorf("Errorf = %v", err)
	}
}
