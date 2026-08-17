//go:build !windows && cgo

package storage

/*
#cgo LDFLAGS: -lsqlite3
#include <sqlite3.h>
#include <stdlib.h>
*/
import "C"

import (
	"fmt"
	"unsafe"
)

type cgoConn struct{ db *C.sqlite3 }

func nativeOpen(path string) (nativeConn, error) {
	cp := C.CString(path)
	defer C.free(unsafe.Pointer(cp))
	var db *C.sqlite3
	flags := C.int(C.SQLITE_OPEN_READWRITE | C.SQLITE_OPEN_CREATE | C.SQLITE_OPEN_FULLMUTEX)
	if rc := C.sqlite3_open_v2(cp, &db, flags, nil); rc != C.SQLITE_OK {
		msg := "sqlite open failed"
		if db != nil {
			msg = C.GoString(C.sqlite3_errmsg(db))
			C.sqlite3_close_v2(db)
		}
		return nil, fmt.Errorf("%s (rc=%d)", msg, int(rc))
	}
	return &cgoConn{db: db}, nil
}
func (c *cgoConn) close() error {
	if c.db == nil {
		return nil
	}
	rc := C.sqlite3_close_v2(c.db)
	if rc != C.SQLITE_OK {
		return fmt.Errorf("sqlite close rc=%d", int(rc))
	}
	c.db = nil
	return nil
}
func (c *cgoConn) exec(sql string) error {
	cs := C.CString(sql)
	defer C.free(unsafe.Pointer(cs))
	var em *C.char
	rc := C.sqlite3_exec(c.db, cs, nil, nil, &em)
	if rc != C.SQLITE_OK {
		msg := C.GoString(C.sqlite3_errmsg(c.db))
		if em != nil {
			msg = C.GoString(em)
			C.sqlite3_free(unsafe.Pointer(em))
		}
		return fmt.Errorf("sqlite exec: %s (rc=%d)", msg, int(rc))
	}
	return nil
}
func (c *cgoConn) query(sql string) ([]Row, error) {
	cs := C.CString(sql)
	defer C.free(unsafe.Pointer(cs))
	var st *C.sqlite3_stmt
	rc := C.sqlite3_prepare_v2(c.db, cs, -1, &st, nil)
	if rc != C.SQLITE_OK {
		return nil, fmt.Errorf("sqlite prepare: %s", C.GoString(C.sqlite3_errmsg(c.db)))
	}
	defer C.sqlite3_finalize(st)
	cols := int(C.sqlite3_column_count(st))
	names := make([]string, cols)
	for i := 0; i < cols; i++ {
		names[i] = C.GoString(C.sqlite3_column_name(st, C.int(i)))
	}
	out := []Row{}
	for {
		rc = C.sqlite3_step(st)
		if rc == C.SQLITE_DONE {
			break
		}
		if rc != C.SQLITE_ROW {
			return nil, fmt.Errorf("sqlite step: %s", C.GoString(C.sqlite3_errmsg(c.db)))
		}
		r := Row{}
		for i, n := range names {
			t := C.sqlite3_column_type(st, C.int(i))
			switch t {
			case C.SQLITE_INTEGER:
				r[n] = int64(C.sqlite3_column_int64(st, C.int(i)))
			case C.SQLITE_FLOAT:
				r[n] = float64(C.sqlite3_column_double(st, C.int(i)))
			case C.SQLITE_NULL:
				r[n] = nil
			default:
				p := C.sqlite3_column_text(st, C.int(i))
				if p == nil {
					r[n] = ""
				} else {
					r[n] = C.GoString((*C.char)(unsafe.Pointer(p)))
				}
			}
		}
		out = append(out, r)
	}
	return out, nil
}
