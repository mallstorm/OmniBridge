//go:build windows

package secretstore

import (
	"errors"
	"syscall"
	"unsafe"
)

type dataBlob struct {
	cbData uint32
	pbData *byte
}

var (
	crypt32            = syscall.NewLazyDLL("crypt32.dll")
	kernel32           = syscall.NewLazyDLL("kernel32.dll")
	cryptProtectData   = crypt32.NewProc("CryptProtectData")
	cryptUnprotectData = crypt32.NewProc("CryptUnprotectData")
	localFree          = kernel32.NewProc("LocalFree")
)

func blobFromBytes(b []byte) dataBlob {
	if len(b) == 0 {
		return dataBlob{}
	}
	return dataBlob{cbData: uint32(len(b)), pbData: &b[0]}
}

func bytesFromBlob(b dataBlob) []byte {
	if b.cbData == 0 || b.pbData == nil {
		return nil
	}
	src := unsafe.Slice(b.pbData, int(b.cbData))
	out := make([]byte, len(src))
	copy(out, src)
	return out
}

func protect(in []byte) ([]byte, error) {
	ib := blobFromBytes(in)
	var ob dataBlob
	r, _, e := cryptProtectData.Call(uintptr(unsafe.Pointer(&ib)), 0, 0, 0, 0, 0x1, uintptr(unsafe.Pointer(&ob))) // CRYPTPROTECT_UI_FORBIDDEN
	if r == 0 {
		if e != syscall.Errno(0) {
			return nil, e
		}
		return nil, errors.New("CryptProtectData failed")
	}
	defer localFree.Call(uintptr(unsafe.Pointer(ob.pbData)))
	return bytesFromBlob(ob), nil
}

func unprotect(in []byte) ([]byte, error) {
	ib := blobFromBytes(in)
	var ob dataBlob
	r, _, e := cryptUnprotectData.Call(uintptr(unsafe.Pointer(&ib)), 0, 0, 0, 0, 0x1, uintptr(unsafe.Pointer(&ob)))
	if r == 0 {
		if e != syscall.Errno(0) {
			return nil, e
		}
		return nil, errors.New("CryptUnprotectData failed")
	}
	defer localFree.Call(uintptr(unsafe.Pointer(ob.pbData)))
	return bytesFromBlob(ob), nil
}
