//go:build !darwin && !windows

package i18n

func systemLocale() string { return envLocale() }
