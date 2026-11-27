package i18n

import "golang.org/x/sys/windows"

// systemLocale returns the first language of the Windows display languages.
func systemLocale() string {
	const muiLanguageName = 0x8
	if langs, err := windows.GetUserPreferredUILanguages(muiLanguageName); err == nil && len(langs) > 0 {
		return langs[0]
	}
	return envLocale()
}
