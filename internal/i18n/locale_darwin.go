package i18n

import (
	"os/exec"
	"strings"
)

// systemLocale reads the first preferred language from the macOS settings;
// apps started from Finder have no LANG.
func systemLocale() string {
	out, err := exec.Command("defaults", "read", "-g", "AppleLanguages").Output()
	if err == nil {
		for _, line := range strings.Split(string(out), "\n") {
			l := strings.Trim(strings.TrimSpace(line), `",()`)
			if l != "" {
				return l
			}
		}
	}
	return envLocale()
}
