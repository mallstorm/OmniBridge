//go:build !windows && !cgo

package storage

import "errors"

func nativeOpen(path string) (nativeConn, error) {
	return nil, errors.New("SQLite support requires cgo on non-Windows builds; Windows uses winsqlite3.dll without cgo")
}
