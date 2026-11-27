//go:build !darwin || !cgo

package ocr

func available() bool { return false }

func pageCount(string) (int, error) { return 0, ErrUnavailable }

func recognizePage(string, int) ([]Line, error) { return nil, ErrUnavailable }
