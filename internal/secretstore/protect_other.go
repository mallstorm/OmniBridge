//go:build !windows

package secretstore

// Test/development fallback on non-Windows. Release target is Windows and uses DPAPI.
func protect(in []byte) ([]byte, error)   { out := append([]byte(nil), in...); return out, nil }
func unprotect(in []byte) ([]byte, error) { out := append([]byte(nil), in...); return out, nil }
