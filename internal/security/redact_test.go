package security

import (
	"bytes"
	"strings"
	"testing"
)

func TestRedactString(t *testing.T) {
	in := "Authorization: Bearer sk-secret-123456789 x-api-key=AIzaabcdefghijklmnopqrstuvwxyz refresh_token=abc123"
	got := RedactString(in)
	if strings.Contains(got, "sk-secret") || strings.Contains(got, "AIza") || strings.Contains(got, "abc123") {
		t.Fatalf("secret leaked: %s", got)
	}
}

func TestRedactingWriter(t *testing.T) {
	var b bytes.Buffer
	w := NewRedactingWriter(&b)
	n, err := w.Write([]byte("Authorization=Bearer token-value\n"))
	if err != nil || n == 0 || strings.Contains(b.String(), "token-value") {
		t.Fatalf("n=%d err=%v body=%q", n, err, b.String())
	}
}
