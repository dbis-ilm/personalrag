package i18n

import "os"

// envLocale follows the POSIX order: LC_ALL, LC_MESSAGES, LANG, LANGUAGE.
func envLocale() string {
	for _, k := range []string{"LC_ALL", "LC_MESSAGES", "LANG", "LANGUAGE"} {
		if v := os.Getenv(k); v != "" && v != "C" && v != "POSIX" {
			return v
		}
	}
	return ""
}
