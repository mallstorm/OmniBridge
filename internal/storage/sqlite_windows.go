//go:build windows

package storage

import (
	"fmt"
	"syscall"
	"unsafe"
)

const (
	sqliteOK            = 0
	sqliteRow           = 100
	sqliteDone          = 101
	sqliteInteger       = 1
	sqliteFloat         = 2
	sqliteText          = 3
	sqliteNull          = 5
	sqliteOpenReadWrite = 0x00000002
	sqliteOpenCreate    = 0x00000004
	sqliteOpenFullMutex = 0x00010000
)

var (
	sqliteDLL    = syscall.NewLazyDLL("winsqlite3.dll")
	kernel32     = syscall.NewLazyDLL("kernel32.dll")
	pLstrlenA    = kernel32.NewProc("lstrlenA")
	pOpen        = sqliteDLL.NewProc("sqlite3_open_v2")
	pClose       = sqliteDLL.NewProc("sqlite3_close_v2")
	pExec        = sqliteDLL.NewProc("sqlite3_exec")
	pErrMsg      = sqliteDLL.NewProc("sqlite3_errmsg")
	pPrepare     = sqliteDLL.NewProc("sqlite3_prepare_v2")
	pStep        = sqliteDLL.NewProc("sqlite3_step")
	pFinalize    = sqliteDLL.NewProc("sqlite3_finalize")
	pColumnCount = sqliteDLL.NewProc("sqlite3_column_count")
	pColumnName  = sqliteDLL.NewProc("sqlite3_column_name")
	pColumnType  = sqliteDLL.NewProc("sqlite3_column_type")
	pColumnText  = sqliteDLL.NewProc("sqlite3_column_text")
	pColumnBytes = sqliteDLL.NewProc("sqlite3_column_bytes")
	pColumnInt64 = sqliteDLL.NewProc("sqlite3_column_int64")
	pFree        = sqliteDLL.NewProc("sqlite3_free")
)

type winConn struct{ db uintptr }

type rawSliceHeader struct {
	Data uintptr
	Len  int
	Cap  int
}

func byteString(ptr uintptr, n int) string {
	if ptr == 0 || n <= 0 {
		return ""
	}
	var b []byte
	*(*rawSliceHeader)(unsafe.Pointer(&b)) = rawSliceHeader{Data: ptr, Len: n, Cap: n}
	return string(b)
}

func cstr(ptr uintptr) string {
	if ptr == 0 {
		return ""
	}
	n, _, _ := pLstrlenA.Call(ptr)
	return byteString(ptr, int(n))
}
func nativeOpen(path string) (nativeConn, error) {
	if err := sqliteDLL.Load(); err != nil {
		return nil, fmt.Errorf("load winsqlite3.dll: %w", err)
	}
	p, err := syscall.BytePtrFromString(path)
	if err != nil {
		return nil, err
	}
	var db uintptr
	rc, _, _ := pOpen.Call(uintptr(unsafe.Pointer(p)), uintptr(unsafe.Pointer(&db)), sqliteOpenReadWrite|sqliteOpenCreate|sqliteOpenFullMutex, 0)
	if int(rc) != sqliteOK {
		return nil, fmt.Errorf("sqlite open rc=%d", rc)
	}
	return &winConn{db: db}, nil
}
func (c *winConn) errmsg() string { p, _, _ := pErrMsg.Call(c.db); return cstr(p) }
func (c *winConn) close() error {
	if c.db == 0 {
		return nil
	}
	rc, _, _ := pClose.Call(c.db)
	if int(rc) != sqliteOK {
		return fmt.Errorf("sqlite close: %s (rc=%d)", c.errmsg(), rc)
	}
	c.db = 0
	return nil
}
func (c *winConn) exec(sql string) error {
	p, err := syscall.BytePtrFromString(sql)
	if err != nil {
		return err
	}
	var em uintptr
	rc, _, _ := pExec.Call(c.db, uintptr(unsafe.Pointer(p)), 0, 0, uintptr(unsafe.Pointer(&em)))
	if int(rc) != sqliteOK {
		msg := c.errmsg()
		if em != 0 {
			msg = cstr(em)
			pFree.Call(em)
		}
		return fmt.Errorf("sqlite exec: %s (rc=%d)", msg, rc)
	}
	return nil
}
func (c *winConn) query(sql string) ([]Row, error) {
	p, err := syscall.BytePtrFromString(sql)
	if err != nil {
		return nil, err
	}
	var st uintptr
	rc, _, _ := pPrepare.Call(c.db, uintptr(unsafe.Pointer(p)), uintptr(^uint32(0)), uintptr(unsafe.Pointer(&st)), 0)
	if int(rc) != sqliteOK {
		return nil, fmt.Errorf("sqlite prepare: %s (rc=%d)", c.errmsg(), rc)
	}
	defer pFinalize.Call(st)
	cc, _, _ := pColumnCount.Call(st)
	cols := int(cc)
	names := make([]string, cols)
	for i := 0; i < cols; i++ {
		np, _, _ := pColumnName.Call(st, uintptr(i))
		names[i] = cstr(np)
	}
	out := []Row{}
	for {
		rc, _, _ = pStep.Call(st)
		if int(rc) == sqliteDone {
			break
		}
		if int(rc) != sqliteRow {
			return nil, fmt.Errorf("sqlite step: %s (rc=%d)", c.errmsg(), rc)
		}
		r := Row{}
		for i, n := range names {
			tp, _, _ := pColumnType.Call(st, uintptr(i))
			switch int(tp) {
			case sqliteInteger:
				v, _, _ := pColumnInt64.Call(st, uintptr(i))
				r[n] = int64(v)
			case sqliteFloat:
				ptr, _, _ := pColumnText.Call(st, uintptr(i))
				ln, _, _ := pColumnBytes.Call(st, uintptr(i))
				if ptr == 0 || ln == 0 {
					r[n] = ""
				} else {
					r[n] = byteString(ptr, int(ln))
				}
			case sqliteNull:
				r[n] = nil
			case sqliteText:
				fallthrough
			default:
				ptr, _, _ := pColumnText.Call(st, uintptr(i))
				ln, _, _ := pColumnBytes.Call(st, uintptr(i))
				if ptr == 0 || ln == 0 {
					r[n] = ""
				} else {
					r[n] = byteString(ptr, int(ln))
				}
			}
		}
		out = append(out, r)
	}
	return out, nil
}
