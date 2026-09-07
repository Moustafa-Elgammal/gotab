package darwin

/*
#include <stdlib.h>
#include "prefs.h"
*/
import "C"

import (
	"errors"
	"strings"
	"unsafe"
)

// Prefs implements prefs.Reader and prefs.Writer over the macOS CFPreferences domain for this
// application. internal/prefs owns the schema and the defaults; this type is only the typed key I/O
// (see prefs.h). The zero value is ready to use.
//
// Every method is safe from any goroutine — CFPreferences is thread-safe — but a read right after a
// write in another process can miss it until cfprefsd propagates; that race does not matter here,
// where gotab reads its settings once at startup.
type Prefs struct{}

func prefsCStr(s string) (*C.char, func()) {
	p := C.CString(s)
	return p, func() { C.free(unsafe.Pointer(p)) }
}

// prefsStrCap is the first-try buffer for a string read; a longer value triggers one exact re-read.
const prefsStrCap = 4096

func (Prefs) Bool(key string) (bool, bool) {
	k, free := prefsCStr(key)
	defer free()
	var out C.int32_t
	if C.gt_prefs_get_bool(k, &out) == 1 {
		return out != 0, true
	}
	return false, false
}

func (Prefs) Int(key string) (int, bool) {
	k, free := prefsCStr(key)
	defer free()
	var out C.int64_t
	if C.gt_prefs_get_i64(k, &out) == 1 {
		return int(out), true
	}
	return 0, false
}

func (Prefs) String(key string) (string, bool) {
	k, free := prefsCStr(key)
	defer free()
	buf := make([]byte, prefsStrCap)
	var n C.int32_t
	if C.gt_prefs_get_str(k, (*C.char)(unsafe.Pointer(&buf[0])), C.int32_t(len(buf)), &n) != 1 {
		return "", false
	}
	if int(n) > len(buf)-1 {
		buf = make([]byte, int(n)+1)
		C.gt_prefs_get_str(k, (*C.char)(unsafe.Pointer(&buf[0])), C.int32_t(len(buf)), &n)
	}
	return string(buf[:n]), true
}

func (Prefs) Strings(key string) ([]string, bool) {
	k, free := prefsCStr(key)
	defer free()
	buf := make([]byte, prefsStrCap)
	var count, n C.int32_t
	if C.gt_prefs_get_strs(k, (*C.char)(unsafe.Pointer(&buf[0])), C.int32_t(len(buf)), &count, &n) != 1 {
		return nil, false
	}
	if int(n) > len(buf)-1 {
		buf = make([]byte, int(n)+1)
		C.gt_prefs_get_strs(k, (*C.char)(unsafe.Pointer(&buf[0])), C.int32_t(len(buf)), &count, &n)
	}
	if count == 0 {
		return nil, true
	}
	return strings.Split(string(buf[:n]), "\n"), true
}

func (Prefs) SetBool(key string, v bool) {
	k, free := prefsCStr(key)
	defer free()
	b := C.int32_t(0)
	if v {
		b = 1
	}
	C.gt_prefs_set_bool(k, b)
}

func (Prefs) SetInt(key string, v int) {
	k, free := prefsCStr(key)
	defer free()
	C.gt_prefs_set_i64(k, C.int64_t(v))
}

func (Prefs) SetString(key, v string) {
	k, free := prefsCStr(key)
	defer free()
	cv, freeV := prefsCStr(v)
	defer freeV()
	C.gt_prefs_set_str(k, cv)
}

func (Prefs) SetStrings(key string, v []string) {
	k, free := prefsCStr(key)
	defer free()
	j, freeJ := prefsCStr(strings.Join(v, "\n"))
	defer freeJ()
	C.gt_prefs_set_strs(k, j, C.int32_t(len(v)))
}

func (Prefs) Sync() error {
	if C.gt_prefs_sync() != 1 {
		return errors.New("darwin: prefs sync: CFPreferencesAppSynchronize failed")
	}
	return nil
}
