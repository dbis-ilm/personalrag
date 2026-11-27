//go:build darwin && cgo

package ocr

/*
#cgo CFLAGS: -x objective-c -fobjc-arc
#cgo LDFLAGS: -framework Foundation -framework CoreGraphics -framework Vision
#include <stdlib.h>

int prag_pdf_pages(const char *path);
char *prag_ocr_page(const char *path, int page, char **err);
*/
import "C"

import (
	"encoding/json"
	"errors"
	"unsafe"
)

func available() bool { return true }

func pageCount(path string) (int, error) {
	cp := C.CString(path)
	defer C.free(unsafe.Pointer(cp))
	n := int(C.prag_pdf_pages(cp))
	if n < 0 {
		return 0, errors.New("PDF cannot be opened")
	}
	return n, nil
}

func recognizePage(path string, page int) ([]Line, error) {
	cp := C.CString(path)
	defer C.free(unsafe.Pointer(cp))
	var cerr *C.char
	out := C.prag_ocr_page(cp, C.int(page), &cerr)
	if cerr != nil {
		defer C.free(unsafe.Pointer(cerr))
		return nil, errors.New(C.GoString(cerr))
	}
	if out == nil {
		return nil, nil
	}
	defer C.free(unsafe.Pointer(out))
	var lines []Line
	if err := json.Unmarshal([]byte(C.GoString(out)), &lines); err != nil {
		return nil, err
	}
	return lines, nil
}
