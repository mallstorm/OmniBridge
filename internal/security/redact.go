package security

import (
	"io"
	"regexp"
	"strings"
	"sync"
)

// RedactingWriter removes common credential/token shapes before they reach logs.
// It is intentionally conservative: values are replaced, while surrounding
// diagnostic context is preserved.
type RedactingWriter struct {
	dst io.Writer
	mu  sync.Mutex
}

func NewRedactingWriter(dst io.Writer) io.Writer { return &RedactingWriter{dst: dst} }

var redactPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)(authorization\s*[:=]\s*bearer\s+)[^\s\"']+`),
	regexp.MustCompile(`(?i)((?:x-api-key|api-key|x-bridge-key)\s*[:=]\s*)[^\s\"']+`),
	regexp.MustCompile(`(?i)(refresh[_ -]?token\s*[:=]\s*)[^\s\"']+`),
	regexp.MustCompile(`(?i)(access[_ -]?token\s*[:=]\s*)[^\s\"']+`),
	regexp.MustCompile(`\bsk-[A-Za-z0-9_\-]{8,}\b`),
	regexp.MustCompile(`\bAIza[A-Za-z0-9_\-]{16,}\b`),
	regexp.MustCompile(`\b[a-fA-F0-9]{64}\b`),
}

func RedactString(s string) string {
	out := s
	for i, re := range redactPatterns {
		switch i {
		case 0, 1, 2, 3:
			out = re.ReplaceAllString(out, `${1}[REDACTED]`)
		default:
			out = re.ReplaceAllString(out, `[REDACTED]`)
		}
	}
	// Avoid accidentally logging huge credential-like compact blobs.
	fields := strings.Fields(out)
	for _, f := range fields {
		if len(f) > 180 && !strings.Contains(f, "/") {
			out = strings.ReplaceAll(out, f, "[REDACTED-LONG-TOKEN]")
		}
	}
	return out
}

func (w *RedactingWriter) Write(p []byte) (int, error) {
	clean := []byte(RedactString(string(p)))
	w.mu.Lock()
	defer w.mu.Unlock()
	if _, err := w.dst.Write(clean); err != nil {
		return 0, err
	}
	// Report the original length so log.Logger does not treat redaction as a short write.
	return len(p), nil
}
